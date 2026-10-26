package gm

import (
	"net/http"
	"time"
)

// Verdict states. Exactly one holds at a time; each is recomputed from what
// Google's relay and the phone last said, never set by a caller.
const (
	// VerdictConnecting: nothing has answered yet since the daemon started.
	VerdictConnecting = "connecting"
	// VerdictLive: the relay accepts the pairing and phone payloads decrypt.
	VerdictLive = "live"
	// VerdictRejected: the relay last answered 401/403. Fresh cookies from
	// the profile may still clear it; the daemon keeps probing.
	VerdictRejected = "rejected"
	// VerdictSourceUnreadable: the pairing's Chrome profile could not be read
	// (keyring locked, profile gone, signed out), so nothing was sent.
	VerdictSourceUnreadable = "source_unreadable"
	// VerdictLoggedOut: the pairing is gone and only a re-pair restores it.
	// The phone said so, or the relay rejected two different cookie sets in a
	// row (rotating the account's credentials did not help).
	VerdictLoggedOut = "logged_out"
	// VerdictUndecryptable: the relay accepts the pairing, but the phone's
	// payloads stopped decrypting (another client replaced its keys).
	VerdictUndecryptable = "undecryptable"
)

// Verdict is the daemon's answer to "does the pairing work right now", with
// the evidence it rests on.
type Verdict struct {
	State              string       `json:"state"`
	Since              time.Time    `json:"since"`
	Reason             string       `json:"reason,omitempty"`
	Evidence           *relayAnswer `json:"evidence,omitempty"`
	DecryptFailures    int64        `json:"decrypt_failures"`
	LastDecryptFailure time.Time    `json:"last_decrypt_failure,omitzero"`
	LastPhoneEvent     time.Time    `json:"last_phone_event,omitzero"`
}

// Unusable reports whether phone operations cannot succeed under v.
func (v Verdict) Unusable() bool {
	switch v.State {
	case VerdictRejected, VerdictSourceUnreadable, VerdictLoggedOut:
		return true
	}
	return false
}

// verdictFacts holds the recorded facts and derives the verdict from them.
// Not safe for concurrent use; Client guards it.
type verdictFacts struct {
	base          string // connecting, live, rejected or source_unreadable
	reason        string
	evidence      *relayAnswer
	lastRejection *relayAnswer // since the relay last accepted the pairing
	loggedOut     bool
	loggedOutWhy  string

	decryptFailures    int64
	lastDecryptFailure time.Time
	lastPhoneEvent     time.Time

	published Verdict
}

func newVerdictFacts(now time.Time) *verdictFacts {
	f := &verdictFacts{base: VerdictConnecting}
	f.published = Verdict{State: VerdictConnecting, Since: now}
	return f
}

func (f *verdictFacts) relayAnswered(a relayAnswer) {
	switch {
	case a.Status >= 200 && a.Status < 300:
		f.base, f.reason, f.evidence, f.lastRejection = VerdictLive, "", nil, nil
	case a.Status == http.StatusUnauthorized || a.Status == http.StatusForbidden:
		prev := f.lastRejection
		f.base, f.reason, f.evidence, f.lastRejection = VerdictRejected, "Google's relay rejected the pairing", &a, &a
		if prev != nil && prev.CookieFingerprint != "" && a.CookieFingerprint != "" && prev.CookieFingerprint != a.CookieFingerprint {
			f.loggedOut, f.loggedOutWhy = true, "Google's relay rejected two different sets of the account's cookies in a row"
		}
	}
	// Other statuses (429, 5xx) say nothing about the pairing itself.
}

func (f *verdictFacts) sourceUnreadable(err error) {
	f.base, f.reason, f.evidence = VerdictSourceUnreadable, err.Error(), nil
}

func (f *verdictFacts) phoneLoggedOut() {
	f.loggedOut, f.loggedOutWhy = true, "the phone reports this pairing logged out"
}

func (f *verdictFacts) phoneEvent(now time.Time) {
	f.lastPhoneEvent = now
	f.loggedOut, f.loggedOutWhy = false, ""
}

func (f *verdictFacts) decryptFailed(now time.Time) {
	f.decryptFailures++
	f.lastDecryptFailure = now
}

// verdict derives the current verdict; changed is true when its state or
// reason differs from the last one derived.
func (f *verdictFacts) verdict(now time.Time) (v Verdict, changed bool) {
	v = Verdict{
		State:              f.base,
		Reason:             f.reason,
		Evidence:           f.evidence,
		DecryptFailures:    f.decryptFailures,
		LastDecryptFailure: f.lastDecryptFailure,
		LastPhoneEvent:     f.lastPhoneEvent,
	}
	switch {
	case f.loggedOut:
		v.State, v.Reason = VerdictLoggedOut, f.loggedOutWhy
	case f.base == VerdictLive && f.lastDecryptFailure.After(f.lastPhoneEvent):
		v.State, v.Reason = VerdictUndecryptable, "phone payloads stopped decrypting"
	}
	prev := f.published
	changed = v.State != prev.State || v.Reason != prev.Reason
	v.Since = prev.Since
	if changed {
		v.Since = now
	}
	f.published = v
	return v, changed
}
