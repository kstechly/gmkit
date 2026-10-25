package gm

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/util/exhttp"
)

type cookieSource func(context.Context) (map[string]string, error)

type googleTransport struct {
	base     http.RoundTripper
	source   cookieSource
	rejected func(any)
}

func (t googleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Downloads can redirect outside Google. Credentials belong only on the
	// relay/config hosts, including when net/http follows a redirect.
	google := false
	switch req.URL.Hostname() {
	case "messages.google.com", "instantmessaging-pa.googleapis.com", "instantmessaging-pa.clients6.google.com":
		google = true
	}
	if !google {
		req = req.Clone(req.Context())
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		return t.base.RoundTrip(req)
	}
	if t.source != nil {
		cookies, err := t.source(req.Context())
		if err != nil {
			return nil, err
		}
		req = req.Clone(req.Context())
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		auth := &libgm.AuthData{Cookies: cookies}
		auth.AddCookiesToRequest(req)
	}
	resp, err := t.base.RoundTrip(req)
	// Ack failures used to stay hidden in libgm's logger. Every authenticated
	// endpoint shares this transport, so status now sees those failures too.
	if err == nil && (resp.StatusCode == 401 || resp.StatusCode == 403) && t.rejected != nil {
		t.rejected(&events.ListenFatalError{Error: events.HTTPError{Resp: resp}})
	}
	return resp, err
}

func httpSettings(rejected func(any)) exhttp.ClientSettings {
	settings := exhttp.SensibleClientSettings
	var source cookieSource
	if endpoint := os.Getenv("GMCLI_CHROME_CDP"); endpoint != "" {
		source = func(ctx context.Context) (map[string]string, error) {
			cookies, err := ChromeCookies(ctx, endpoint)
			if err != nil {
				return nil, fmt.Errorf("read current Google credentials: %w", err)
			}
			return cookies, nil
		}
	}
	settings.TransportOverride = func(s exhttp.ClientSettings) http.RoundTripper {
		return googleTransport{base: s.Configure(&http.Transport{}), source: source, rejected: rejected}
	}
	return settings
}
