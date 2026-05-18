package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/api"
)

// Each test stands up a tiny httptest server with canned responses for
// the endpoint under test, builds a real *api.Client pointed at it, and
// invokes the core function directly. The thin handler closures in
// tools() that just unwrap the MCP request are exercised implicitly via
// the core API surface, but their job is purely argument extraction;
// the interesting behaviour lives in the *Core functions.

type recordedRequest struct {
	method string
	path   string
	body   string
}

// canned builds an httptest.Server that records every incoming request
// into recorded and responds with status + body for the matching path.
// Use "*" as a catch-all path.
func canned(t *testing.T, recorded *[]recordedRequest, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		_, _ = io.Copy(&b, r.Body)
		*recorded = append(*recorded, recordedRequest{
			method: r.Method,
			path:   r.URL.RequestURI(),
			body:   b.String(),
		})
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body)
	})
	return httptest.NewServer(mux)
}

func newTestClient(server *httptest.Server) *api.Client {
	return api.New(server.URL, "test-token", "magus-cli/test")
}

func TestBrainListCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[{"id":"b1","slug":"work","title":"Work"}]}`)
	defer srv.Close()
	c := newTestClient(srv)

	res, err := brainListCore(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if len(got) != 1 || got[0].method != http.MethodGet || got[0].path != "/api/v2/brains" {
		t.Errorf("unexpected request: %+v", got)
	}
}

func TestBrainCreateCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"b1","title":"Hello"}}`)
	defer srv.Close()
	c := newTestClient(srv)

	args := map[string]string{
		"title":       "Hello",
		"description": "Greetings",
	}
	if _, err := brainCreateCore(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 request, got %d", len(got))
	}
	if got[0].method != http.MethodPost {
		t.Errorf("method: %s", got[0].method)
	}
	if got[0].path != "/api/v2/brains" {
		t.Errorf("path: %s", got[0].path)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v (body=%q)", err, got[0].body)
	}
	if sent["title"] != "Hello" {
		t.Errorf("title: %v", sent["title"])
	}
	if sent["description"] != "Greetings" {
		t.Errorf("description: %v", sent["description"])
	}
}

func TestPageListCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[{"id":"p1","title":"Top"}]}`)
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := pageListCore(context.Background(), c, "brain-slug", true); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/brain-slug/pages?as=flat" {
		t.Errorf("path: %s", got[0].path)
	}
}

func TestPageReadCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK,
		`{"data":{"id":"p1","title":"Top","markdown":"# hello"}}`)
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := pageReadCore(context.Background(), c, "p1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got[0].path, "/api/v2/pages/p1") {
		t.Errorf("path: %s", got[0].path)
	}
	if !strings.Contains(got[0].path, "format=markdown") {
		t.Errorf("missing format=markdown: %s", got[0].path)
	}
}

func TestPageWriteCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"p2","title":"New"}}`)
	defer srv.Close()
	c := newTestClient(srv)

	args := map[string]string{
		"title":   "New",
		"content": "body text",
		"mode":    "append",
	}
	if _, err := pageWriteCore(context.Background(), c, "brain-slug", args); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost {
		t.Errorf("method: %s", got[0].method)
	}
	if got[0].path != "/api/v2/brains/brain-slug/pages" {
		t.Errorf("path: %s", got[0].path)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["title"] != "New" || sent["content"] != "body text" || sent["mode"] != "append" {
		t.Errorf("body: %+v", sent)
	}
}

func TestPageUpdateCoreTitleOnly(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":{"id":"p1","title":"Renamed"}}`)
	defer srv.Close()
	c := newTestClient(srv)

	args := map[string]string{"title": "Renamed"}
	if _, err := pageUpdateCore(context.Background(), c, "p1", args); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPatch {
		t.Errorf("method: %s", got[0].method)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["title"] != "Renamed" {
		t.Errorf("title: %v", sent["title"])
	}
	if _, ok := sent["parent_page_id"]; ok {
		t.Errorf("parent_page_id should be omitted when empty: %+v", sent)
	}
}

func TestPageDeleteCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK,
		`{"data":{"id":"p1","deleted_at":"2026-05-17T12:00:00Z"}}`)
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := pageDeleteCore(context.Background(), c, "p1"); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodDelete {
		t.Errorf("method: %s", got[0].method)
	}
	if got[0].path != "/api/v2/pages/p1" {
		t.Errorf("path: %s", got[0].path)
	}
}

func TestBrainSearchCoreFallsBackToActiveBrain(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	c := newTestClient(srv)

	// brainArg "" -> falls back to activeBrain "fallback".
	if _, err := brainSearchCore(context.Background(), c, "fallback", "", "needle", "", 0); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/fallback/search" {
		t.Errorf("expected fallback brain in path: %s", got[0].path)
	}
}

func TestBrainSearchCoreErrorsWhenNoBrain(t *testing.T) {
	srv := canned(t, &[]recordedRequest{}, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	c := newTestClient(srv)

	_, err := brainSearchCore(context.Background(), c, "", "", "needle", "", 0)
	if err == nil {
		t.Fatal("expected error when no brain is specified")
	}
	if !strings.Contains(err.Error(), "no brain specified") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBrainSearchCorePostsSearchInput(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	c := newTestClient(srv)

	if _, err := brainSearchCore(context.Background(), c, "", "explicit", "needle", "semantic", 5); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost {
		t.Errorf("method: %s", got[0].method)
	}
	if got[0].path != "/api/v2/brains/explicit/search" {
		t.Errorf("path: %s", got[0].path)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["query"] != "needle" || sent["mode"] != "semantic" {
		t.Errorf("body: %+v", sent)
	}
	if got, want := sent["limit"], float64(5); got != want {
		t.Errorf("limit: got %v want %v", got, want)
	}
}

// Sanity: tools() returns the full registered tool slice with stable
// names so MCP clients enumerate them correctly.
func TestToolsRegistration(t *testing.T) {
	srv := canned(t, &[]recordedRequest{}, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	c := newTestClient(srv)

	registered := tools(c, "any-active-brain")
	want := []string{
		"brain_list", "brain_create",
		"page_list", "page_read", "page_write", "page_update", "page_delete",
		"brain_search",
	}
	if len(registered) != len(want) {
		t.Fatalf("expected %d tools, got %d", len(want), len(registered))
	}
	names := make(map[string]bool, len(registered))
	for _, r := range registered {
		names[r.def.Name] = true
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing tool %q", n)
		}
	}
}
