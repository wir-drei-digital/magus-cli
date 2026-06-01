// Package brain holds page-body helpers shared by the CLI and the MCP server.
package brain

import (
	"fmt"
	"strings"
)

// ApplyFindReplace substitutes find with replacement inside body.
//
// It is unique-match guarded to avoid silent partial edits: zero matches is an
// error, and more than one match without all=true is an error. With all=true,
// every occurrence is replaced.
func ApplyFindReplace(body, find, replacement string, all bool) (string, error) {
	if find == "" {
		return "", fmt.Errorf("find text must not be empty")
	}
	n := strings.Count(body, find)
	switch {
	case n == 0:
		return "", fmt.Errorf("text not found in page body: %q", find)
	case n > 1 && !all:
		return "", fmt.Errorf("found %d occurrences of %q; pass --all to replace all, or use a more specific find string", n, find)
	}
	count := 1
	if all {
		count = -1
	}
	return strings.Replace(body, find, replacement, count), nil
}
