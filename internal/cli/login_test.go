package cli

import "testing"

func TestRandomStateLength(t *testing.T) {
	s, err := randomState()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 {
		t.Errorf("expected 32 hex chars, got %d", len(s))
	}
}
