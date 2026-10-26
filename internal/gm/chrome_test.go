package gm

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func profileFixture(t *testing.T, file, value string, names []string) *sql.DB {
	t.Helper()
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("profile reader integration needs uv")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE cookies(host_key TEXT,path TEXT,is_secure INTEGER,expires_utc INTEGER,name TEXT,value TEXT,encrypted_value BLOB,is_httponly INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := db.Exec(`INSERT INTO cookies VALUES('.google.com','/',1,0,?,?,X'',1)`, name, value); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestChromeProfileRemainsPinnedAndReadsCurrentWAL(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Default", "Cookies")
	db := profileFixture(t, file, "paired", requiredRelayGoogleCookies)
	profileFixture(t, filepath.Join(root, "Profile 1", "Cookies"), "other account", requiredRelayGoogleCookies)
	for _, lastUsed := range []string{"Default", "Profile 1"} {
		if err := os.WriteFile(filepath.Join(root, "Local State"), []byte(`{"profile":{"last_used":"`+lastUsed+`"}}`), 0600); err != nil {
			t.Fatal(err)
		}
		cookies, err := ChromeProfileCookies(context.Background(), file)
		if err != nil {
			t.Fatal(err)
		}
		if cookies["SID"] != "paired" {
			t.Fatal("credentials changed with the last-used profile")
		}
	}
	if _, err := db.Exec(`UPDATE cookies SET value='rotated' WHERE name='SID'`); err != nil {
		t.Fatal(err)
	}
	cookies, err := ChromeProfileCookies(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	if cookies["SID"] != "rotated" {
		t.Fatal("reader reused stale cookies instead of current WAL contents")
	}
	if cookies["OSID"] != "" {
		t.Fatal("reader invented the optional pairing cookie")
	}
}

func TestChromeProfileRejectsUnavailableOrSignedOutStore(t *testing.T) {
	if _, err := ChromeProfileCookies(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing profile accepted")
	}
	for _, names := range [][]string{nil, {"SID"}} {
		file := filepath.Join(t.TempDir(), "Cookies")
		profileFixture(t, file, "fixture", names)
		if _, err := ChromeProfileCookies(context.Background(), file); err == nil {
			t.Fatal("incomplete account credentials accepted")
		}
	}
	if _, err := ParseGoogleCookieInput(`{"SID":"s","HSID":"s","SSID":"s","APISID":"s","SAPISID":"s"}`); err == nil {
		t.Fatal("pairing accepted input without OSID")
	}
}
