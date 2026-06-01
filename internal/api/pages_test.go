package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recorded struct {
	method string
	path   string
	body   string
}

func cannedServer(t *testing.T, rec *[]recorded, status int, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*rec = append(*rec, recorded{method: r.Method, path: r.URL.RequestURI(), body: string(b)})
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, respBody)
	}))
}

func TestCreatePage(t *testing.T) {
	var rec []recorded
	srv := cannedServer(t, &rec, http.StatusCreated, `{"data":{"id":"p1","title":"T","slug":"t","body":"hi"}}`)
	defer srv.Close()
	c := New(srv.URL, "tok", "")

	page, err := c.CreatePage(context.Background(), "brain-1", CreatePageInput{Title: "T", Body: "hi", ParentPageID: "par"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Body != "hi" {
		t.Errorf("body: %q", page.Body)
	}
	if rec[0].method != http.MethodPost || rec[0].path != "/api/v2/brains/brain-1/pages" {
		t.Fatalf("request: %+v", rec[0])
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["title"] != "T" || sent["body"] != "hi" || sent["parent_page_id"] != "par" {
		t.Errorf("sent: %+v", sent)
	}
}

func TestUpdatePageBodyModes(t *testing.T) {
	for _, mode := range []string{"append", "prepend", "replace"} {
		var rec []recorded
		srv := cannedServer(t, &rec, http.StatusOK, `{"data":{"id":"p1","title":"T","body":"x"}}`)
		defer srv.Close()
		c := New(srv.URL, "tok", "")
		if _, err := c.UpdatePageBody(context.Background(), "p1", "x", mode); err != nil {
			t.Fatal(err)
		}
		if rec[0].method != http.MethodPatch || rec[0].path != "/api/v2/pages/p1" {
			t.Fatalf("request: %+v", rec[0])
		}
		var sent map[string]any
		if err := json.Unmarshal([]byte(rec[0].body), &sent); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if sent["body"] != "x" || sent["mode"] != mode {
			t.Errorf("mode %s sent: %+v", mode, sent)
		}
	}
}

func TestClearPage(t *testing.T) {
	var rec []recorded
	srv := cannedServer(t, &rec, http.StatusOK, `{"data":{"id":"p1","title":"T","body":""}}`)
	defer srv.Close()
	c := New(srv.URL, "tok", "")
	if _, err := c.ClearPage(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if rec[0].method != http.MethodPost || rec[0].path != "/api/v2/pages/p1/clear" {
		t.Fatalf("request: %+v", rec[0])
	}
}

func TestUndoPage(t *testing.T) {
	var rec []recorded
	srv := cannedServer(t, &rec, http.StatusOK, `{"data":{"id":"p1","title":"T","body":"old"}}`)
	defer srv.Close()
	c := New(srv.URL, "tok", "")
	if _, err := c.UndoPage(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if rec[0].method != http.MethodPost || rec[0].path != "/api/v2/pages/p1/undo" {
		t.Fatalf("request: %+v", rec[0])
	}
}
