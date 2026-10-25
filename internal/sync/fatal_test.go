package sync

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
)

// A pairing Google refuses (4xx on the long poll or the token refresh) is the
// same dead end as the phone logging it out; anything else stays a plain
// fatal that a restart may clear.
func TestListenFatalClassifiesRejectedSessions(t *testing.T) {
	httpErr := func(code int) events.HTTPError {
		return events.HTTPError{Action: "polling", Resp: &http.Response{StatusCode: code}}
	}
	cases := []struct {
		name      string
		err       error
		loggedOut bool
	}{
		{"poll 401 SESSION_COOKIE_INVALID", httpErr(http.StatusUnauthorized), true},
		{"poll 403", httpErr(http.StatusForbidden), true},
		{"token refresh 404", fmt.Errorf("failed to refresh auth token: %w", events.RequestError{HTTP: ptr(httpErr(http.StatusNotFound))}), true},
		{"server 503", httpErr(http.StatusServiceUnavailable), false},
		{"no HTTP status", errors.New("failed to reconnect after pair success"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pump := New(nil, zerolog.Nop())
			pump.Handle(&events.ListenFatalError{Error: tc.err})
			select {
			case got := <-pump.Fatal():
				if errors.Is(got, ErrLoggedOut) != tc.loggedOut {
					t.Fatalf("errors.Is(%v, ErrLoggedOut) = %v, want %v", got, !tc.loggedOut, tc.loggedOut)
				}
			default:
				t.Fatal("pump reported no fatal error")
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
