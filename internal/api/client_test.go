package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAddsBearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test_token" {
			t.Errorf("expected Bearer test_token, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "x"}})
	}))
	defer server.Close()

	c := New(server.URL, "test_token")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Get("/brains", &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "x" {
		t.Errorf("decoded id %q", out.ID)
	}
}

func TestClientErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_token","message":"Invalid"}}`)
	}))
	defer server.Close()

	c := New(server.URL, "bad")
	var out any
	err := c.Get("/brains", &out)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if apiErr.Code != "invalid_token" || apiErr.Status != 401 {
		t.Errorf("got %+v", apiErr)
	}
}

func TestClientPostJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"title":"X"`) {
			t.Errorf("body missing title: %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "new"}})
	}))
	defer server.Close()

	c := New(server.URL, "tok")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Post("/brains", map[string]string{"title": "X"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "new" {
		t.Errorf("got id %q", out.ID)
	}
}
