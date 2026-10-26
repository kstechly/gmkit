package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/history"
	"github.com/johnlindquist/gmkit/internal/store"
	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

func (s *Server) dispatch(ctx context.Context, c *serverConn, method string, params json.RawMessage) (any, *Error) {
	switch method {
	case "ping":
		return s.handlePing(ctx)
	case "status":
		return s.handleStatus(ctx)
	case "subscribe":
		c.subscribed.Store(true)
		return map[string]any{"subscribed": true}, nil
	case "chats.verifyArchive":
		return s.handleVerifyArchive(ctx, params)
	case "chats.scan":
		return s.handleChatsScan(ctx, params)
	case "chats.read":
		return s.handleChatsRead(ctx, params)
	case "chats.list":
		return s.handleChatsList(ctx, params)
	case "chats.find":
		return s.handleChatsFind(ctx, params)
	case "chats.archive":
		return s.handleChatsArchive(ctx, params)
	case "chats.show":
		return s.handleChatsShow(ctx, params)
	case "messages.list":
		return s.handleMessagesList(ctx, params)
	case "messages.search":
		return s.handleMessagesSearch(ctx, params)
	case "messages.show":
		return s.handleMessagesShow(ctx, params)
	case "messages.context":
		return s.handleMessagesContext(ctx, params)
	case "contacts.search":
		return s.handleContactsSearch(ctx, params)
	case "contacts.show":
		return s.handleContactsShow(ctx, params)
	case "sync.refresh":
		return s.handleSyncRefresh(ctx)
	case "auth.pair":
		return s.handleAuthPair()
	case "auth.pair.google":
		return s.handleAuthPairGoogle(params)
	case "media.download":
		return s.handleMediaDownload(ctx, params)
	case "alias.set":
		return s.handleAliasSet(ctx, params)
	case "alias.rm":
		return s.handleAliasRm(ctx, params)
	case "send.react":
		return s.handleSendReact(params)
	case "daemon.shutdown":
		// Used by `gmcli auth` after re-pairing: the daemon must restart to
		// load the new session; on-demand clients spawn a fresh one.
		s.requestShutdown()
		return map[string]any{"shutting_down": true}, nil
	case "history.backfill":
		return s.handleHistoryBackfill(ctx, params)
	case "history.lookup":
		return s.handleHistoryLookup(ctx, params)
	case "send.text":
		return s.handleSendText(ctx, params)
	case "approvals.list":
		return s.handleApprovalsList(ctx, params)
	case "approvals.approve":
		return s.handleApprovalsApprove(ctx, params)
	case "approvals.deny":
		return s.handleApprovalsDeny(ctx, params)
	default:
		return nil, &Error{Code: CodeMethodNotFound, Message: "unknown method " + method}
	}
}

