// Package gm wraps go.mau.fi/mautrix-gmessages/pkg/libgm with the conventions
// gmcli needs: filesystem-backed AuthData persistence, an event subscriber
// model on top of libgm's single SetEventHandler, and helpers for the QR
// pairing flow.
//
// Two entry points cover the lifecycle:
//
//	PairGoogle(ctx, layout, logger, cookieFile, render) // writes session.json
//	Open(layout, logger) -> *Client                     // ready to Connect()
//
// The wrapper does not own a goroutine of its own; libgm runs the long-poll.
// Subscribers must not block in their handlers.
package gm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/johnlindquist/gmkit/internal/paths"
)

// PairTimeout is the upper bound on how long we wait for a phone to scan the
// QR code. Google's relay drops unfinished pairings after a few minutes.
const PairTimeout = 5 * time.Minute

// sendMetadataTimeout bounds how long a write waits for the phone settings
// event that carries SIM metadata. mautrix/gmessages includes this metadata
// in send requests; sending without it can be accepted but not actually sent
// on some devices.
const sendMetadataTimeout = 10 * time.Second

// phonePingInterval spaces libgm's unprompted NotifyDittoActivity pings to the
// phone. Each one makes Google Messages take its ditto wake lock and start its
// foreground service for about a minute, so libgm's raw 1-minute default kept
// her phone awake around the clock under the always-on serve unit (graph
// nb9cc95e45a). Requests that stall still ping at once through libgm's short
// circuit, and receiving and token refresh run on the long poll, not on pings.
// SPEC-AMBIGUITY: (2026-10-27 04:50) Fix asked for "the cleanest change" to stop the relay keeping Messages awake, but this could mean
// 1. no unprompted pings at all (libgm has no off switch; a huge interval fakes one)
// 2. the interval mautrix's own bridge ships with libgm, 20m
// 3. some other number
// Choice defaulted to: 2 because it is the value the library's maintainers run in production, keeping phone-offline detection while cutting the wakes 20x
const phonePingInterval = 20 * time.Minute

// EventHandler is invoked for each event delivered by libgm. The argument
// type is one of the concrete types in pkg/libgm/events or pkg/libgm/gmproto.
type EventHandler func(evt any)

// Client is a thin wrapper around *libgm.Client adding fan-out event
// subscription, persistence of the pairing record, and the pairing verdict.
type Client struct {
	libgm  *libgm.Client
	record *record
	layout paths.Layout
	logger zerolog.Logger

	mu          sync.RWMutex
	subscribers []EventHandler
	settings    *gmproto.Settings

	// facts is the one place the pairing's health is decided (verdict.go).
	// publishMu keeps verdict changes reaching subscribers in order.
	factsMu   sync.Mutex
	facts     *verdictFacts
	publishMu sync.Mutex
}

// VerdictChanged is dispatched to subscribers each time the verdict's state
// or reason changes.
type VerdictChanged struct{ Verdict }

// libgmDecodeFailure is the message libgm logs, and only logs, when a phone
// payload fails to decrypt (event_handler.go HandleRPCMsg).
const libgmDecodeFailure = "Failed to decode incoming RPC message"

// Open loads session.json and returns a connected-but-not-yet-Connect()'d
// Client. Returns an error if no session exists — the caller must pair
// first.
func Open(layout paths.Layout, logger zerolog.Logger) (*Client, error) {
	rec, err := loadRecord(layout.Session)
	if err != nil {
		return nil, err
	}
	if rec.auth.Browser == nil {
		return nil, fmt.Errorf("session %s has no paired device; run `gmcli auth google --cookie-file PATH` first", layout.Session)
	}
	c := &Client{
		record: rec,
		layout: layout,
		logger: logger,
		facts:  newVerdictFacts(time.Now()),
	}
	// zerolog runs hooks only for enabled levels, and the decode failure is
	// an error-level line: the verdict needs it whatever level was asked for.
	libgmLogger := logger
	if logger.GetLevel() > zerolog.ErrorLevel {
		libgmLogger = logger.Level(zerolog.ErrorLevel)
	}
	libgmLogger = libgmLogger.Hook(zerolog.HookFunc(func(_ *zerolog.Event, _ zerolog.Level, msg string) {
		if msg == libgmDecodeFailure {
			c.observe(func(f *verdictFacts) { f.decryptFailed(time.Now()) })
		}
	}))
	c.libgm = libgm.NewClient(rec.auth, nil, libgmLogger, httpSettings(profileSource(rec.cookieFile, relayGoogleCookies), c))
	c.libgm.SetPingInterval(phonePingInterval)
	c.libgm.SetEventHandler(c.dispatch)
	return c, nil
}

