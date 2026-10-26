package gm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
)

// A Google pairing's record names its profile and never holds a cookie, so
// nothing can later send a frozen copy of the account's credentials.
func TestRecordKeepsProfileNotCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	auth := libgm.NewAuthData()
	auth.DestRegID = uuid.New()
	auth.SetCookies(map[string]string{"SID": "secret-from-a-response"})
	if err := saveRecord(path, &record{auth: auth, cookieFile: "/profile/Cookies"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "secret-from-a-response") || strings.Contains(string(raw), `"cookies"`) {
		t.Fatalf("record stored cookies: %s", raw)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	rec, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.cookieFile != "/profile/Cookies" || rec.auth.Cookies == nil || len(rec.auth.Cookies) != 0 || !rec.auth.HasCookies() {
		t.Fatalf("loaded %q cookies=%v", rec.cookieFile, rec.auth.Cookies)
	}
}

func TestRecordRefusesGoogleSessionWithoutProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	auth := libgm.NewAuthData()
	auth.DestRegID = uuid.New()
	if err := saveRecord(path, &record{auth: auth}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecord(path); err == nil || !strings.Contains(err.Error(), "cookie_file") {
		t.Fatalf("err=%v", err)
	}
}

func TestQRRecordStaysOutOfGoogleAccountMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := saveRecord(path, &record{auth: libgm.NewAuthData()}); err != nil {
		t.Fatal(err)
	}
	rec, err := loadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.cookieFile != "" || rec.auth.Cookies != nil {
		t.Fatal("QR pairing gained account credentials")
	}
}