func decode[T any](params json.RawMessage) (T, *Error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &Error{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return v, nil
}

func internalErr(err error) *Error {
	return &Error{Code: CodeInternal, Message: err.Error()}
}

func (s *Server) handlePing(ctx context.Context) (any, *Error) {
	version, err := s.deps.Store.SchemaVersion(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	return map[string]any{
		"pong":           true,
		"version":        s.deps.Version,
		"schema_version": version,
	}, nil
}

func (s *Server) handleStatus(ctx context.Context) (any, *Error) {
	sync, err := s.deps.Store.SyncState(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	pending, err := s.deps.Store.ListApprovals(ctx, store.ApprovalPending, 1000)
	if err != nil {
		return nil, internalErr(err)
	}
	conversations, _ := s.deps.Store.CountConversations(ctx)
	messages, _ := s.deps.Store.CountMessages(ctx)
	connected := false
	if s.deps.Client != nil {
		connected = s.deps.Client.IsConnected()
	}
	cov, err := s.deps.Store.CachedCoverage(ctx)
	if err != nil {
		return nil, internalErr(err)
	}
	var verdict *gm.Verdict
	if s.deps.Client != nil {
		v := s.deps.Client.Verdict()
		verdict = &v
	}
	return map[string]any{
		"coverage":          cov,
		"connected":         connected,
		"offline":           s.deps.Client == nil,
		"verdict":           verdict,
		"send_mode":         s.deps.SendMode,
		"pending_approvals": len(pending),
		"conversations":     conversations,
		"messages":          messages,
		"last_event_ms":     sync.LastEventTime.UnixMilli(),
		"last_connect_ms":   sync.LastConnectTime.UnixMilli(),
		"updated_at_ms":     sync.UpdatedAt.UnixMilli(),
	}, nil
}

type chatsListParams struct {
	Limit      int  `json:"limit"`
	UnreadOnly bool `json:"unread_only"`
	Pinned     bool `json:"pinned"`
}

func (s *Server) handleChatsList(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[chatsListParams](params)
	if perr != nil {
		return nil, perr
	}
	convs, err := s.deps.Store.ListConversations(ctx, store.ListConversationOpts{
		Limit:      p.Limit,
		UnreadOnly: p.UnreadOnly,
		Pinned:     p.Pinned,
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return convs, nil
}

type chatsFindParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (s *Server) handleChatsFind(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[chatsFindParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.Query == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "query is required"}
	}
	convs, err := s.deps.Store.FindConversations(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	return convs, nil
}

type chatsShowParams struct {
	ConversationID string `json:"conversation_id"`
	Limit          int    `json:"limit"`
}

func (s *Server) handleChatsShow(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[chatsShowParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ConversationID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "conversation_id is required"}
	}
	conv, err := s.deps.Store.GetConversation(ctx, p.ConversationID)
	if err != nil {
		return nil, &Error{Code: CodeNotFound, Message: "conversation not found: " + p.ConversationID}
	}
	msgs, err := s.deps.Store.ListMessages(ctx, store.ListMessageOpts{
		ConversationID: p.ConversationID,
		Limit:          p.Limit,
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return map[string]any{"conversation": conv, "messages": s.deps.Store.EnrichMessages(ctx, msgs)}, nil
}

type messagesListParams struct {
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	SinceMS        int64  `json:"since_ms"`
	UntilMS        int64  `json:"until_ms"`
	Limit          int    `json:"limit"`
	Order          string `json:"order"`
}

func (s *Server) handleMessagesList(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[messagesListParams](params)
	if perr != nil {
		return nil, perr
	}
	opts := store.ListMessageOpts{
		ConversationID: p.ConversationID,
		SenderID:       p.SenderID,
		Limit:          p.Limit,
		Order:          p.Order,
	}
	if p.SinceMS > 0 {
		opts.Since = time.UnixMilli(p.SinceMS)
	}
	if p.UntilMS > 0 {
		opts.Until = time.UnixMilli(p.UntilMS)
	}
	msgs, err := s.deps.Store.ListMessages(ctx, opts)
	if err != nil {
		return nil, internalErr(err)
	}
	return s.deps.Store.EnrichMessages(ctx, msgs), nil
}

type messagesSearchParams struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id"`
	SinceMS        int64  `json:"since_ms"`
	UntilMS        int64  `json:"until_ms"`
	Limit          int    `json:"limit"`
}

func (s *Server) handleMessagesSearch(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[messagesSearchParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.Query == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "query is required"}
	}
	opts := store.SearchOpts{
		Query:          p.Query,
		ConversationID: p.ConversationID,
		Limit:          p.Limit,
	}
	if p.SinceMS > 0 {
		opts.Since = time.UnixMilli(p.SinceMS)
	}
	if p.UntilMS > 0 {
		opts.Until = time.UnixMilli(p.UntilMS)
	}
	hits, err := s.deps.Store.SearchMessagesRich(ctx, opts)
	if err != nil {
		return nil, internalErr(err)
	}
	return hits, nil
}

type messagesShowParams struct {
	MessageID string `json:"message_id"`
}

func (s *Server) handleMessagesShow(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[messagesShowParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.MessageID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "message_id is required"}
	}
	msg, err := s.deps.Store.GetMessage(ctx, p.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "message not found: " + p.MessageID}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return msg, nil
}

type messagesContextParams struct {
	MessageID string `json:"message_id"`
	Before    int    `json:"before"`
	After     int    `json:"after"`
}

func (s *Server) handleMessagesContext(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[messagesContextParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.MessageID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "message_id is required"}
	}
	if p.Before == 0 && p.After == 0 {
		p.Before, p.After = 5, 5
	}
	msgs, err := s.deps.Store.GetMessageContext(ctx, p.MessageID, p.Before, p.After)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "message not found: " + p.MessageID}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return s.deps.Store.EnrichMessages(ctx, msgs), nil
}

type contactsSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (s *Server) handleContactsSearch(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[contactsSearchParams](params)
	if perr != nil {
		return nil, perr
	}
	contacts, err := s.deps.Store.SearchContacts(ctx, p.Query, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	return contacts, nil
}

type contactsShowParams struct {
	ID string `json:"id"` // participant_id or phone number
}

func (s *Server) handleContactsShow(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[contactsShowParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "id is required"}
	}
	contact, err := s.deps.Store.GetContact(ctx, p.ID)
	if errors.Is(err, store.ErrNotFound) {
		contact, err = s.deps.Store.GetContactByNumber(ctx, p.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "contact not found: " + p.ID}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	return contact, nil
}

// handleSyncRefresh schedules a paginated folder scan. Message history is
// explicitly not fetched. Completion and coverage arrive in sync.status.
func (s *Server) handleSyncRefresh(ctx context.Context) (any, *Error) {
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	s.refreshMu.Lock()
	if time.Since(s.lastRefresh) < 30*time.Second {
		s.refreshMu.Unlock()
		return map[string]any{"started": false, "reason": "refresh scheduled recently; inspect completion event for coverage"}, nil
	}
	s.lastRefresh = time.Now()
	s.refreshMu.Unlock()

	go s.runRefresh()
	return map[string]any{"started": true}, nil
}

// handleAuthPair starts the QR pairing flow inside the daemon. The QR URL
// and the outcome are broadcast as pair.* events for clients to render. On
// success the daemon restarts itself so the fresh session loads — on-demand
// clients respawn it and reconnect automatically.
func (s *Server) handleAuthPair() (any, *Error) {
	if s.deps.Layout.Session == "" {
		return nil, &Error{Code: CodeUnavailable, Message: "pairing is not available on this daemon"}
	}
	if !s.pairing.CompareAndSwap(false, true) {
		return map[string]any{"started": false, "reason": "pairing already in progress"}, nil
	}
	// The old session is dead weight during pairing; stop it from fighting
	// over the relay.
	if s.deps.Client != nil {
		s.deps.Client.Disconnect()
	}
	go s.runPairing()
	return map[string]any{"started": true}, nil
}

func (s *Server) runPairing() {
	defer s.pairing.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), gm.PairTimeout+30*time.Second)
	defer cancel()

	res, err := gm.Pair(ctx, s.deps.Layout, s.deps.Logger, func(qrURL string) {
		s.Broadcast(EventPairQR, map[string]any{"url": qrURL})
	})
	if err != nil {
		s.deps.Logger.Warn().Err(err).Msg("in-daemon pairing failed")
		s.Broadcast(EventPairError, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Logger.Info().Str("phone_id", res.PhoneID).Msg("paired via TUI; restarting daemon to load new session")
	s.Broadcast(EventPairSuccess, map[string]any{"phone_id": res.PhoneID})
	// Give the success event a moment to reach subscribers, then restart so
	// the daemon comes back with the fresh session.
	time.Sleep(500 * time.Millisecond)
	s.requestShutdown()
}

type authPairGoogleParams struct {
	// CookieFile is the Chrome profile cookie database of the Google account
	// to pair as. Empty means the current pairing's own profile.
	CookieFile string `json:"cookie_file"`
}

// handleAuthPairGoogle starts the Google-account pairing flow (the QR flow
// no longer works on current Google Messages builds). The daemon broadcasts
// pair.emoji with the confirmation emoji to tap on the phone, then
// pair.success/pair.error; on success it restarts to load the new session.
func (s *Server) handleAuthPairGoogle(params json.RawMessage) (any, *Error) {
	p, perr := decode[authPairGoogleParams](params)
	if perr != nil {
		return nil, perr
	}
	if s.deps.Layout.Session == "" {
		return nil, &Error{Code: CodeUnavailable, Message: "pairing is not available on this daemon"}
	}
	cookieFile := p.CookieFile
	if cookieFile == "" && s.deps.Client != nil {
		cookieFile = s.deps.Client.CookieFile()
	}
	if cookieFile == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "cookie_file is required: this daemon has no Google-account pairing to take it from"}
	}
	if !filepath.IsAbs(cookieFile) {
		return nil, &Error{Code: CodeInvalidParams, Message: "cookie_file must be an absolute path: " + cookieFile}
	}
	if !s.pairing.CompareAndSwap(false, true) {
		return map[string]any{"started": false, "reason": "pairing already in progress"}, nil
	}
	if s.deps.Client != nil {
		s.deps.Client.Disconnect()
	}
	go s.runGooglePairing(cookieFile)
	return map[string]any{"started": true, "cookie_file": cookieFile}, nil
}