// CookieFile is the Chrome profile cookie database this pairing reads its
// Google account credentials from ("" for a QR pairing).
func (c *Client) CookieFile() string { return c.record.cookieFile }

// Verdict returns the current pairing verdict.
func (c *Client) Verdict() Verdict {
	c.factsMu.Lock()
	defer c.factsMu.Unlock()
	return c.facts.published
}

// observe applies one fact and dispatches the verdict if it changed.
func (c *Client) observe(apply func(*verdictFacts)) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.factsMu.Lock()
	apply(c.facts)
	v, changed := c.facts.verdict(time.Now())
	c.factsMu.Unlock()
	if changed {
		c.fanOut(&VerdictChanged{v})
	}
}

func (c *Client) sourceUnreadable(err error) {
	c.observe(func(f *verdictFacts) { f.sourceUnreadable(err) })
}

func (c *Client) relayAnswered(a relayAnswer) {
	c.observe(func(f *verdictFacts) { f.relayAnswered(a) })
}

// Subscribe registers a handler. Multiple subscribers receive each event in
// the order they were registered. Handlers must not block.
func (c *Client) Subscribe(h EventHandler) {
	c.mu.Lock()
	c.subscribers = append(c.subscribers, h)
	c.mu.Unlock()
}

// Connect opens the long-poll connection. Events flow to subscribers
// asynchronously. Call WaitForReady after Connect before phone mutations.
func (c *Client) Connect() error {
	return c.libgm.Connect(context.Background())
}

// Disconnect closes the long-poll. Safe to call multiple times.
func (c *Client) Disconnect() {
	c.libgm.Disconnect()
}

// IsConnected reports whether the long-poll is currently active.
func (c *Client) IsConnected() bool {
	return c.libgm.IsConnected()
}

// WaitForReady waits for the phone settings delivered by GET_UPDATES during
// connection setup. libgm v0.6.4 does not emit ClientReady; a live HTTP poll
// alone also does not establish that the paired phone has responded.
func (c *Client) WaitForReady(ctx context.Context) error {
	return c.WaitForSettings(ctx)
}

// Underlying returns the wrapped *libgm.Client for callers that need access
// to libgm methods we haven't surfaced yet (ListContacts, FetchMessages,
// etc.). Higher-level operations should prefer the typed wrappers below.
func (c *Client) Underlying() *libgm.Client { return c.libgm }

// SendTextResult describes a successful send.
type SendTextResult struct {
	MessageID      string
	ConversationID string
	TmpID          string
}

// SendText sends a text message into the given conversation. ReplyToID is
// optional; when set, the new message is rendered as a quoted reply by the
// recipient's client. The libgm long-poll must be Connected; call
// WaitForReady first for fresh sessions.
func (c *Client) SendText(ctx context.Context, conversationID, body, replyToID string) (*SendTextResult, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if body == "" {
		return nil, fmt.Errorf("message body is required")
	}
	settingsCtx, cancel := context.WithTimeout(ctx, sendMetadataTimeout)
	defer cancel()
	if err := c.WaitForSettings(settingsCtx); err != nil {
		return nil, fmt.Errorf("wait for phone send settings: %w", err)
	}
	tmpID := uuid.NewString()
	req, err := c.buildSendTextRequest(conversationID, body, replyToID, tmpID)
	if err != nil {
		return nil, err
	}
	waitEcho, unsubscribe := c.watchMessageEcho(tmpID)
	defer unsubscribe()

	resp, err := c.libgm.SendMessage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("libgm send: %w", err)
	}
	if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return nil, fmt.Errorf("send rejected by phone: %s", sendStatusMessage(resp))
	}

	echo, err := waitEcho(ctx)
	if err != nil {
		return nil, fmt.Errorf("send accepted by phone, but no sent-message echo arrived for tmp_id %s: %w", tmpID, err)
	}
	return &SendTextResult{
		MessageID:      echo.Message.GetMessageID(),
		ConversationID: echo.Message.GetConversationID(),
		TmpID:          tmpID,
	}, nil
}

