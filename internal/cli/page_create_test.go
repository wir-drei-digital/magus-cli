package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/config"
)

func TestPageCreateSurfacesCollisionDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintln(w, `{"error":{"code":"already_exists","message":"a page with this title already exists","details":{"existing_page_id":"pg-123","existing_page_title":"Notes"}}}`)
	}))
	t.Cleanup(server.Close)

	withTempConfig(t, &config.Config{
		DefaultProfile: "work",
		Profiles: map[string]config.Profile{
			"work": {APIURL: server.URL, Token: "tok", ActiveBrain: "from-active"},
		},
	})

	root := newRootCmd()
	root.SetArgs([]string{"page", "create", "Notes", "--file", writeTempMarkdown(t)})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error on 409 already_exists")
	}
	if !containsString(err.Error(), "pg-123") {
		t.Errorf("expected error to include existing page id, got: %v", err)
	}
}
