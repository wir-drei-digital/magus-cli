package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func TestResolvePageAcceptsUUID(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	got, err := resolvePage(context.Background(), nil, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if got != uuid {
		t.Errorf("uuid: want %q, got %q", uuid, got)
	}
}

func TestResolvePagePathStyle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/brains/my-brain/pages/api-design" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"id": "resolved-uuid"},
		})
	}))
	defer server.Close()

	c := api.New(server.URL, "tok", "test")
	got, err := resolvePage(context.Background(), c, "my-brain/api-design")
	if err != nil {
		t.Fatal(err)
	}
	if got != "resolved-uuid" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePageActiveBrainStyle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/brains/active-brain/pages/notes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"id": "page-from-active"},
		})
	}))
	defer server.Close()

	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: server.URL, Token: "tok", ActiveBrain: "active-brain"},
		},
	})

	c := api.New(server.URL, "tok", "test")
	got, err := resolvePage(context.Background(), c, "notes")
	if err != nil {
		t.Fatal(err)
	}
	if got != "page-from-active" {
		t.Errorf("got %q", got)
	}
}

func TestResolvePageErrorsWhenNoActiveBrain(t *testing.T) {
	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: "https://example.test", Token: "tok"},
		},
	})

	_, err := resolvePage(context.Background(), nil, "lonely-slug")
	if err == nil {
		t.Fatal("expected error when no active brain configured")
	}
	if !strings.Contains(err.Error(), "active brain") {
		t.Errorf("expected error to mention active brain, got %v", err)
	}
}
