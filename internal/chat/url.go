// internal/chat/url.go
package chat

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// WSURL derives the chat WebSocket URL from the API base URL (e.g.
// "https://magus.digital" -> "wss://magus.digital/cli/chat"). Plaintext ws://
// is allowed only for localhost; any other plaintext or unknown scheme errors.
func WSURL(apiBaseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(apiBaseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("parse api url: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		if !isLocalhost(u.Host) {
			return "", fmt.Errorf("refusing plaintext ws:// to non-localhost host %q (use https)", u.Host)
		}
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q in api url", u.Scheme)
	}
	u.Path = "/cli/chat"
	u.RawQuery = ""
	return u.String(), nil
}

func isLocalhost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
