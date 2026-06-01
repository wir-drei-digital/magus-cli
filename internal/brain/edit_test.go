package brain

import "testing"

func TestApplyFindReplace(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		find        string
		replacement string
		all         bool
		want        string
		wantErr     bool
	}{
		{name: "single match", body: "hello world", find: "world", replacement: "there", want: "hello there"},
		{name: "no match errors", body: "hello", find: "xyz", replacement: "q", wantErr: true},
		{name: "multiple without all errors", body: "a a a", find: "a", replacement: "b", wantErr: true},
		{name: "multiple with all", body: "a a a", find: "a", replacement: "b", all: true, want: "b b b"},
		{name: "empty find errors", body: "x", find: "", replacement: "y", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyFindReplace(tt.body, tt.find, tt.replacement, tt.all)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
