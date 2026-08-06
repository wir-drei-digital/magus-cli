// internal/chat/url_test.go
package chat

import "testing"

func TestWSURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"https becomes wss", "https://magus.digital", "wss://magus.digital/cli/chat", false},
		{"trailing slash trimmed", "https://magus.digital/", "wss://magus.digital/cli/chat", false},
		{"http localhost allowed", "http://localhost:4000", "ws://localhost:4000/cli/chat", false},
		{"http 127.0.0.1 allowed", "http://127.0.0.1:4000", "ws://127.0.0.1:4000/cli/chat", false},
		{"http remote rejected", "http://magus.digital", "", true},
		{"unknown scheme rejected", "ftp://magus.digital", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := WSURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("WSURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("WSURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
