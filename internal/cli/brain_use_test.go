package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/config"
)

// withTempConfig points MAGUS_CONFIG_DIR at a fresh temp dir and writes the
// provided config. It also resets the package-level cobra flags between tests
// so state from a prior run doesn't leak. Returns the dir.
func withTempConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MAGUS_CONFIG_DIR", dir)
	t.Setenv("MAGUS_API_TOKEN", "")
	t.Setenv("MAGUS_API_URL", "")

	// Reset package-level flag globals.
	prevProfile, prevAPIURL, prevJSON, prevQuiet := profile, apiURL, jsonMode, quietMode
	profile, apiURL, jsonMode, quietMode = "", "", false, false
	t.Cleanup(func() {
		profile, apiURL, jsonMode, quietMode = prevProfile, prevAPIURL, prevJSON, prevQuiet
	})

	if cfg != nil {
		if err := cfg.Save(); err != nil {
			t.Fatalf("save initial config: %v", err)
		}
	}
	return dir
}

func TestBrainUseStoresActiveBrain(t *testing.T) {
	dir := withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: "https://example.test", Token: "tok"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"brain", "use", "research"})
	if err := root.Execute(); err != nil {
		t.Fatalf("brain use: %v", err)
	}

	// Verify config file on disk.
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !containsString(string(data), `active_brain = 'research'`) && !containsString(string(data), `active_brain = "research"`) {
		t.Errorf("expected active_brain in config:\n%s", data)
	}

	// Round-trip via Load.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["work"].ActiveBrain; got != "research" {
		t.Errorf("active brain mismatch: got %q", got)
	}
}

func TestBrainCurrentEmptyExitsNonZero(t *testing.T) {
	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: "https://example.test", Token: "tok"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"brain", "current"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when no active brain set")
	}
}

func TestBrainUnsetClearsActiveBrain(t *testing.T) {
	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: "https://example.test", Token: "tok", ActiveBrain: "research"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"brain", "unset"})
	if err := root.Execute(); err != nil {
		t.Fatalf("brain unset: %v", err)
	}

	cfg, _ := config.Load()
	if cfg.Profiles["work"].ActiveBrain != "" {
		t.Errorf("expected empty active brain, got %q", cfg.Profiles["work"].ActiveBrain)
	}
}

func TestPageListUsesActiveBrainWhenFlagOmitted(t *testing.T) {
	// Mock API: records the request path so we can assert which brain was used.
	var hitPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)

	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: server.URL, Token: "tok", ActiveBrain: "from-active"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"page", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("page list: %v", err)
	}

	want := "/api/v2/brains/from-active/pages"
	if hitPath != want {
		t.Errorf("expected request to %s, got %s", want, hitPath)
	}
}

func TestSearchErrorsWhenNoBrainAndNoActive(t *testing.T) {
	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: "https://example.test", Token: "tok"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"search", "anything"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when no brain specified")
	}
}

func TestPageWriteAcceptsSinglePositionalWithActiveBrain(t *testing.T) {
	// Mock API: capture the brain in path + the parsed body.
	type writeReq struct {
		Title string `json:"title"`
	}
	var hitPath string
	var gotBody writeReq

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"data":{"id":"p1","title":"Notes/Today","slug":"notes-today"}}`)
	}))
	t.Cleanup(server.Close)

	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: server.URL, Token: "tok", ActiveBrain: "from-active"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"page", "write", "Notes/Today", "--file", writeTempMarkdown(t)})
	if err := root.Execute(); err != nil {
		t.Fatalf("page write: %v", err)
	}

	if hitPath != "/api/v2/brains/from-active/pages" {
		t.Errorf("expected brain in path, got %s", hitPath)
	}
	if gotBody.Title != "Notes/Today" {
		t.Errorf("expected title 'Notes/Today', got %q", gotBody.Title)
	}
}

func writeTempMarkdown(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("# hi\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