func (s *Server) runGooglePairing(cookieFile string) {
	defer s.pairing.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), gm.PairTimeout+30*time.Second)
	defer cancel()

	res, err := gm.PairGoogle(ctx, s.deps.Layout, s.deps.Logger, cookieFile, func(emoji string) {
		s.Broadcast(EventPairEmoji, map[string]any{"emoji": emoji})
	})
	if err != nil {
		s.deps.Logger.Warn().Err(err).Msg("in-daemon google pairing failed")
		s.Broadcast(EventPairError, map[string]any{"error": err.Error()})
		return
	}
	s.deps.Logger.Info().Str("phone_id", res.PhoneID).Msg("paired (google flow); restarting daemon")
	s.Broadcast(EventPairSuccess, map[string]any{"phone_id": res.PhoneID})
	time.Sleep(500 * time.Millisecond)
	s.requestShutdown()
}

type mediaDownloadParams struct {
	MessageID string `json:"message_id"`
}

func (s *Server) handleMediaDownload(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[mediaDownloadParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.MessageID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "message_id is required"}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	m, err := s.deps.Store.GetMessage(ctx, p.MessageID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "message not found: " + p.MessageID}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	if m.MediaID == nil || *m.MediaID == "" {
		return nil, &Error{Code: CodeNotFound, Message: "message has no attached media"}
	}
	if len(m.DecryptionKey) == 0 {
		return nil, &Error{Code: CodeUnavailable, Message: "media has no decryption key (stripped during sync?)"}
	}
	s.liveMu.Lock()
	bytes, err := s.deps.Client.DownloadMedia(*m.MediaID, m.DecryptionKey)
	s.liveMu.Unlock()
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "download: " + err.Error()}
	}
	ext := ""
	if m.MimeType != nil {
		if exts, err := mime.ExtensionsByType(*m.MimeType); err == nil && len(exts) > 0 {
			ext = exts[0]
		}
	}
	path := filepath.Join(s.deps.Layout.MediaDir, m.ID+ext)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return nil, internalErr(err)
	}
	mimeType := ""
	if m.MimeType != nil {
		mimeType = *m.MimeType
	}
	return map[string]any{"path": path, "bytes": len(bytes), "mime_type": mimeType}, nil
}

