// Package skill exposes the embedded magus agent skill content as a string.
//
// The canonical skill source lives at plugins/magus/skills/magus/SKILL.md
// (the Claude Code plugin shipping path). A byte-for-byte copy is kept at
// internal/skill/SKILL.md because //go:embed requires the file to live inside
// the embedding package. The Makefile's sync-skill target keeps the two in
// sync, and TestSkillContentMatchesPluginCopy catches drift.
package skill

import _ "embed"

//go:embed SKILL.md
var Content string