func (c *Client) buildSendTextRequest(conversationID, body, replyToID, tmpID string) (*gmproto.SendMessageRequest, error) {
	conv, err := c.libgm.GetConversation(context.Background(), conversationID)
	if err != nil {
		return nil, fmt.Errorf("get conversation %s before send: %w", conversationID, err)
	}
	outgoingID := conv.GetDefaultOutgoingID()
	if outgoingID == "" {
		return nil, fmt.Errorf("conversation %s has no default outgoing participant; cannot choose sender/SIM", conversationID)
	}
	req := &gmproto.SendMessageRequest{
		ConversationID: conversationID,
		TmpID:          tmpID,
		MessagePayload: &gmproto.MessagePayload{
			ConversationID: conversationID,
			ParticipantID:  outgoingID,
			TmpID:          tmpID,
			TmpID2:         tmpID,
			MessageInfo: []*gmproto.MessageInfo{{
				Data: &gmproto.MessageInfo_MessageContent{
					MessageContent: &gmproto.MessageContent{Content: body},
				},
			}},
		},
	}
	if sim := c.simForParticipant(outgoingID); sim != nil {
		req.SIMPayload = sim.GetSIMData().GetSIMPayload()
	} else {
		return nil, fmt.Errorf("conversation %s uses outgoing participant %s, but no matching SIM metadata was received", conversationID, outgoingID)
	}
	if replyToID != "" {
		req.Reply = &gmproto.ReplyPayload{MessageID: replyToID}
	}
	return req, nil
}

// WaitForSettings blocks until libgm emits the phone settings event. Send
// requests need its SIM metadata to match the browser client shape.
func (c *Client) WaitForSettings(ctx context.Context) error {
	c.mu.RLock()
	hasSettings := c.settings != nil
	c.mu.RUnlock()
	if hasSettings {
		return nil
	}

	ready := make(chan struct{}, 1)
	var fired sync.Once
	c.mu.Lock()
	idx := len(c.subscribers)
	c.subscribers = append(c.subscribers, func(evt any) {
		if _, ok := evt.(*gmproto.Settings); ok {
			fired.Do(func() { close(ready) })
		}
	})
	if c.settings != nil {
		fired.Do(func() { close(ready) })
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if idx < len(c.subscribers) {
			c.subscribers = append(c.subscribers[:idx], c.subscribers[idx+1:]...)
		}
		c.mu.Unlock()
	}()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) simForParticipant(participantID string) *gmproto.SIMCard {
	c.mu.RLock()
	settings := c.settings
	c.mu.RUnlock()
	if settings == nil {
		return nil
	}
	for _, sim := range settings.GetSIMCards() {
		if sim.GetSIMParticipant().GetID() == participantID {
			return sim
		}
	}
	return nil
}

