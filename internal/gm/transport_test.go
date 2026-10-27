package gm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
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

type factLog struct {
	answers []relayAnswer
	sources []error
}

func (f *factLog) sourceUnreadable(err error)  { f.sources = append(f.sources, err) }
func (f *factLog) relayAnswered(a relayAnswer) { f.answers = append(f.answers, a) }

// Every relay answer reaches the verdict, with Google's reason kept on a
// rejection while libgm still reads the whole body.
func TestGoogleTransportRecordsRelayAnswers(t *testing.T) {
	reason := strings.Repeat("SESSION_COOKIE_INVALID ", 40)
	for _, status := range []int{200, 401, 403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, reason)
			}))
			defer server.Close()
			facts := &factLog{}
			client := &http.Client{Transport: googleTransport{base: localTransport(server), facts: facts, source: func(context.Context) (map[string]string, error) {
				return map[string]string{"SAPISID": "s", "SID": "a"}, nil
			}}}
			resp, err := client.Get("https://instantmessaging-pa.clients6.google.com/$rpc/AckMessages")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != reason {
				t.Fatal("libgm lost part of the response body")
			}
			if len(facts.answers) != 1 || facts.answers[0].Status != status || facts.answers[0].CookieFingerprint == "" {
				t.Fatalf("answers=%+v", facts.answers)
			}
			rejected := status == 401 || status == 403
			if got := facts.answers[0].Body; rejected != (got == reason[:rejectionBodyBytes]) {
				t.Fatalf("evidence body=%q", got)
			}
		})
	}
}

func TestGoogleTransportRecordsUnreadableSourceAndIgnoresConfigHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	facts := &factLog{}
	locked := errors.New("keyring locked")
	readable := true
	client := &http.Client{Transport: googleTransport{base: localTransport(server), facts: facts, source: func(context.Context) (map[string]string, error) {
		if !readable {
			return nil, locked
		}
		return map[string]string{"SAPISID": "s"}, nil
	}}}
	resp, err := client.Get("https://messages.google.com/web/config")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(facts.answers) != 0 {
		t.Fatal("config page answer counted as a relay verdict")
	}
	readable = false
	if _, err := client.Get("https://instantmessaging-pa.googleapis.com/x"); !errors.Is(err, locked) {
		t.Fatal(err)
	}
	if len(facts.sources) != 1 || !errors.Is(facts.sources[0], locked) {
		t.Fatalf("sources=%v", facts.sources)
	}
}

func TestCookieFingerprintFollowsRelayCredentialsOnly(t *testing.T) {
	a := map[string]string{"SID": "1", "HSID": "2", "SSID": "3", "APISID": "4", "SAPISID": "5", "OSID": "x"}
	b := map[string]string{"SID": "1", "HSID": "2", "SSID": "3", "APISID": "4", "SAPISID": "5", "OSID": "y"}
	c := map[string]string{"SID": "rotated", "HSID": "2", "SSID": "3", "APISID": "4", "SAPISID": "5"}
	if cookieFingerprint(a) != cookieFingerprint(b) || cookieFingerprint(a) == cookieFingerprint(c) {
		t.Fatal("fingerprint does not track exactly the relay cookies")
	}
	if fp := cookieFingerprint(a); len(fp) != 12 {
		t.Fatalf("fingerprint %q", fp)
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