type aliasParams struct {
	TargetType string `json:"target_type"` // contact | conversation
	TargetID   string `json:"target_id"`
	Alias      string `json:"alias"`
}

func aliasTarget(t string) (store.AliasTarget, *Error) {
	switch t {
	case "contact":
		return store.AliasContact, nil
	case "conversation":
		return store.AliasConversation, nil
	default:
		return "", &Error{Code: CodeInvalidParams, Message: "target_type must be contact or conversation"}
	}
}

func (s *Server) handleAliasSet(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[aliasParams](params)
	if perr != nil {
		return nil, perr
	}
	target, terr := aliasTarget(p.TargetType)
	if terr != nil {
		return nil, terr
	}
	if p.TargetID == "" || p.Alias == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "target_id and alias are required"}
	}
	if err := s.deps.Store.SetAlias(ctx, target, p.TargetID, p.Alias); err != nil {
		return nil, internalErr(err)
	}
	return map[string]any{"set": true}, nil
}

func (s *Server) handleAliasRm(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[aliasParams](params)
	if perr != nil {
		return nil, perr
	}
	target, terr := aliasTarget(p.TargetType)
	if terr != nil {
		return nil, terr
	}
	if p.TargetID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "target_id is required"}
	}
	if err := s.deps.Store.RemoveAlias(ctx, target, p.TargetID); err != nil {
		return nil, internalErr(err)
	}
	return map[string]any{"removed": true}, nil
}

type sendReactParams struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
	Remove    bool   `json:"remove"`
	Switch    bool   `json:"switch"`
}

