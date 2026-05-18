package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientAddsBearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test_token" {
			t.Errorf("expected Bearer test_token, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "x"}})
	}))
	defer server.Close()

	c := New(server.URL, "test_token", "")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Get(context.Background(), "/brains", &out); err != nil {
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

	c := New(server.URL, "bad", "")
	var out any
	err := c.Get(context.Background(), "/brains", &out)
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

	c := New(server.URL, "tok", "")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Post(context.Background(), "/brains", map[string]string{"title": "X"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "new" {
		t.Errorf("got id %q", out.ID)
	}
}

// TestClientContextCancellation verifies that an in-flight request honours
// context cancellation: a server that hangs forever must return promptly
// once the caller's context is cancelled.
func TestClientContextCancellation(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	c := New(server.URL, "tok", "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	var out any
	err := c.Get(ctx, "/never", &out)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast return after cancel, took %v", elapsed)
	}
}
