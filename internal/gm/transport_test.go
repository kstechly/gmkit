package gm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
)

func TestGoogleTransportFollowsCookieRotationOnEveryEndpoint(t *testing.T) {
	current := "first"
	var got []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("__Secure-1PSIDTS")
		got = append(got, cookie.Value)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "SAPISIDHASH ") {
			t.Error("missing Google request signature")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	client := &http.Client{Transport: googleTransport{base: localTransport(server), source: func(context.Context) (map[string]string, error) {
		return map[string]string{"__Secure-1PSIDTS": current, "SAPISID": "sapisid"}, nil
	}}}
	for _, path := range []string{"/ReceiveMessages", "/AckMessages", "/SendMessage"} {
		resp, err := client.Get("https://messages.google.com" + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		current = "rotated"
	}
	if !slices.Equal(got, []string{"first", "rotated", "rotated"}) {
		t.Fatal(got)
	}
}

func TestGoogleTransportDoesNotSendStaleCookiesWhenChromeIsUnavailable(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer server.Close()
	unavailable := errors.New("Chrome closed")
	client := &http.Client{Transport: googleTransport{base: localTransport(server), source: func(context.Context) (map[string]string, error) { return nil, unavailable }}}
	_, err := client.Get("https://messages.google.com")
	if !errors.Is(err, unavailable) || called {
		t.Fatalf("err=%v contacted Google=%v", err, called)
	}
}

func TestGoogleTransportReportsAckAuthenticationFailures(t *testing.T) {
	for _, status := range []int{200, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var rejected error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			client := &http.Client{Transport: googleTransport{base: localTransport(server), rejected: func(evt any) { rejected = evt.(*events.ListenFatalError).Error }}}
			resp, err := client.Get("https://instantmessaging-pa.clients6.google.com/AckMessages")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if (rejected != nil) != (status == 401 || status == 403) {
				t.Fatalf("status=%d rejected=%v", status, rejected)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func localTransport(server *httptest.Server) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		target, _ := url.Parse(server.URL)
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
}
func TestGoogleTransportDoesNotLeakCredentialsOnRedirect(t *testing.T) {
	called := false
	tpt := googleTransport{source: func(context.Context) (map[string]string, error) { called = true; return nil, nil }, base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("Google credentials leaked to media redirect")
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	req, _ := http.NewRequest("GET", "https://media.example/file", nil)
	req.Header.Set("Cookie", "SID=secret")
	req.Header.Set("Authorization", "secret")
	if _, err := tpt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("unrelated host read Chrome credentials")
	}
}
