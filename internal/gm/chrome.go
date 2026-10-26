package gm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ChromeCookies reads Chrome's browser endpoint without opening or changing a tab.
// Each Google request gets a fresh snapshot: Chrome can invalidate its previous
// cookies at any time, so a timer or a cache cannot establish validity.
func ChromeCookies(ctx context.Context, endpoint string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// An owned Chrome profile uses --remote-debugging-port=0. Resolve its
	// current port each time, including after a browser restart.
	if filepath.IsAbs(endpoint) {
		active, err := os.ReadFile(filepath.Join(endpoint, "DevToolsActivePort"))
		if err != nil {
			return nil, fmt.Errorf("Chrome profile source unavailable: %w", err)
		}
		port, _, _ := strings.Cut(string(active), "\n")
		endpoint = "http://127.0.0.1:" + port
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/json/version", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Chrome cookie source unavailable: %w", err)
	}
	defer resp.Body.Close()
	var version struct {
		URL string `json:"webSocketDebuggerUrl"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return nil, err
	}
	conn, _, err := websocket.Dial(ctx, version.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("Chrome cookie source unavailable: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1) // Chrome's cookie store routinely exceeds websocket's default 32 KiB.
	if err = wsjson.Write(ctx, conn, map[string]any{"id": 1, "method": "Storage.getCookies"}); err != nil {
		return nil, err
	}
	var result struct {
		ID    int `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			Cookies []struct{ Name, Value, Domain string } `json:"cookies"`
		} `json:"result"`
	}
	for result.ID != 1 {
		if err = wsjson.Read(ctx, conn, &result); err != nil {
			return nil, err
		}
	}
	if result.Error != nil {
		return nil, fmt.Errorf("Chrome cookie source: %s", result.Error.Message)
	}
	cookies := map[string]string{}
	for _, c := range result.Result.Cookies {
		if c.Domain == ".google.com" || c.Domain == "google.com" || c.Domain == "messages.google.com" || c.Domain == ".messages.google.com" {
			cookies[c.Name] = c.Value
		}
	}
	return ParseGoogleCookieInput(mustCookieJSON(cookies))
}

func mustCookieJSON(cookies map[string]string) string {
	b, _ := json.Marshal(cookies) // map[string]string always marshals
	return string(b)
}
