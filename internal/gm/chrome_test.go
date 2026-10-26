package gm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func chromeCookieServer(t *testing.T, names []string) string {
	t.Helper()
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"webSocketDebuggerUrl":"ws%s/browser"}`, server.URL[4:])
	})
	mux.HandleFunc("/browser", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		var request struct {
			ID     int
			Method string
		}
		if err = wsjson.Read(ctx, conn, &request); err != nil {
			t.Error(err)
			return
		}
		if request.Method != "Storage.getCookies" {
			t.Errorf("unexpected browser mutation %s", request.Method)
			return
		}
		cookies := []map[string]string{}
		for _, name := range names {
			domain := ".google.com"
			if name == "OSID" {
				domain = "messages.google.com"
			}
			cookies = append(cookies, map[string]string{"name": name, "value": "fresh", "domain": domain})
		}
		cookies = append(cookies, map[string]string{"name": "SID", "value": "unrelated", "domain": "other.example"})
		wsjson.Write(ctx, conn, map[string]any{"id": request.ID, "result": map[string]any{"cookies": cookies}})
	})
	return server.URL
}

func TestChromeCookiesReadsBrowserWithoutOpeningATab(t *testing.T) {
	endpoint := chromeCookieServer(t, []string{"SID", "HSID", "OSID", "SSID", "APISID", "SAPISID", "__Secure-1PSIDTS"})
	cookies, err := ChromeCookies(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 7 || cookies["SID"] != "fresh" {
		t.Fatal("wrong cookie selection")
	}
}

func TestRelayReadsDoNotRequirePairingOSID(t *testing.T) {
	endpoint := chromeCookieServer(t, requiredRelayGoogleCookies)
	cookies, err := ChromeCookies(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if cookies["OSID"] != "" || len(cookies) != 5 {
		t.Fatal("relay credentials must remain fresh and must not invent OSID")
	}
	if _, err = ParseGoogleCookieInput(`{"SID":"fresh","HSID":"fresh","SSID":"fresh","APISID":"fresh","SAPISID":"fresh"}`); err == nil {
		t.Fatal("pairing accepted input without OSID")
	}
}

func TestRelayRejectsMissingSignInCredentials(t *testing.T) {
	for _, missing := range requiredRelayGoogleCookies {
		t.Run(missing, func(t *testing.T) {
			var names []string
			for _, name := range requiredRelayGoogleCookies {
				if name != missing {
					names = append(names, name)
				}
			}
			if _, err := ChromeCookies(context.Background(), chromeCookieServer(t, names)); err == nil {
				t.Fatal("accepted missing credential", missing)
			}
		})
	}
}

func TestChromeCookiesRejectsSignedOutBrowser(t *testing.T) {
	if _, err := ChromeCookies(context.Background(), chromeCookieServer(t, nil)); err == nil {
		t.Fatal("signed-out Chrome accepted")
	}
}

func TestChromeCookiesRejectsUnavailableBrowser(t *testing.T) {
	server := httptest.NewServer(http.NewServeMux())
	server.Close()
	if _, err := ChromeCookies(context.Background(), server.URL); err == nil {
		t.Fatal("unavailable Chrome accepted")
	}
}
