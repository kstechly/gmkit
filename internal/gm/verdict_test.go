package gm

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/johnlindquist/gmkit/internal/paths"
)

func TestVerdictFollowsLatestRelayAnswer(t *testing.T) {
	now := time.Unix(1000, 0)
	f := newVerdictFacts(now)
	step := func(apply func(), want string) {
		t.Helper()
		apply()
		now = now.Add(time.Second)
		if v, _ := f.verdict(now); v.State != want {
			t.Fatalf("state=%s want %s (%s)", v.State, want, v.Reason)
		}
	}
	step(func() {}, VerdictConnecting)
	step(func() { f.relayAnswered(relayAnswer{Status: 503}) }, VerdictConnecting)
	step(func() { f.relayAnswered(relayAnswer{Status: 200}) }, VerdictLive)
	step(func() { f.relayAnswered(relayAnswer{Status: 401, CookieFingerprint: "a"}) }, VerdictRejected)
	step(func() { f.relayAnswered(relayAnswer{Status: 401, CookieFingerprint: "a"}) }, VerdictRejected)
	step(func() { f.relayAnswered(relayAnswer{Status: 200}) }, VerdictLive)
	step(func() { f.sourceUnreadable(errors.New("keyring locked")) }, VerdictSourceUnreadable)
	step(func() { f.relayAnswered(relayAnswer{Status: 200}) }, VerdictLive)
}

// Google refusing the account's rotated cookies too means the pairing itself
// is gone; refusing one set says nothing beyond that set.
func TestVerdictLoggedOutWhenRotatedCookiesAreRejectedToo(t *testing.T) {
	now := time.Unix(1000, 0)
	f := newVerdictFacts(now)
	f.relayAnswered(relayAnswer{Status: 403, CookieFingerprint: "a"})
	f.sourceUnreadable(errors.New("keyring locked"))
	f.relayAnswered(relayAnswer{Status: 401, CookieFingerprint: "b"})
	v, _ := f.verdict(now)
	if v.State != VerdictLoggedOut || v.Evidence == nil || v.Evidence.CookieFingerprint != "b" {
		t.Fatalf("%+v", v)
	}
	// A relay 200 (an ack) does not bring back a pairing the phone cannot
	// decrypt for; only the phone's own payload does.
	f.relayAnswered(relayAnswer{Status: 200})
	if v, _ := f.verdict(now); v.State != VerdictLoggedOut {
		t.Fatal(v.State)
	}
	f.phoneEvent(now)
	if v, _ := f.verdict(now); v.State != VerdictLive {
		t.Fatal(v.State)
	}

	g := newVerdictFacts(now)
	g.relayAnswered(relayAnswer{Status: 401, CookieFingerprint: "a"})
	g.relayAnswered(relayAnswer{Status: 200})
	g.relayAnswered(relayAnswer{Status: 401, CookieFingerprint: "b"})
	if v, _ := g.verdict(now); v.State != VerdictRejected {
		t.Fatalf("an accepted answer between rejections still counted: %s", v.State)
	}
}

func TestVerdictPhoneLogoutAndDecryption(t *testing.T) {
	now := time.Unix(1000, 0)
	f := newVerdictFacts(now)
	f.relayAnswered(relayAnswer{Status: 200})
	f.phoneEvent(now)
	f.decryptFailed(now.Add(time.Second))
	if v, _ := f.verdict(now); v.State != VerdictUndecryptable || v.DecryptFailures != 1 {
		t.Fatalf("%+v", v)
	}
	f.phoneEvent(now.Add(2 * time.Second))
	if v, _ := f.verdict(now); v.State != VerdictLive {
		t.Fatal(v.State)
	}
	f.phoneLoggedOut()
	if v, _ := f.verdict(now); v.State != VerdictLoggedOut || !v.Unusable() {
		t.Fatal(v.State)
	}
}

func TestVerdictSinceMarksTransitionsOnly(t *testing.T) {
	t0 := time.Unix(1000, 0)
	f := newVerdictFacts(t0)
	f.relayAnswered(relayAnswer{Status: 200})
	v, changed := f.verdict(t0.Add(time.Minute))
	if !changed || !v.Since.Equal(t0.Add(time.Minute)) {
		t.Fatalf("%v %v", changed, v.Since)
	}
	f.relayAnswered(relayAnswer{Status: 200})
	v, changed = f.verdict(t0.Add(time.Hour))
	if changed || !v.Since.Equal(t0.Add(time.Minute)) {
		t.Fatalf("%v %v", changed, v.Since)
	}
}

// libgm only logs a payload it cannot decrypt; the daemon's verdict must
// still see it, and subscribers hear each change once.
func TestClientVerdictSeesLibgmDecodeFailures(t *testing.T) {
	dir := t.TempDir()
	layout := paths.Layout{Session: filepath.Join(dir, "session.json")}
	auth := libgm.NewAuthData()
	auth.Browser = &gmproto.Device{SourceID: "browser"}
	if err := saveRecord(layout.Session, &record{auth: auth}); err != nil {
		t.Fatal(err)
	}
	c, err := Open(layout, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	var heard []string
	c.Subscribe(func(evt any) {
		if v, ok := evt.(*VerdictChanged); ok {
			heard = append(heard, v.State)
		}
	})
	c.relayAnswered(relayAnswer{Status: 200})
	c.relayAnswered(relayAnswer{Status: 200})
	c.Underlying().Logger.Err(errors.New("hmac mismatch")).Str("message_id", "m").Msg(libgmDecodeFailure)
	c.dispatch(&gmproto.Settings{})
	if got := c.Verdict(); got.State != VerdictLive || got.DecryptFailures != 1 {
		t.Fatalf("%+v", got)
	}
	if want := []string{VerdictLive, VerdictUndecryptable, VerdictLive}; !slices.Equal(heard, want) {
		t.Fatalf("heard %v want %v", heard, want)
	}
}
