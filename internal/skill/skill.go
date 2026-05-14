// Package skill exposes the embedded magus agent skill content as a string.
//
// The canonical skill source lives at internal/skill/magus.md and is embedded
// into the binary at build time via //go:embed.
package skill

import _ "embed"

//go:embed magus.md
var Content string
