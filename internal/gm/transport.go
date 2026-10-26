package gm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/util/exhttp"
)

type cookieSource func(context.Context) (map[string]string, error)

// profileSource reads the pairing record's Chrome profile on every request.
func profileSource(cookieFile string, required []string) cookieSource {
	if cookieFile == "" {
		return nil
	}
	return func(ctx context.Context) (map[string]string, error) {
		cookies, err := ChromeProfileCookies(ctx, cookieFile, required)
		if err != nil {
			return nil, fmt.Errorf("read current Google credentials: %w", err)
		}
		return cookies, nil
	}
}

// relayFacts receives what the transport learns about the pairing: whether
// the credential source could be read, and how Google's relay answered.
type relayFacts interface {
	sourceUnreadable(err error)
	relayAnswered(answer relayAnswer)
}

// relayAnswer is one response from the Messages relay. Body is kept only for
// rejections, cut to rejectionBodyBytes.
type relayAnswer struct {
	Endpoint          string `json:"endpoint"`
	Status            int    `json:"status"`
	Body              string `json:"body,omitempty"`
	CookieFingerprint string `json:"cookie_fingerprint,omitempty"`
}

const rejectionBodyBytes = 512

type googleTransport struct {
	base   http.RoundTripper
	source cookieSource // nil for a QR pairing: no account credentials
	facts  relayFacts   // nil outside the daemon's client
}

func (t googleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Downloads can redirect outside Google. Credentials belong only on the
	// relay/config hosts, including when net/http follows a redirect.
	relay := false
	switch req.URL.Hostname() {
	case "instantmessaging-pa.googleapis.com", "instantmessaging-pa.clients6.google.com":
		relay = true
	case "messages.google.com":
	default:
		req = req.Clone(req.Context())
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		return t.base.RoundTrip(req)
	}
	fingerprint := ""
	if t.source != nil {
		cookies, err := t.source(req.Context())
		if err != nil {
			if t.facts != nil {
				t.facts.sourceUnreadable(err)
			}
			return nil, err
		}
		fingerprint = cookieFingerprint(cookies)
		req = req.Clone(req.Context())
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		auth := &libgm.AuthData{Cookies: cookies}
		auth.AddCookiesToRequest(req)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || !relay || t.facts == nil {
		return resp, err
	}
	answer := relayAnswer{Endpoint: req.URL.Path, Status: resp.StatusCode, CookieFingerprint: fingerprint}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Keep Google's reason as evidence and hand libgm the body unread.
		head, _ := io.ReadAll(io.LimitReader(resp.Body, rejectionBodyBytes))
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}
		answer.Body = strings.ToValidUTF8(string(head), "?")
	}
	t.facts.relayAnswered(answer)
	return resp, nil
}

// cookieFingerprint names a set of relay credentials without revealing them,
// so evidence can tell whether Google rejected the same cookies twice.
func cookieFingerprint(cookies map[string]string) string {
	names := append([]string(nil), relayGoogleCookies...)
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s=%s;", name, cookies[name])
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func httpSettings(source cookieSource, facts relayFacts) exhttp.ClientSettings {
	settings := exhttp.SensibleClientSettings
	settings.TransportOverride = func(s exhttp.ClientSettings) http.RoundTripper {
		return googleTransport{base: s.Configure(&http.Transport{}), source: source, facts: facts}
	}
	return settings
}
