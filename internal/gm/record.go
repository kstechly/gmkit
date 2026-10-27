package gm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// record is session.json, the one pairing record: libgm's AuthData (the
// phone pairing's keys and relay token) plus, for a Google-account pairing,
// the Chrome profile cookie database of the account it was paired with.
//
// Google's cookies are never stored here. Chrome rotates them, so a copy
// starts going stale the moment it is written; every request reads the
// current ones from cookieFile instead (transport.go). In memory a Google
// pairing carries an empty, non-nil cookie map: that is what puts libgm in
// Google-account mode, while the transport supplies the values.
type record struct {
	auth       *libgm.AuthData
	cookieFile string
}

func loadRecord(path string) (*record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no session at %s; run `gmcli auth google --cookie-file PATH` first", path)
		}
		return nil, fmt.Errorf("open session %s: %w", path, err)
	}
	var auth libgm.AuthData
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", path, err)
	}
	var source struct {
		CookieFile string `json:"cookie_file"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", path, err)
	}
	if auth.IsGoogleAccount() && source.CookieFile == "" {
		return nil, fmt.Errorf("session %s is a Google-account pairing with no cookie_file; re-pair with `gmcli auth google --cookie-file PATH`", path)
	}
	auth.Cookies = nil
	if source.CookieFile != "" {
		auth.Cookies = map[string]string{}
	}
	return &record{auth: &auth, cookieFile: source.CookieFile}, nil
}

func saveRecord(path string, r *record) error {
	r.auth.CookiesLock.RLock()
	data, err := json.Marshal(r.auth)
	r.auth.CookiesLock.RUnlock()
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	delete(fields, "cookies")
	if r.cookieFile != "" {
		fields["cookie_file"], _ = json.Marshal(r.cookieFile)
	}
	data, err = json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
