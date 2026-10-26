package gm

import (
	"fmt"
	"strings"
)

// pairingGoogleCookies are what Google-account pairing needs: OSID identifies
// the Messages web login while the phone confirms the emoji.
var pairingGoogleCookies = []string{"SID", "HSID", "OSID", "SSID", "APISID", "SAPISID"}

// relayGoogleCookies are what an already paired relay connection needs. It
// works without OSID, including while Chrome rotates that host-scoped cookie.
var relayGoogleCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID"}
var optionalGoogleCookies = []string{"__Secure-1PSIDTS"}

func filterGoogleCookies(cookies map[string]string) map[string]string {
	filtered := make(map[string]string)
	for _, name := range append(append([]string(nil), pairingGoogleCookies...), optionalGoogleCookies...) {
		if value := strings.TrimSpace(cookies[name]); value != "" {
			filtered[name] = value
		}
	}
	return filtered
}

func validateGoogleCookies(cookies map[string]string, required []string) error {
	var missing []string
	for _, name := range required {
		if cookies[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Chrome profile is missing Google cookies %s; sign that profile in to messages.google.com", strings.Join(missing, ", "))
	}
	return nil
}
