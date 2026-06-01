package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSearchPostsKindAndCrossBrain(t *testing.T) {
	var rec []recorded
	srv := cannedServer(t, &rec, http.StatusOK,
		`{"data":[{"kind":"page","rank":0.7,"brain_id":"b1","page_id":"p1","title":"T","snippet":"s"}]}`)
	defer srv.Close()
	c := New(srv.URL, "tok", "")

	hits, err := c.Search(context.Background(), "b1", SearchInput{Query: "q", Kind: "semantic", Limit: 5, CrossBrain: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Title != "T" || hits[0].Rank != 0.7 {
		t.Fatalf("hits: %+v", hits)
	}
	if rec[0].method != http.MethodPost || rec[0].path != "/api/v2/brains/b1/search" {
		t.Fatalf("request: %+v", rec[0])
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(rec[0].body), &sent); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if sent["kind"] != "semantic" || sent["cross_brain"] != true {
		t.Errorf("sent: %+v", sent)
	}
	if _, hasMode := sent["mode"]; hasMode {
		t.Errorf("mode should not be sent: %+v", sent)
	}
}