// handleSendReact adds/removes a reaction. Reactions are low-risk phone
// mutations initiated by a human keystroke in the TUI, so they respect the
// send mode but skip the approval queue (documented in rpc-protocol.md).
func (s *Server) handleSendReact(params json.RawMessage) (any, *Error) {
	p, perr := decode[sendReactParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.MessageID == "" || p.Emoji == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "message_id and emoji are required"}
	}
	if p.Remove && p.Switch {
		return nil, &Error{Code: CodeInvalidParams, Message: "remove and switch are mutually exclusive"}
	}
	if s.deps.SendMode == SendOff {
		return nil, &Error{Code: CodeSendsDisabled, Message: "daemon is read-only; restart with `gmcli --read-only=false serve`"}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	action := gm.ReactionAdd
	if p.Remove {
		action = gm.ReactionRemove
	} else if p.Switch {
		action = gm.ReactionSwitch
	}
	s.liveMu.Lock()
	err := s.deps.Client.SendReaction(p.MessageID, p.Emoji, action)
	s.liveMu.Unlock()
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error()}
	}
	return map[string]any{"reacted": true, "emoji": p.Emoji}, nil
}

// runRefresh uses the same paginated traversal as explicit CLI reads. The
// "started" reply is only scheduling; the completion event carries coverage.
func (s *Server) runRefresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	r, e := s.Scan(ctx, gmsync.ScanOptions{Folder: "all", PageSize: 25, MaxPages: 1000})
	state := "refreshed"
	if e != nil {
		state = "refresh_failed"
		s.deps.Logger.Warn().Err(e).Msg("Folder refresh incomplete")
	}
	s.Broadcast(EventSyncStatus, map[string]any{"state": state, "coverage": r.Coverage})
}

type historyBackfillParams struct {
	ConversationID string `json:"conversation_id"`
	Requests       int    `json:"requests"`
	Count          int64  `json:"count"`
}

func (s *Server) handleHistoryBackfill(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[historyBackfillParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ConversationID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "conversation_id is required"}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	// Backfills page through many requests; give them more room than one send.
	ctx, cancel := context.WithTimeout(ctx, 5*s.deps.LiveTimeout)
	defer cancel()
	if conv, err := s.deps.Client.ReadConversation(ctx, p.ConversationID); err == nil {
		s.deps.Pump.Handle(conv)
	} else if _, localErr := s.deps.Store.GetConversation(ctx, p.ConversationID); localErr != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "get conversation: " + err.Error()}
	}
	res, err := history.Backfill(ctx, s.deps.Store, s.deps.Client, s.deps.Pump, p.ConversationID, p.Requests, p.Count)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error()}
	}
	return res, nil
}

type historyLookupParams struct {
	Phone    string `json:"phone"`
	Requests int    `json:"requests"`
	Count    int64  `json:"count"`
}

func (s *Server) handleHistoryLookup(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[historyLookupParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.Phone == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "phone is required (E.164, e.g. +13855551234)"}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*s.deps.LiveTimeout)
	defer cancel()
	conv, err := history.LookupConversation(s.deps.Client, s.deps.Pump, p.Phone)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error()}
	}
	res, err := history.Backfill(ctx, s.deps.Store, s.deps.Client, s.deps.Pump, conv.GetConversationID(), p.Requests, p.Count)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error()}
	}
	return map[string]any{
		"conversation_id": conv.GetConversationID(),
		"name":            conv.GetName(),
		"backfill":        res,
	}, nil
}

type sendTextParams struct {
	ConversationID string `json:"conversation_id"`
	Body           string `json:"body"`
	ReplyToID      string `json:"reply_to_id"`
	RequestedBy    string `json:"requested_by"`
}

func (s *Server) handleSendText(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[sendTextParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ConversationID == "" || p.Body == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "conversation_id and body are required"}
	}
	if s.deps.SendMode == SendOff {
		return nil, &Error{
			Code:    CodeSendsDisabled,
			Message: "daemon is read-only; restart with `gmcli --read-only=false serve` to enable the approval queue",
		}
	}
	requestedBy := p.RequestedBy
	if requestedBy == "" {
		requestedBy = "rpc"
	}
	approval := store.Approval{
		ID:             uuid.NewString(),
		ConversationID: p.ConversationID,
		Body:           p.Body,
		RequestedBy:    requestedBy,
	}
	if p.ReplyToID != "" {
		r := p.ReplyToID
		approval.ReplyToID = &r
	}
	if err := s.deps.Store.CreateApproval(ctx, approval); err != nil {
		return nil, internalErr(err)
	}
	created, err := s.deps.Store.GetApproval(ctx, approval.ID)
	if err != nil {
		return nil, internalErr(err)
	}

	if s.deps.SendMode == SendApprove {
		s.Broadcast(EventApprovalRequested, created)
		return created, nil
	}

	// SendDirect: perform the send now; the approval row becomes an audit
	// record with a terminal status.
	resolved, rpcErr := s.performApprovedSend(ctx, created)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return resolved, nil
}

