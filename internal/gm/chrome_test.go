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

func TestChromeCookiesReadsBrowserWithoutOpeningATab(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
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
		for _, name := range []string{"SID", "HSID", "OSID", "SSID", "APISID", "SAPISID", "__Secure-1PSIDTS"} {
			domain := ".google.com"
			if name == "OSID" {
				domain = "messages.google.com"
			}
			cookies = append(cookies, map[string]string{"name": name, "value": "fresh", "domain": domain})
		}
		cookies = append(cookies, map[string]string{"name": "SID", "value": "unrelated", "domain": "other.example"})
		wsjson.Write(ctx, conn, map[string]any{"id": request.ID, "result": map[string]any{"cookies": cookies}})
	})
	cookies, err := ChromeCookies(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 7 || cookies["SID"] != "fresh" {
		t.Fatal("wrong cookie selection")
	}
}

func TestChromeCookiesRejectsSignedOutBrowser(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"webSocketDebuggerUrl":"ws%s/browser"}`, server.URL[4:])
	})
	mux.HandleFunc("/browser", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		var req any
		wsjson.Read(r.Context(), conn, &req)
		wsjson.Write(r.Context(), conn, map[string]any{"id": 1, "result": map[string]any{"cookies": []any{}}})
	})
	if _, err := ChromeCookies(context.Background(), server.URL); err == nil {
		t.Fatal("signed-out Chrome accepted")
	}
}
