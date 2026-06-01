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

type recordedRequest struct {
	method string
	path   string
	body   string
}

// canned builds an httptest.Server that records every request and responds
// with status + body. Pass a slice of bodies to answer successive requests
// (the last one repeats); a single body answers every request.
func canned(t *testing.T, recorded *[]recordedRequest, status int, bodies ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		_, _ = io.Copy(&b, r.Body)
		*recorded = append(*recorded, recordedRequest{method: r.Method, path: r.URL.RequestURI(), body: b.String()})
		idx := len(*recorded) - 1
		if idx >= len(bodies) {
			idx = len(bodies) - 1
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, bodies[idx])
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
	if _, err := brainListCore(context.Background(), newTestClient(srv)); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodGet || got[0].path != "/api/v2/brains" {
		t.Errorf("unexpected request: %+v", got[0])
	}
}

func TestBrainCreateCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"b1","title":"Hello"}}`)
	defer srv.Close()
	if _, err := brainCreateCore(context.Background(), newTestClient(srv), map[string]string{"title": "Hello"}); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains" {
		t.Errorf("request: %+v", got[0])
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["title"] != "Hello" {
		t.Errorf("title: %v", sent["title"])
	}
}

func TestPageListCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[{"id":"p1","title":"Top"}]}`)
	defer srv.Close()
	if _, err := pageListCore(context.Background(), newTestClient(srv), "brain-slug", true); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/brain-slug/pages?as=flat" {
		t.Errorf("path: %s", got[0].path)
	}
}

func TestPageReadCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":{"id":"p1","title":"Top","body":"# hello"}}`)
	defer srv.Close()
	if _, err := pageReadCore(context.Background(), newTestClient(srv), "p1"); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodGet || got[0].path != "/api/v2/pages/p1" {
		t.Errorf("request: %+v", got[0])
	}
}

func TestPageCreateCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"p2","title":"New","body":"hi"}}`)
	defer srv.Close()
	in := api.CreatePageInput{Title: "New", Body: "hi"}
	if _, err := pageCreateCore(context.Background(), newTestClient(srv), "brain-slug", in); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains/brain-slug/pages" {
		t.Errorf("request: %+v", got[0])
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["title"] != "New" || sent["body"] != "hi" {
		t.Errorf("body: %+v", sent)
	}
}

func TestPageEditCoreReadsThenReplaces(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK,
		`{"data":{"id":"p1","title":"T","body":"hello world"}}`,
		`{"data":{"id":"p1","title":"T","body":"hello there"}}`)
	defer srv.Close()
	if _, err := pageEditCore(context.Background(), newTestClient(srv), "p1", "world", "there", false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected GET then PATCH, got %d requests", len(got))
	}
	if got[0].method != http.MethodGet {
		t.Errorf("first request method: %s", got[0].method)
	}
	if got[1].method != http.MethodPatch {
		t.Errorf("second request method: %s", got[1].method)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[1].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["body"] != "hello there" || sent["mode"] != "replace" {
		t.Errorf("patch body: %+v", sent)
	}
}

func TestPageDeleteCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":{"id":"p1","deleted_at":"2026-05-17T12:00:00Z"}}`)
	defer srv.Close()
	if _, err := pageDeleteCore(context.Background(), newTestClient(srv), "p1"); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodDelete || got[0].path != "/api/v2/pages/p1" {
		t.Errorf("request: %+v", got[0])
	}
}

func TestBrainSearchCoreFallsBackToActiveBrain(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	if _, err := brainSearchCore(context.Background(), newTestClient(srv), "fallback", "", "needle", "", 0); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/fallback/search" {
		t.Errorf("expected fallback brain in path: %s", got[0].path)
	}
}

func TestBrainSearchCoreErrorsWhenNoBrain(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	_, err := brainSearchCore(context.Background(), newTestClient(srv), "", "", "needle", "", 0)
	if err == nil || !strings.Contains(err.Error(), "no brain specified") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBrainSearchCorePostsKind(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	if _, err := brainSearchCore(context.Background(), newTestClient(srv), "", "explicit", "needle", "semantic", 5); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains/explicit/search" {
		t.Errorf("request: %+v", got[0])
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["query"] != "needle" || sent["kind"] != "semantic" {
		t.Errorf("body: %+v", sent)
	}
	if _, hasMode := sent["mode"]; hasMode {
		t.Errorf("mode should not be sent: %+v", sent)
	}
}

func TestToolsRegistration(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	registered := tools(newTestClient(srv), "any-active-brain")
	want := []string{
		"brain_list", "brain_create",
		"page_list", "page_read",
		"page_create", "page_append", "page_prepend", "page_replace", "page_edit",
		"page_clear", "page_undo", "page_rename", "page_move", "page_delete",
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
