package output

import (
	"encoding/json"
	"fmt"
	"os"
)

// JSON prints `v` as pretty JSON to stdout.
func JSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Println is a stdout wrapper that obeys --quiet.
func Println(quiet bool, args ...any) {
	if quiet {
		return
	}
	fmt.Println(args...)
}
