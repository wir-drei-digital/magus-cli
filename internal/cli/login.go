package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

var (
	loginToken   string
	loginProfile string
)

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authorize this machine with a Magus account",
		Long: `Opens a browser to the Magus CLI authorize page and stores
the returned token in the active profile. Use --token to paste a PAT
directly (for CI or headless use).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			apiURL := config.ResolveAPIURL(cfg, profile, DefaultAPIURL)
			if v := apiURL; v == "" {
				apiURL = DefaultAPIURL
			}

			var token string
			if loginToken != "" {
				token = loginToken
			} else {
				token, err = browserFlow(apiURL)
				if err != nil {
					return err
				}
			}

			name := loginProfile
			if name == "" {
				name = "personal"
			}

			if cfg.Profiles == nil {
				cfg.Profiles = map[string]config.Profile{}
			}

			existing := cfg.Profiles[name]
			existing.APIURL = apiURL
			existing.Token = token
			cfg.Profiles[name] = existing
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}

			if err := cfg.Save(); err != nil {
				return err
			}

			fmt.Printf("Saved token to profile %q.\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&loginToken, "token", "", "paste a PAT instead of running the browser flow")
	cmd.Flags().StringVar(&loginProfile, "name", "", "profile name to save under (default: personal)")
	return cmd
}

func browserFlow(apiURL string) (string, error) {
	state, err := randomState()
	if err != nil {
		return "", err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("bind localhost: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	callbackURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	authURL := fmt.Sprintf("%s/cli/authorize?callback=%s&state=%s",
		apiURL,
		url.QueryEscape(callbackURL),
		url.QueryEscape(state))

	fmt.Printf("Opening %s in your browser...\n", authURL)
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically. Visit:\n  %s\n", authURL)
	}

	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{
		Handler: newCallbackHandler(state, tokenCh, errCh, listener),
	}

	go func() {
		_ = srv.Serve(listener)
	}()

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	select {
	case token := <-tokenCh:
		return token, nil
	case err := <-errCh:
		return "", err
	case <-time.After(5 * time.Minute):
		return "", errors.New("login timed out")
	}
}

// newCallbackHandler builds the http.HandlerFunc used for the localhost
// OAuth-style callback. It enforces:
//
//   - method must be GET (favicon HEAD/OPTIONS probes are rejected)
//   - path must be exactly "/" (devtools and prefetch hits to /favicon.ico,
//     /.well-known/..., etc. don't consume the state)
//   - Host header must begin with "127.0.0.1:" (defends against DNS
//     rebinding from a page the user has open in another tab)
//   - state in the query must match expectedState
//   - token must be non-empty
//
// Only the first valid hit succeeds (guarded by sync.Once); subsequent
// requests get 410 Gone. On state-mismatch or missing-token errors the
// listener is closed immediately so further requests cannot race.
func newCallbackHandler(expectedState string, tokenCh chan<- string, errCh chan<- error, listener net.Listener) http.HandlerFunc {
	var once sync.Once
	closeListener := func() {
		if listener != nil {
			_ = listener.Close()
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Host, "127.0.0.1:") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		gotState := r.URL.Query().Get("state")
		gotToken := r.URL.Query().Get("token")
		if gotState != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case errCh <- errors.New("state mismatch (possible CSRF)"):
			default:
			}
			closeListener()
			return
		}
		if gotToken == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			select {
			case errCh <- errors.New("missing token in callback"):
			default:
			}
			closeListener()
			return
		}

		handled := false
		once.Do(func() {
			handled = true
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintln(w, "<h1>Magus CLI authorized</h1><p>You can close this tab.</p>")
			tokenCh <- gotToken
		})
		if !handled {
			http.Error(w, "callback already consumed", http.StatusGone)
		}
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "linux":
		return exec.Command("xdg-open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return fmt.Errorf("unsupported OS for auto-open: %s", runtime.GOOS)
	}
}
