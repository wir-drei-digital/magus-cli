package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRandomStateLength(t *testing.T) {
	s, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 {
		t.Errorf("expected 32 hex chars, got %d", len(s))
	}
}

// newTestHandler builds a callback handler wired to a real listener so the
// listener-close side effect on error paths can be observed. The returned
// channels are buffered, matching how browserFlow uses them.
func newTestHandler(t *testing.T, state string) (http.HandlerFunc, chan string, chan error, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)
	return newCallbackHandler(state, tokenCh, errCh, ln), tokenCh, errCh, ln
}

func TestCallbackHandlerRejectsWrongMethod(t *testing.T) {
	h, _, _, _ := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:12345/?state=abc&token=t", nil)
	req.Host = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestCallbackHandlerRejectsWrongPath(t *testing.T) {
	h, _, _, _ := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/favicon.ico?state=abc&token=t", nil)
	req.Host = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestCallbackHandlerRejectsWrongHost(t *testing.T) {
	h, _, _, _ := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example.com/?state=abc&token=t", nil)
	req.Host = "attacker.example.com"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestCallbackHandlerRejectsStateMismatch(t *testing.T) {
	h, _, errCh, ln := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/?state=wrong&token=t", nil)
	req.Host = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "state mismatch") {
			t.Errorf("unexpected error message: %v", err)
		}
	default:
		t.Error("expected error on errCh")
	}
	// Listener must be closed; a fresh Accept should fail immediately.
	if _, err := ln.Accept(); err == nil {
		t.Error("expected listener to be closed after state mismatch")
	}
}

func TestCallbackHandlerRejectsMissingToken(t *testing.T) {
	h, _, errCh, ln := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/?state=abc", nil)
	req.Host = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	select {
	case err := <-errCh:
		if !strings.Contains(err.Error(), "missing token") {
			t.Errorf("unexpected error: %v", err)
		}
	default:
		t.Error("expected error on errCh")
	}
	if _, err := ln.Accept(); err == nil {
		t.Error("expected listener to be closed after missing token")
	}
}

func TestCallbackHandlerSuccessPath(t *testing.T) {
	h, tokenCh, _, _ := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/?state=abc&token=secret-token", nil)
	req.Host = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	select {
	case got := <-tokenCh:
		if got != "secret-token" {
			t.Errorf("got token %q, want %q", got, "secret-token")
		}
	default:
		t.Error("expected token on tokenCh")
	}
}

func TestCallbackHandlerSecondHitReturnsGone(t *testing.T) {
	h, tokenCh, _, _ := newTestHandler(t, "abc")
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:12345/?state=abc&token=secret-token", nil)
	req.Host = "127.0.0.1:12345"

	rec1 := httptest.NewRecorder()
	h(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first hit: expected 200, got %d", rec1.Code)
	}
	<-tokenCh // drain

	rec2 := httptest.NewRecorder()
	h(rec2, req)
	if rec2.Code != http.StatusGone {
		t.Errorf("second hit: expected 410 Gone, got %d", rec2.Code)
	}
}