type approvalsListParams struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func (s *Server) handleApprovalsList(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[approvalsListParams](params)
	if perr != nil {
		return nil, perr
	}
	approvals, err := s.deps.Store.ListApprovals(ctx, p.Status, p.Limit)
	if err != nil {
		return nil, internalErr(err)
	}
	return approvals, nil
}

type approvalResolveParams struct {
	ApprovalID string `json:"approval_id"`
	Reason     string `json:"reason"`
}

func (s *Server) handleApprovalsApprove(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[approvalResolveParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ApprovalID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "approval_id is required"}
	}
	if s.deps.SendMode == SendOff {
		return nil, &Error{
			Code:    CodeSendsDisabled,
			Message: "daemon is read-only; restart with `gmcli --read-only=false serve` to enable sends",
		}
	}
	approval, err := s.deps.Store.GetApproval(ctx, p.ApprovalID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "approval not found: " + p.ApprovalID}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	if approval.Status != store.ApprovalPending {
		return nil, &Error{Code: CodeAlreadyResolved, Message: "approval is already " + approval.Status}
	}
	return s.performApprovedSend(ctx, approval)
}

// performApprovedSend sends one approved message and records the outcome.
// liveMu makes double-approve races lose cleanly: the second caller finds the
// row no longer pending.
func (s *Server) performApprovedSend(ctx context.Context, approval store.Approval) (any, *Error) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()

	current, err := s.deps.Store.GetApproval(ctx, approval.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	if current.Status != store.ApprovalPending {
		return nil, &Error{Code: CodeAlreadyResolved, Message: "approval is already " + current.Status}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}

	sendCtx, cancel := context.WithTimeout(ctx, s.deps.LiveTimeout)
	defer cancel()
	replyTo := ""
	if approval.ReplyToID != nil {
		replyTo = *approval.ReplyToID
	}
	res, sendErr := s.deps.Client.SendText(sendCtx, approval.ConversationID, approval.Body, replyTo)

	status := store.ApprovalSent
	var errMsg, messageID *string
	if sendErr != nil {
		status = store.ApprovalFailed
		m := sendErr.Error()
		errMsg = &m
	} else {
		messageID = &res.MessageID
	}
	if err := s.deps.Store.ResolveApproval(ctx, approval.ID, status, errMsg, messageID); err != nil {
		return nil, internalErr(err)
	}
	resolved, err := s.deps.Store.GetApproval(ctx, approval.ID)
	if err != nil {
		return nil, internalErr(err)
	}
	s.Broadcast(EventApprovalResolved, resolved)
	if sendErr != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "send failed: " + sendErr.Error(), Data: resolved}
	}
	return resolved, nil
}

func (s *Server) handleApprovalsDeny(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, perr := decode[approvalResolveParams](params)
	if perr != nil {
		return nil, perr
	}
	if p.ApprovalID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "approval_id is required"}
	}
	var reason *string
	if p.Reason != "" {
		reason = &p.Reason
	}
	err := s.deps.Store.ResolveApproval(ctx, p.ApprovalID, store.ApprovalDenied, reason, nil)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Code: CodeNotFound, Message: "approval not found: " + p.ApprovalID}
	}
	if errors.Is(err, store.ErrApprovalResolved) {
		return nil, &Error{Code: CodeAlreadyResolved, Message: "approval already resolved"}
	}
	if err != nil {
		return nil, internalErr(err)
	}
	resolved, err := s.deps.Store.GetApproval(ctx, p.ApprovalID)
	if err != nil {
		return nil, internalErr(err)
	}
	s.Broadcast(EventApprovalResolved, resolved)
	return resolved, nil
}