func (c *Client) watchMessageEcho(tmpID string) (func(context.Context) (*libgm.WrappedMessage, error), func()) {
	echo := make(chan *libgm.WrappedMessage, 1)
	c.mu.Lock()
	idx := len(c.subscribers)
	c.subscribers = append(c.subscribers, func(evt any) {
		w, ok := evt.(*libgm.WrappedMessage)
		if !ok || w.Message.GetTmpID() != tmpID {
			return
		}
		select {
		case echo <- w:
		default:
		}
	})
	c.mu.Unlock()

	unsubscribe := func() {
		c.mu.Lock()
		if idx < len(c.subscribers) {
			c.subscribers = append(c.subscribers[:idx], c.subscribers[idx+1:]...)
		}
		c.mu.Unlock()
	}
	wait := func(ctx context.Context) (*libgm.WrappedMessage, error) {
		select {
		case w := <-echo:
			return w, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return wait, unsubscribe
}

func sendStatusMessage(resp *gmproto.SendMessageResponse) string {
	switch resp.GetStatus() {
	case gmproto.SendMessageResponse_UNKNOWN:
		if resp.GetGoogleAccountSwitch() != nil {
			return "switch back to QR pairing or log in with Google account to send messages"
		}
		return "unknown status"
	case gmproto.SendMessageResponse_FAILURE_2:
		return "unknown permanent error"
	case gmproto.SendMessageResponse_FAILURE_3:
		return "unknown temporary error"
	case gmproto.SendMessageResponse_FAILURE_4:
		return "Google Messages is not your default SMS app"
	default:
		return resp.GetStatus().String()
	}
}

// ReactionAction selects ADD / REMOVE / SWITCH semantics on SendReaction.
type ReactionAction int

const (
	ReactionAdd ReactionAction = iota
	ReactionRemove
	ReactionSwitch
)

// SendReaction adds, removes, or switches a unicode reaction on a message.
func (c *Client) SendReaction(messageID, emoji string, action ReactionAction) error {
	if messageID == "" {
		return fmt.Errorf("message id is required")
	}
	if emoji == "" {
		return fmt.Errorf("emoji is required")
	}
	var act gmproto.SendReactionRequest_Action
	switch action {
	case ReactionAdd:
		act = gmproto.SendReactionRequest_ADD
	case ReactionRemove:
		act = gmproto.SendReactionRequest_REMOVE
	case ReactionSwitch:
		act = gmproto.SendReactionRequest_SWITCH
	default:
		return fmt.Errorf("unknown reaction action %v", action)
	}
	_, err := c.libgm.SendReaction(context.Background(), &gmproto.SendReactionRequest{
		MessageID:    messageID,
		Action:       act,
		ReactionData: &gmproto.ReactionData{Unicode: emoji},
	})
	if err != nil {
		return fmt.Errorf("libgm reaction: %w", err)
	}
	return nil
}

// DownloadMedia retrieves and decrypts the bytes for an attachment.
// The connection does not need to be in long-poll mode — DownloadMedia
// uses authenticated HTTP — but the AuthData's TachyonAuthToken must be
// fresh. Call Connect once before this if the session has been idle.
func (c *Client) DownloadMedia(mediaID string, key []byte) ([]byte, error) {
	if mediaID == "" {
		return nil, fmt.Errorf("media id is required")
	}
	reader, err := c.libgm.DownloadMedia(mediaID, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// AuthSnapshot returns a deep copy of the current AuthData by JSON
// round-trip. Useful for diagnostics; do not modify.
func (c *Client) AuthSnapshot() (*libgm.AuthData, error) {
	b, err := json.Marshal(c.record.auth)
	if err != nil {
		return nil, err
	}
	var out libgm.AuthData
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// dispatch is the single libgm callback. It persists on token refresh,
// records what the phone said for the verdict, and fans out to subscribers.
func (c *Client) dispatch(evt any) {
	switch e := evt.(type) {
	case *events.AuthTokenRefreshed, *events.PairSuccessful:
		if err := saveRecord(c.layout.Session, c.record); err != nil {
			c.logger.Error().Err(err).Msg("Failed to persist refreshed auth data")
		}
	case *events.GaiaLoggedOut:
		c.observe(func(f *verdictFacts) { f.phoneLoggedOut() })
	case *gmproto.Settings:
		c.mu.Lock()
		c.settings = e
		c.mu.Unlock()
	}
	if phonePayload(evt) {
		c.observe(func(f *verdictFacts) { f.phoneEvent(time.Now()) })
	}
	c.fanOut(evt)
}

// phonePayload reports whether libgm could only have produced evt by
// decrypting something the phone sent.
func phonePayload(evt any) bool {
	switch evt.(type) {
	case *gmproto.Settings, *gmproto.Conversation, *gmproto.UserAlertEvent, *gmproto.TypingData,
		*libgm.WrappedMessage, *events.AccountChange, *events.PhoneRespondingAgain:
		return true
	}
	return false
}

func (c *Client) fanOut(evt any) {
	c.mu.RLock()
	subs := append([]EventHandler(nil), c.subscribers...)
	c.mu.RUnlock()
	for _, h := range subs {
		h(evt)
	}
}

// PairResult is returned by Pair on success. PhoneID identifies the paired
// device; SessionPath is where the persisted AuthData lives.
type PairResult struct {
	PhoneID     string
	SessionPath string
}

// QRRenderer is invoked once Pair has the QR URL ready. The implementation
// is responsible for displaying it (terminal QR, plain URL, etc.).
type QRRenderer func(qrURL string)

// EmojiRenderer is invoked once PairGoogle has the phone confirmation emoji.
type EmojiRenderer func(emoji string)

// Pair runs the QR pairing flow. It writes session.json on success and
// returns the paired phone ID. Cancellable via ctx; otherwise bounded by
// PairTimeout. Existing session.json (if any) is overwritten on success.
func Pair(ctx context.Context, layout paths.Layout, logger zerolog.Logger, render QRRenderer) (*PairResult, error) {
	if err := layout.EnsureDirs(); err != nil {
		return nil, err
	}
	auth := libgm.NewAuthData()
	cli := libgm.NewClient(auth, nil, logger, httpSettings(nil, nil))

	done := make(chan *events.PairSuccessful, 1)
	fatal := make(chan error, 1)
	cli.SetEventHandler(func(evt any) {
		switch e := evt.(type) {
		case *events.PairSuccessful:
			select {
			case done <- e:
			default:
			}
		case *events.ListenFatalError:
			select {
			case fatal <- fmt.Errorf("pairing transport failed: %w", e.Error):
			default:
			}
		}
	})

	qr, err := cli.StartLogin(ctx)
	if err != nil {
		return nil, fmt.Errorf("start login: %w", err)
	}
	render(qr)

	timeout := time.NewTimer(PairTimeout)
	defer timeout.Stop()

	select {
	case <-ctx.Done():
		cli.Disconnect()
		return nil, ctx.Err()
	case err := <-fatal:
		cli.Disconnect()
		return nil, err
	case <-timeout.C:
		cli.Disconnect()
		return nil, errors.New("pairing timed out — phone never scanned the QR code")
	case res := <-done:
		// libgm reconnects internally 2s after PairSuccessful; we're not
		// going to keep this client around, so close down cleanly.
		cli.Disconnect()
		if err := saveRecord(layout.Session, &record{auth: auth}); err != nil {
			return nil, fmt.Errorf("persist session: %w", err)
		}
		return &PairResult{PhoneID: res.PhoneID, SessionPath: layout.Session}, nil
	}
}

// PairGoogle runs the Google account emoji pairing flow as the account
// signed in to the Chrome profile whose cookie database is cookieFile. Every
// request reads that profile's current cookies; session.json records the
// file, never the cookies (record.go). Returns the paired phone ID.
func PairGoogle(ctx context.Context, layout paths.Layout, logger zerolog.Logger, cookieFile string, render EmojiRenderer) (*PairResult, error) {
	if err := layout.EnsureDirs(); err != nil {
		return nil, err
	}
	if cookieFile == "" {
		return nil, errors.New("google account pairing needs the Chrome profile cookie file of the account to pair")
	}

	auth := libgm.NewAuthData()
	auth.SetCookies(map[string]string{})
	cli := libgm.NewClient(auth, nil, logger, httpSettings(profileSource(cookieFile, pairingGoogleCookies), nil))
	defer cli.Disconnect()

	pairCtx, cancel := context.WithTimeout(ctx, PairTimeout)
	defer cancel()

	if err := cli.FetchConfig(pairCtx); err != nil {
		return nil, fmt.Errorf("fetch Google Messages config as %s: %w", cookieFile, err)
	}
	// FetchConfig adopts the cookie owner's browser device ID. Sharing that
	// registration with Chrome makes each pairing replace the other's keys.
	// Google login credentials are shared; this paired device identity is ours.
	auth.SessionID = uuid.New()
	emoji, session, err := cli.StartGaiaPairing(pairCtx, pairCtx)
	if err != nil {
		return nil, fmt.Errorf("start Google account pairing: %w", err)
	}
	render(emoji)
	phoneID, err := cli.FinishGaiaPairing(pairCtx, session)
	if err != nil {
		return nil, fmt.Errorf("finish Google account pairing: %w", err)
	}
	if err := saveRecord(layout.Session, &record{auth: auth, cookieFile: cookieFile}); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}
	return &PairResult{PhoneID: phoneID, SessionPath: layout.Session}, nil
}
