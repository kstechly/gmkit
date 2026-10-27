package gm

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

//go:embed chrome_profile_cookies.py
var chromeProfileCookieReader string

// ChromeProfileCookies reads one explicit profile's current cookie database.
// Browser-level CDP follows the last-used profile, which may be another account.
// browser-cookie3 owns SQLite snapshots and OS keyring decryption; this wrapper
// never opens a tab, changes browser focus, or caches a credential snapshot.
// required names the cookies the caller's request needs (pairing or relay).
func ChromeProfileCookies(ctx context.Context, cookieFile string, required []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "uv", "run", "--offline", "--no-project", "--with", "browser-cookie3==0.20.1", "python", "-c", chromeProfileCookieReader, cookieFile)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Chrome profile cookie source unavailable: %w", err)
	}
	var cookies map[string]string
	if err := json.Unmarshal(output, &cookies); err != nil {
		return nil, fmt.Errorf("decode Chrome profile cookies: %w", err)
	}
	cookies = filterGoogleCookies(cookies)
	if err := validateGoogleCookies(cookies, required); err != nil {
		return nil, err
	}
	return cookies, nil
}
