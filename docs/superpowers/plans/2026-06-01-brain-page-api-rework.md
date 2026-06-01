# Brain Page API Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-align the magus-cli brain page + search surface with the server's markdown-as-storage API: explicit page verbs, surgical edit, blocks/connections removed, MCP + docs updated.

**Architecture:** Expand-then-contract refactor so every commit compiles and its tests pass. First delete the dead blocks/connections concepts, then add the new body-model client functions additively, migrate the CLI and MCP consumers, and finally remove the now-unused old functions. Shared find/replace logic lives in a neutral `internal/brain` package consumed by both `cli` and `mcp`.

**Tech Stack:** Go 1.26, cobra, mark3labs/mcp-go, net/http + httptest for tests.

**Spec:** `docs/superpowers/specs/2026-06-01-brain-page-api-rework-design.md`

**Conventions:**
- Conventional commit subjects (`feat(cli):`, `refactor(api):`, `docs(skill):`).
- Every commit message ends with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- Work happens on branch `brain-markdown-api-rework` (already created).
- `<ref>` = page id | page-slug (active brain) | `brain/page-slug`, resolved by the existing `resolvePage`.

> **WORKING DIRECTORY — read this.** The repo is `/Users/daniel/Development/magus-cli`. The shell's CWD resets to a *different* directory between tool calls, so **every command block below begins with `cd /Users/daniel/Development/magus-cli`**. Do NOT strip those `cd`s, and verify `git rev-parse --show-toplevel` ends in `magus-cli` before any commit. Committing from the wrong directory lands changes in the wrong repo.

---

## Server API contract (quick reference)

| Operation | Method + path | Body |
|---|---|---|
| Create page | `POST /api/v2/brains/:b/pages` | `{title, body?, parent_page_id?}` |
| Edit body | `PATCH /api/v2/pages/:id` | `{body, mode}` (mode: replace\|append\|prepend) |
| Rename | `PATCH /api/v2/pages/:id` | `{title}` |
| Move | `PATCH /api/v2/pages/:id` | `{parent_page_id}` |
| Clear | `POST /api/v2/pages/:id/clear` | (none) |
| Undo | `POST /api/v2/pages/:id/undo` | (none) |
| Show | `GET /api/v2/pages/:id` | (none) |
| Delete | `DELETE /api/v2/pages/:id` | (none) |
| Search | `POST /api/v2/brains/:b/search` | `{query, kind?, limit?, cross_brain?}` |

All responses use the `{"data": ...}` envelope already handled by `api/client.go`.

---

## Task 1: Remove blocks & connections

Pure deletion. No new tests; success = the tree still builds and the existing suite passes.

**Files:**
- Delete: `internal/api/blocks.go`
- Delete: `internal/api/connections.go`
- Delete: `internal/cli/block.go`
- Delete: `internal/cli/link.go`
- Modify: `internal/api/pages.go` (remove `Blocks` field)
- Modify: `internal/cli/page.go` (remove blocks loop in `page show`)
- Modify: `internal/cli/root.go` (drop block/link registration; update `Long`)

- [ ] **Step 1: Delete the four dead files**

```bash
cd /Users/daniel/Development/magus-cli
git rm internal/api/blocks.go internal/api/connections.go internal/cli/block.go internal/cli/link.go
```

- [ ] **Step 2: Remove the `Blocks` field from the `Page` struct**

In `internal/api/pages.go`, delete this line from the `Page` struct (leave `BlocksAdded` for now; it is a plain int with no `Block` dependency and is removed in Task 7):

```go
	Blocks       []Block `json:"blocks,omitempty"`
```

- [ ] **Step 3: Remove the block-printing loop in `page show`**

In `internal/cli/page.go`, inside `pageShowCmd`'s `RunE`, delete these three lines (the loop over `page.Blocks`):

```go
			for _, b := range page.Blocks {
				fmt.Printf("  [%s] %v\n", b.Type, b.Content)
			}
```

- [ ] **Step 4: Drop block/link command registration and fix help text**

In `internal/cli/root.go`, remove these two lines from the `data` group:

```go
	addInGroup("data", newBlockCmd())
	addInGroup("data", newLinkCmd())
```

And change the root `Long` description so it no longer mentions blocks/connections. Replace:

```go
		Long: `magus is the command-line interface for the Magus brain API.

It authenticates with a Personal Access Token (PAT) scoped to a single
workspace, and exposes commands for managing brains, pages, blocks,
search, and connections. Includes a bundled stdio MCP server (` + "`magus mcp`" + `)
for use with Claude Desktop, Cursor, and other MCP-aware clients.`,
```

with:

```go
		Long: `magus is the command-line interface for the Magus brain API.

It authenticates with a Personal Access Token (PAT) scoped to a single
workspace, and exposes commands for managing brains, pages, and search.
Pages are stored as markdown. Includes a bundled stdio MCP server
(` + "`magus mcp`" + `) for use with Claude Desktop, Cursor, and other MCP-aware
clients.`,
```

- [ ] **Step 5: Verify the tree builds and tests pass**

Run: `cd /Users/daniel/Development/magus-cli && go build ./... && go test ./...`
Expected: build succeeds, all existing tests PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add -A
git commit -m "refactor: remove blocks and connections from the CLI" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Add body-model page client functions (expand)

Additive change to `internal/api/pages.go`. Old functions (`WritePage`, `GetPage` with format) stay until their consumers migrate. TDD with a new `pages_test.go`.

**Files:**
- Modify: `internal/api/pages.go`
- Create: `internal/api/pages_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/pages_test.go`:

```go
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
	_ = json.Unmarshal([]byte(rec[0].body), &sent)
	if sent["title"] != "T" || sent["body"] != "hi" || sent["parent_page_id"] != "par" {
		t.Errorf("sent: %+v", sent)
	}
}

func TestUpdatePageBodyModes(t *testing.T) {
	for _, mode := range []string{"append", "prepend", "replace"} {
		var rec []recorded
		srv := cannedServer(t, &rec, http.StatusOK, `{"data":{"id":"p1","title":"T","body":"x"}}`)
		c := New(srv.URL, "tok", "")
		if _, err := c.UpdatePageBody(context.Background(), "p1", "x", mode); err != nil {
			t.Fatal(err)
		}
		if rec[0].method != http.MethodPatch || rec[0].path != "/api/v2/pages/p1" {
			t.Fatalf("request: %+v", rec[0])
		}
		var sent map[string]any
		_ = json.Unmarshal([]byte(rec[0].body), &sent)
		if sent["body"] != "x" || sent["mode"] != mode {
			t.Errorf("mode %s sent: %+v", mode, sent)
		}
		srv.Close()
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/api/ -run 'CreatePage|UpdatePageBody|ClearPage|UndoPage'`
Expected: FAIL to compile (`undefined: CreatePageInput`, `c.CreatePage`, etc.).

- [ ] **Step 3: Add the new fields and functions**

In `internal/api/pages.go`, add these fields to the `Page` struct (alongside the existing ones; keep `Markdown` and `BlocksAdded` for now):

```go
	Body        string         `json:"body,omitempty"`
	LockVersion int            `json:"lock_version,omitempty"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Icon        string         `json:"icon,omitempty"`
```

Then add these functions at the end of the file:

```go
type CreatePageInput struct {
	Title        string `json:"title"`
	Body         string `json:"body,omitempty"`
	ParentPageID string `json:"parent_page_id,omitempty"`
}

func (c *Client) CreatePage(ctx context.Context, brainID string, input CreatePageInput) (*Page, error) {
	var out Page
	if err := c.Post(ctx, fmt.Sprintf("/api/v2/brains/%s/pages", url.PathEscape(brainID)), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type updatePageBodyInput struct {
	Body string `json:"body"`
	Mode string `json:"mode"`
}

// UpdatePageBody edits a page body. mode is "replace", "append", or "prepend".
func (c *Client) UpdatePageBody(ctx context.Context, pageID, body, mode string) (*Page, error) {
	var out Page
	path := fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID))
	if err := c.Patch(ctx, path, updatePageBodyInput{Body: body, Mode: mode}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ClearPage(ctx context.Context, pageID string) (*Page, error) {
	var out Page
	path := fmt.Sprintf("/api/v2/pages/%s/clear", url.PathEscape(pageID))
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UndoPage(ctx context.Context, pageID string) (*Page, error) {
	var out Page
	path := fmt.Sprintf("/api/v2/pages/%s/undo", url.PathEscape(pageID))
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/api/`
Expected: PASS (including the existing client tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/api/pages.go internal/api/pages_test.go
git commit -m "feat(api): add body-model page client functions" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Add search `kind` / `cross_brain` and hit fields (expand)

Additive change to `internal/api/search.go`. Keep `Mode` and the old hit fields until consumers migrate.

**Files:**
- Modify: `internal/api/search.go`
- Create: `internal/api/search_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/search_test.go`:

```go
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
	_ = json.Unmarshal([]byte(rec[0].body), &sent)
	if sent["kind"] != "semantic" || sent["cross_brain"] != true {
		t.Errorf("sent: %+v", sent)
	}
	if _, hasMode := sent["mode"]; hasMode {
		t.Errorf("mode should not be sent: %+v", sent)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/api/ -run TestSearchPostsKindAndCrossBrain`
Expected: FAIL to compile (`unknown field Kind`, `CrossBrain`, `hits[0].Rank`).

- [ ] **Step 3: Add the new fields**

In `internal/api/search.go`, add to `SearchHit` (keep the existing fields for now):

```go
	Rank     float64 `json:"rank,omitempty"`
	SourceID string  `json:"source_id,omitempty"`
	FileID   string  `json:"file_id,omitempty"`
	Title    string  `json:"title,omitempty"`
```

And add to `SearchInput` (keep `Mode` for now):

```go
	Kind       string `json:"kind,omitempty"`
	CrossBrain bool   `json:"cross_brain,omitempty"`
```

The test asserts `mode` is absent. Since the old `Mode` field is `json:"mode,omitempty"` and the test leaves it empty, it is omitted. Good.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/api/search.go internal/api/search_test.go
git commit -m "feat(api): add search kind, cross_brain, and hit fields" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Shared surgical find/replace (`internal/brain`)

A pure, unique-match-guarded helper used by both the CLI `page edit` and the MCP `page_edit` tool. Lives in a neutral package because `mcp` cannot import `cli`.

**Files:**
- Create: `internal/brain/edit.go`
- Create: `internal/brain/edit_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/brain/edit_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/brain/`
Expected: FAIL (`package internal/brain` / `undefined: ApplyFindReplace`).

- [ ] **Step 3: Write the implementation**

Create `internal/brain/edit.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/brain/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/brain/
git commit -m "feat(brain): shared unique-match find/replace helper" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Rewrite the `page` and `search` commands

Migrate the CLI consumers to the new client functions and verb surface. `root.go`'s `newPageCmd`/`newSearchCmd` names are unchanged, so no registration edits are needed. Success = build + existing suite pass (the CLI commands are thin wiring over already-tested `api` + `brain` code, consistent with the repo's existing test layering).

**Files:**
- Rewrite: `internal/cli/page.go`
- Rewrite: `internal/cli/search.go`

- [ ] **Step 1: Replace `internal/cli/page.go` in full**

```go
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/brain"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newPageCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "page", Short: "Manage pages"}
	cmd.AddCommand(
		pageListCmd(),
		pageShowCmd(),
		pageCreateCmd(),
		pageAppendCmd(),
		pagePrependCmd(),
		pageReplaceCmd(),
		pageEditCmd(),
		pageClearCmd(),
		pageUndoCmd(),
		pageRenameCmd(),
		pageMoveCmd(),
		pageDeleteCmd(),
	)
	return cmd
}

func pageListCmd() *cobra.Command {
	var brainFlag string
	var tree bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pages",
		Long: `List pages in a brain.

If --brain is omitted the active brain (set via 'magus brain use <id>') is used.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			brainID := config.ResolveActiveBrain(cfg, brainFlag)
			if brainID == "" {
				return fmt.Errorf("no brain specified (use --brain <id> or `magus brain use <id>`)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pages, err := c.ListPages(cmd.Context(), brainID, !tree)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(pages)
			}
			rows := make([][]string, 0, len(pages))
			collect(&rows, pages, "")
			output.PrintTable([]string{"title", "slug", "depth"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().BoolVar(&tree, "tree", false, "render tree-shaped output")
	return cmd
}

func collect(rows *[][]string, pages []api.Page, indent string) {
	for _, p := range pages {
		*rows = append(*rows, []string{indent + p.Title, p.Slug, fmt.Sprintf("%d", p.Depth)})
		if len(p.Children) > 0 {
			collect(rows, p.Children, indent+"  ")
		}
	}
}

func pageShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Show a page (prints its markdown body)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.GetPage(cmd.Context(), pageID, "")
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			fmt.Println(page.Body)
			return nil
		},
	}
	return cmd
}

func pageCreateCmd() *cobra.Command {
	var brainFlag, parent, file string
	cmd := &cobra.Command{
		Use:   "create <title>",
		Args:  cobra.ExactArgs(1),
		Short: "Create a new page (body from stdin or --file)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			brainID := config.ResolveActiveBrain(cfg, brainFlag)
			if brainID == "" {
				return fmt.Errorf("no brain specified (use --brain <id> or `magus brain use <id>`)")
			}
			body, err := readContent(file)
			if err != nil {
				return err
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			input := api.CreatePageInput{Title: args[0], Body: body}
			if parent != "" {
				parentID, err := resolvePage(cmd.Context(), c, parent)
				if err != nil {
					return fmt.Errorf("resolve --parent: %w", err)
				}
				input.ParentPageID = parentID
			}
			page, err := c.CreatePage(cmd.Context(), brainID, input)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Created %q (%s)", page.Title, page.Slug))
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent page ref (id|slug|brain/slug)")
	cmd.Flags().StringVar(&file, "file", "", "markdown file path; defaults to stdin")
	return cmd
}

// bodyEditCmd builds append/prepend/replace: each reads markdown from
// stdin/--file and PATCHes the page body with the given mode.
func bodyEditCmd(use, short, mode string) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   use,
		Args:  cobra.ExactArgs(1),
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readContent(file)
			if err != nil {
				return err
			}
			if body == "" {
				return fmt.Errorf("no content provided (pipe markdown via stdin or pass --file)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.UpdatePageBody(cmd.Context(), pageID, body, mode)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Updated %q (%s)", page.Title, mode))
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "markdown file path; defaults to stdin")
	return cmd
}

func pageAppendCmd() *cobra.Command {
	return bodyEditCmd("append <ref>", "Append markdown to a page", "append")
}

func pagePrependCmd() *cobra.Command {
	return bodyEditCmd("prepend <ref>", "Prepend markdown to a page", "prepend")
}

func pageReplaceCmd() *cobra.Command {
	return bodyEditCmd("replace <ref>", "Overwrite a page's entire body", "replace")
}

func pageEditCmd() *cobra.Command {
	var find, with string
	var all bool
	cmd := &cobra.Command{
		Use:   "edit <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Find-and-replace within a page body",
		RunE: func(cmd *cobra.Command, args []string) error {
			if find == "" {
				return fmt.Errorf("--find is required")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.GetPage(cmd.Context(), pageID, "")
			if err != nil {
				return err
			}
			next, err := brain.ApplyFindReplace(page.Body, find, with, all)
			if err != nil {
				return err
			}
			updated, err := c.UpdatePageBody(cmd.Context(), pageID, next, "replace")
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(updated)
			}
			output.Println(quietMode, fmt.Sprintf("Edited %q", updated.Title))
			return nil
		},
	}
	cmd.Flags().StringVar(&find, "find", "", "text to find (must match exactly once unless --all)")
	cmd.Flags().StringVar(&with, "with", "", "replacement text")
	cmd.Flags().BoolVar(&all, "all", false, "replace all occurrences")
	return cmd
}

func pageClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Empty a page's body (the page itself is kept)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.ClearPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Cleared %q", page.Title))
			return nil
		},
	}
}

func pageUndoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undo <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Undo the last body change on a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			page, err := c.UndoPage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(page)
			}
			output.Println(quietMode, fmt.Sprintf("Reverted last change on %q", page.Title))
			return nil
		},
	}
}

func pageRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <ref> <title>",
		Args:  cobra.ExactArgs(2),
		Short: "Rename a page",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			title := args[1]
			p, err := c.UpdatePage(cmd.Context(), pageID, api.UpdatePageInput{Title: &title})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			output.Println(quietMode, fmt.Sprintf("Renamed to %q", p.Title))
			return nil
		},
	}
}

func pageMoveCmd() *cobra.Command {
	var parent string
	cmd := &cobra.Command{
		Use:   "move <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Move a page under another parent (or 'none' for root)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if parent == "" {
				return fmt.Errorf("--parent is required (use 'none' to move to root)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			var parentPtr *string
			if parent == "none" {
				empty := ""
				parentPtr = &empty
			} else {
				parentID, err := resolvePage(cmd.Context(), c, parent)
				if err != nil {
					return fmt.Errorf("resolve --parent: %w", err)
				}
				parentPtr = &parentID
			}
			p, err := c.UpdatePage(cmd.Context(), pageID, api.UpdatePageInput{ParentPageID: parentPtr})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(p)
			}
			output.Println(quietMode, fmt.Sprintf("Moved %q", p.Title))
			return nil
		},
	}
	cmd.Flags().StringVar(&parent, "parent", "", "new parent page ref, or 'none'")
	return cmd
}

func pageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <ref>",
		Args:  cobra.ExactArgs(1),
		Short: "Soft-delete a page (recoverable from trash)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			pageID, err := resolvePage(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			res, err := c.DeletePage(cmd.Context(), pageID)
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(res)
			}
			output.Println(quietMode, fmt.Sprintf("Trashed (deleted_at=%s)", res.DeletedAt))
			return nil
		},
	}
}

func readContent(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 2: Replace `internal/cli/search.go` in full**

```go
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
	"github.com/wir-drei-digital/magus-cli/internal/output"
)

func newSearchCmd() *cobra.Command {
	var brainFlag, kind string
	var limit int
	var crossBrain bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Args:  cobra.ExactArgs(1),
		Short: "Search brain content",
		Long: `Search across a brain's pages and attached file chunks.

If --brain is omitted the active brain (set via 'magus brain use <id>') is used.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			brainID := config.ResolveActiveBrain(cfg, brainFlag)
			if brainID == "" {
				return fmt.Errorf("no brain specified (use --brain <id> or `magus brain use <id>`)")
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			hits, err := c.Search(cmd.Context(), brainID, api.SearchInput{
				Query: args[0], Kind: kind, Limit: limit, CrossBrain: crossBrain,
			})
			if err != nil {
				return err
			}
			if jsonMode {
				return output.JSON(hits)
			}
			for _, h := range hits {
				score := h.Score
				if score == 0 {
					score = h.Rank
				}
				label := h.Title
				if label == "" {
					label = h.PageID
				}
				fmt.Printf("[%s %.2f] %s  %s\n", h.Kind, score, label, h.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&brainFlag, "brain", "", "brain id or slug (defaults to active brain)")
	cmd.Flags().StringVar(&kind, "kind", "", "unified (default) | semantic | text")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results")
	cmd.Flags().BoolVar(&crossBrain, "cross-brain", false, "search across all accessible brains")
	return cmd
}
```

- [ ] **Step 3: Verify build and tests**

Run: `cd /Users/daniel/Development/magus-cli && go build ./... && go test ./...`
Expected: build succeeds; all tests PASS. (The `api.WritePage` / `Markdown` / `GetPage` format arg are now unused by the CLI but still referenced by the MCP package, so they remain until Task 7.)

- [ ] **Step 4: Smoke-check the command tree compiles into the binary**

Run: `cd /Users/daniel/Development/magus-cli && go run ./cmd/magus page --help`
Expected: help lists `create append prepend replace edit clear undo rename move delete list show` and no `write`.

- [ ] **Step 5: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/cli/page.go internal/cli/search.go
git commit -m "feat(cli): explicit page verbs and search --kind" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Rewrite the MCP tool surface

Mirror the page verbs and switch search to `kind`. Update the tests.

**Files:**
- Rewrite: `internal/mcp/tools.go`
- Rewrite: `internal/mcp/tools_test.go`

- [ ] **Step 1: Replace `internal/mcp/tools.go` in full**

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/brain"
)

func tools(c *api.Client, activeBrain string) []registeredTool {
	return []registeredTool{
		{
			def: mcpgo.NewTool("brain_list",
				mcpgo.WithDescription("List all brains in the active workspace.")),
			handler: func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				return brainListCore(ctx, c)
			},
		},
		{
			def: mcpgo.NewTool("brain_create",
				mcpgo.WithDescription("Create a new brain in the active workspace."),
				mcpgo.WithString("title", mcpgo.Required(), mcpgo.Description("Brain title")),
				mcpgo.WithString("description", mcpgo.Description("Optional description")),
				mcpgo.WithString("icon"),
				mcpgo.WithString("color")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return brainCreateCore(ctx, c, map[string]string{
					"title":       title,
					"description": stringArg(req, "description"),
					"icon":        stringArg(req, "icon"),
					"color":       stringArg(req, "color"),
				})
			},
		},
		{
			def: mcpgo.NewTool("page_list",
				mcpgo.WithDescription("List pages in a brain as a tree or flat array."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("as", mcpgo.Description("tree (default) or flat"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brainRef, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				return pageListCore(ctx, c, brainRef, stringArg(req, "as") == "flat")
			},
		},
		{
			def: mcpgo.NewTool("page_read",
				mcpgo.WithDescription("Read a page's markdown body."),
				mcpgo.WithString("page", mcpgo.Required(), mcpgo.Description("Page id"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return pageReadCore(ctx, c, pageID)
			},
		},
		{
			def: mcpgo.NewTool("page_create",
				mcpgo.WithDescription("Create a new page with an optional markdown body. Body may use frontmatter, [[wikilinks]], ```source/```callout fenced blocks, magus://file links, and #tags."),
				mcpgo.WithString("brain", mcpgo.Required()),
				mcpgo.WithString("title", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Description("Markdown body")),
				mcpgo.WithString("parent_page_id", mcpgo.Description("Parent page id for nesting"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				brainRef, err := req.RequireString("brain")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return pageCreateCore(ctx, c, brainRef, api.CreatePageInput{
					Title:        title,
					Body:         stringArg(req, "body"),
					ParentPageID: stringArg(req, "parent_page_id"),
				})
			},
		},
		{
			def: mcpgo.NewTool("page_append",
				mcpgo.WithDescription("Append markdown to the end of a page body."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "append"),
		},
		{
			def: mcpgo.NewTool("page_prepend",
				mcpgo.WithDescription("Prepend markdown to the start of a page body."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "prepend"),
		},
		{
			def: mcpgo.NewTool("page_replace",
				mcpgo.WithDescription("Overwrite a page's entire body. Destructive."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("body", mcpgo.Required())),
			handler: bodyEditHandler(c, "replace"),
		},
		{
			def: mcpgo.NewTool("page_edit",
				mcpgo.WithDescription("Find-and-replace within a page body. The find text must match exactly once unless all=true."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("find", mcpgo.Required()),
				mcpgo.WithString("with", mcpgo.Description("Replacement text")),
				mcpgo.WithBoolean("all", mcpgo.Description("Replace every occurrence"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				find, err := req.RequireString("find")
				if err != nil {
					return nil, err
				}
				return pageEditCore(ctx, c, pageID, find, stringArg(req, "with"), boolArg(req, "all"))
			},
		},
		{
			def: mcpgo.NewTool("page_clear",
				mcpgo.WithDescription("Empty a page's body; the page itself is kept."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.ClearPage(ctx, pageID))
			},
		},
		{
			def: mcpgo.NewTool("page_undo",
				mcpgo.WithDescription("Undo the last body change on a page."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.UndoPage(ctx, pageID))
			},
		},
		{
			def: mcpgo.NewTool("page_rename",
				mcpgo.WithDescription("Rename a page."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("title", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				title, err := req.RequireString("title")
				if err != nil {
					return nil, err
				}
				return resultFromPage(c.UpdatePage(ctx, pageID, api.UpdatePageInput{Title: &title}))
			},
		},
		{
			def: mcpgo.NewTool("page_move",
				mcpgo.WithDescription("Move a page under another parent. Pass an empty parent_page_id to move to root."),
				mcpgo.WithString("page", mcpgo.Required()),
				mcpgo.WithString("parent_page_id", mcpgo.Description("New parent page id, or empty for root"))),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				parent := stringArg(req, "parent_page_id")
				return resultFromPage(c.UpdatePage(ctx, pageID, api.UpdatePageInput{ParentPageID: &parent}))
			},
		},
		{
			def: mcpgo.NewTool("page_delete",
				mcpgo.WithDescription("Soft-delete a page (recoverable from trash)."),
				mcpgo.WithString("page", mcpgo.Required())),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				pageID, err := req.RequireString("page")
				if err != nil {
					return nil, err
				}
				return pageDeleteCore(ctx, c, pageID)
			},
		},
		{
			def: mcpgo.NewTool("brain_search",
				mcpgo.WithDescription("Search across brain content. Returns ranked hits from page bodies and attached file chunks."),
				mcpgo.WithString("query", mcpgo.Required()),
				mcpgo.WithString("brain"),
				mcpgo.WithString("kind", mcpgo.Description("unified (default) | semantic | text")),
				mcpgo.WithNumber("limit")),
			handler: func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				query, err := req.RequireString("query")
				if err != nil {
					return nil, err
				}
				limit := 0
				if n, ok := numberArg(req, "limit"); ok {
					limit = int(n)
				}
				return brainSearchCore(ctx, c, activeBrain, stringArg(req, "brain"), query, stringArg(req, "kind"), limit)
			},
		},
	}
}

// ---- core functions ---------------------------------------------------

func brainListCore(ctx context.Context, c *api.Client) (*mcpgo.CallToolResult, error) {
	brains, err := c.ListBrains(ctx, api.ListBrainsOpts{})
	if err != nil {
		return nil, err
	}
	return jsonResult(brains)
}

func brainCreateCore(ctx context.Context, c *api.Client, args map[string]string) (*mcpgo.CallToolResult, error) {
	brain, err := c.CreateBrain(ctx, api.CreateBrainInput{
		Title:       args["title"],
		Description: args["description"],
		Icon:        args["icon"],
		Color:       args["color"],
	})
	if err != nil {
		return nil, err
	}
	return jsonResult(brain)
}

func pageListCore(ctx context.Context, c *api.Client, brainID string, asFlat bool) (*mcpgo.CallToolResult, error) {
	pages, err := c.ListPages(ctx, brainID, asFlat)
	if err != nil {
		return nil, err
	}
	return jsonResult(pages)
}

func pageReadCore(ctx context.Context, c *api.Client, pageID string) (*mcpgo.CallToolResult, error) {
	return resultFromPage(c.GetPage(ctx, pageID, ""))
}

func pageCreateCore(ctx context.Context, c *api.Client, brainID string, input api.CreatePageInput) (*mcpgo.CallToolResult, error) {
	return resultFromPage(c.CreatePage(ctx, brainID, input))
}

func pageEditCore(ctx context.Context, c *api.Client, pageID, find, with string, all bool) (*mcpgo.CallToolResult, error) {
	page, err := c.GetPage(ctx, pageID, "")
	if err != nil {
		return nil, err
	}
	next, err := brain.ApplyFindReplace(page.Body, find, with, all)
	if err != nil {
		return nil, err
	}
	return resultFromPage(c.UpdatePageBody(ctx, pageID, next, "replace"))
}

func pageDeleteCore(ctx context.Context, c *api.Client, pageID string) (*mcpgo.CallToolResult, error) {
	res, err := c.DeletePage(ctx, pageID)
	if err != nil {
		return nil, err
	}
	return jsonResult(res)
}

func brainSearchCore(ctx context.Context, c *api.Client, activeBrain, brainArg, query, kind string, limit int) (*mcpgo.CallToolResult, error) {
	brainRef := brainArg
	if brainRef == "" {
		brainRef = activeBrain
	}
	if brainRef == "" {
		return nil, fmt.Errorf("no brain specified (pass brain arg or run `magus brain use <id>`)")
	}
	hits, err := c.Search(ctx, brainRef, api.SearchInput{Query: query, Kind: kind, Limit: limit})
	if err != nil {
		return nil, err
	}
	return jsonResult(hits)
}

// ---- helpers ----------------------------------------------------------

// bodyEditHandler builds an append/prepend/replace MCP handler.
func bodyEditHandler(c *api.Client, mode string) func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		pageID, err := req.RequireString("page")
		if err != nil {
			return nil, err
		}
		body, err := req.RequireString("body")
		if err != nil {
			return nil, err
		}
		return resultFromPage(c.UpdatePageBody(ctx, pageID, body, mode))
	}
}

func resultFromPage(p *api.Page, err error) (*mcpgo.CallToolResult, error) {
	if err != nil {
		return nil, err
	}
	return jsonResult(p)
}

func stringArg(req mcpgo.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

func boolArg(req mcpgo.CallToolRequest, key string) bool {
	return req.GetBool(key, false)
}

func numberArg(req mcpgo.CallToolRequest, key string) (float64, bool) {
	v, err := req.RequireFloat(key)
	if err != nil {
		return 0, false
	}
	return v, true
}

func jsonResult(payload any) (*mcpgo.CallToolResult, error) {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpgo.NewToolResultText(string(b)), nil
}
```

- [ ] **Step 2: Replace `internal/mcp/tools_test.go` in full**

```go
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wir-drei-digital/magus-cli/internal/api"
)

type recordedRequest struct {
	method string
	path   string
	body   string
}

// canned builds an httptest.Server that records every request and responds
// with status + body. Pass a slice of bodies to answer successive requests
// (the last one repeats); a single body answers every request.
func canned(t *testing.T, recorded *[]recordedRequest, status int, bodies ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		_, _ = io.Copy(&b, r.Body)
		*recorded = append(*recorded, recordedRequest{method: r.Method, path: r.URL.RequestURI(), body: b.String()})
		idx := len(*recorded) - 1
		if idx >= len(bodies) {
			idx = len(bodies) - 1
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, bodies[idx])
	})
	return httptest.NewServer(mux)
}

func newTestClient(server *httptest.Server) *api.Client {
	return api.New(server.URL, "test-token", "magus-cli/test")
}

func TestBrainListCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[{"id":"b1","slug":"work","title":"Work"}]}`)
	defer srv.Close()
	if _, err := brainListCore(context.Background(), newTestClient(srv)); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodGet || got[0].path != "/api/v2/brains" {
		t.Errorf("unexpected request: %+v", got[0])
	}
}

func TestBrainCreateCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"b1","title":"Hello"}}`)
	defer srv.Close()
	if _, err := brainCreateCore(context.Background(), newTestClient(srv), map[string]string{"title": "Hello"}); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains" {
		t.Errorf("request: %+v", got[0])
	}
}

func TestPageListCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[{"id":"p1","title":"Top"}]}`)
	defer srv.Close()
	if _, err := pageListCore(context.Background(), newTestClient(srv), "brain-slug", true); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/brain-slug/pages?as=flat" {
		t.Errorf("path: %s", got[0].path)
	}
}

func TestPageReadCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":{"id":"p1","title":"Top","body":"# hello"}}`)
	defer srv.Close()
	if _, err := pageReadCore(context.Background(), newTestClient(srv), "p1"); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodGet || got[0].path != "/api/v2/pages/p1" {
		t.Errorf("request: %+v", got[0])
	}
}

func TestPageCreateCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusCreated, `{"data":{"id":"p2","title":"New","body":"hi"}}`)
	defer srv.Close()
	in := api.CreatePageInput{Title: "New", Body: "hi"}
	if _, err := pageCreateCore(context.Background(), newTestClient(srv), "brain-slug", in); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains/brain-slug/pages" {
		t.Errorf("request: %+v", got[0])
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(got[0].body), &sent)
	if sent["title"] != "New" || sent["body"] != "hi" {
		t.Errorf("body: %+v", sent)
	}
}

func TestPageEditCoreReadsThenReplaces(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK,
		`{"data":{"id":"p1","title":"T","body":"hello world"}}`,
		`{"data":{"id":"p1","title":"T","body":"hello there"}}`)
	defer srv.Close()
	if _, err := pageEditCore(context.Background(), newTestClient(srv), "p1", "world", "there", false); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected GET then PATCH, got %d requests", len(got))
	}
	if got[0].method != http.MethodGet {
		t.Errorf("first request method: %s", got[0].method)
	}
	if got[1].method != http.MethodPatch {
		t.Errorf("second request method: %s", got[1].method)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(got[1].body), &sent)
	if sent["body"] != "hello there" || sent["mode"] != "replace" {
		t.Errorf("patch body: %+v", sent)
	}
}

func TestPageDeleteCore(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":{"id":"p1","deleted_at":"2026-05-17T12:00:00Z"}}`)
	defer srv.Close()
	if _, err := pageDeleteCore(context.Background(), newTestClient(srv), "p1"); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodDelete || got[0].path != "/api/v2/pages/p1" {
		t.Errorf("request: %+v", got[0])
	}
}

func TestBrainSearchCoreFallsBackToActiveBrain(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	if _, err := brainSearchCore(context.Background(), newTestClient(srv), "fallback", "", "needle", "", 0); err != nil {
		t.Fatal(err)
	}
	if got[0].path != "/api/v2/brains/fallback/search" {
		t.Errorf("expected fallback brain in path: %s", got[0].path)
	}
}

func TestBrainSearchCoreErrorsWhenNoBrain(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	_, err := brainSearchCore(context.Background(), newTestClient(srv), "", "", "needle", "", 0)
	if err == nil || !strings.Contains(err.Error(), "no brain specified") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBrainSearchCorePostsKind(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	if _, err := brainSearchCore(context.Background(), newTestClient(srv), "", "explicit", "needle", "semantic", 5); err != nil {
		t.Fatal(err)
	}
	if got[0].method != http.MethodPost || got[0].path != "/api/v2/brains/explicit/search" {
		t.Errorf("request: %+v", got[0])
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(got[0].body), &sent)
	if sent["query"] != "needle" || sent["kind"] != "semantic" {
		t.Errorf("body: %+v", sent)
	}
	if _, hasMode := sent["mode"]; hasMode {
		t.Errorf("mode should not be sent: %+v", sent)
	}
}

func TestToolsRegistration(t *testing.T) {
	var got []recordedRequest
	srv := canned(t, &got, http.StatusOK, `{"data":[]}`)
	defer srv.Close()
	registered := tools(newTestClient(srv), "any-active-brain")
	want := []string{
		"brain_list", "brain_create",
		"page_list", "page_read",
		"page_create", "page_append", "page_prepend", "page_replace", "page_edit",
		"page_clear", "page_undo", "page_rename", "page_move", "page_delete",
		"brain_search",
	}
	if len(registered) != len(want) {
		t.Fatalf("expected %d tools, got %d", len(want), len(registered))
	}
	names := make(map[string]bool, len(registered))
	for _, r := range registered {
		names[r.def.Name] = true
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing tool %q", n)
		}
	}
}
```

- [ ] **Step 3: Run the MCP tests**

Run: `cd /Users/daniel/Development/magus-cli && go test ./internal/mcp/`
Expected: PASS.

- [ ] **Step 4: Full build + suite**

Run: `cd /Users/daniel/Development/magus-cli && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): mirror page verbs and search --kind" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Contract — remove the dead client surface

All consumers now use the new functions. Remove the transitional old code and drop the `GetPage` format parameter.

**Files:**
- Modify: `internal/api/pages.go`
- Modify: `internal/api/search.go`
- Modify: `internal/cli/page.go` (two `GetPage` call sites)
- Modify: `internal/mcp/tools.go` (one `GetPage` call site)

- [ ] **Step 1: Trim `internal/api/pages.go`**

Remove `Markdown` and `BlocksAdded` from the `Page` struct. Delete `WritePage` and `WritePageInput` entirely. Change `GetPage` to drop the `format` parameter:

```go
func (c *Client) GetPage(ctx context.Context, pageID string) (*Page, error) {
	var out Page
	if err := c.Get(ctx, fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

The final `Page` struct is:

```go
type Page struct {
	ID           string         `json:"id"`
	Slug         string         `json:"slug"`
	Title        string         `json:"title"`
	Body         string         `json:"body,omitempty"`
	LockVersion  int            `json:"lock_version,omitempty"`
	Frontmatter  map[string]any `json:"frontmatter,omitempty"`
	Icon         string         `json:"icon,omitempty"`
	BrainID      string         `json:"brain_id"`
	ParentPageID *string        `json:"parent_page_id"`
	Depth        int            `json:"depth"`
	UpdatedAt    string         `json:"updated_at"`
	Children     []Page         `json:"children,omitempty"`
	DeletedAt    string         `json:"deleted_at,omitempty"`
}
```

- [ ] **Step 2: Trim `internal/api/search.go`**

Remove `Mode` from `SearchInput`. Remove `ID`, `PageTitle`, and `PageNumber` from `SearchHit`. The final structs:

```go
type SearchHit struct {
	Kind     string  `json:"kind"`
	Score    float64 `json:"score,omitempty"`
	Rank     float64 `json:"rank,omitempty"`
	BrainID  string  `json:"brain_id,omitempty"`
	PageID   string  `json:"page_id,omitempty"`
	SourceID string  `json:"source_id,omitempty"`
	FileID   string  `json:"file_id,omitempty"`
	Title    string  `json:"title,omitempty"`
	Snippet  string  `json:"snippet"`
}

type SearchInput struct {
	Query      string `json:"query"`
	Kind       string `json:"kind,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	CrossBrain bool   `json:"cross_brain,omitempty"`
}
```

- [ ] **Step 3: Update the `GetPage` call sites**

In `internal/cli/page.go`, change both `c.GetPage(cmd.Context(), pageID, "")` calls (in `pageShowCmd` and `pageEditCmd`) to `c.GetPage(cmd.Context(), pageID)`.

In `internal/mcp/tools.go`, change both `c.GetPage(ctx, pageID, "")` calls (in `pageReadCore` and `pageEditCore`) to `c.GetPage(ctx, pageID)`.

- [ ] **Step 4: Verify build and tests**

Run: `cd /Users/daniel/Development/magus-cli && go build ./... && go test ./...`
Expected: build succeeds, all tests PASS.

- [ ] **Step 5: Confirm no stragglers reference removed symbols**

Run: `cd /Users/daniel/Development/magus-cli && grep -rn "WritePage\|BlocksAdded\|\.Markdown\|PageTitle\|SearchInput{.*Mode" --include="*.go" .`
Expected: no matches (empty output).

- [ ] **Step 6: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add internal/api/pages.go internal/api/search.go internal/cli/page.go internal/mcp/tools.go
git commit -m "refactor(api): drop legacy write/markdown/mode surface" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Rewrite SKILL.md, README, QUICKSTART

Update the agent-facing docs. SKILL.md has two byte-identical copies kept in sync by `make sync-skill`; edit the canonical one and regenerate.

**Files:**
- Rewrite: `plugins/magus/skills/magus/SKILL.md`
- Regenerate: `internal/skill/SKILL.md` (via `make sync-skill`)
- Modify: `README.md`
- Rewrite: `docs/QUICKSTART.md`

- [ ] **Step 1: Replace `plugins/magus/skills/magus/SKILL.md` in full**

````markdown
---
name: magus
description: Use when the user wants to read, write, search, or organize their Magus knowledge brain (also called "my brain", "my notes", "my second brain"). The magus CLI is a Bash-callable binary that authenticates against the user's workspace and exposes brain and page operations with JSON output for scripting.
allowed-tools: Bash, Read, Grep
user-invocable: true
---

# magus: Knowledge Brain CLI

The user has a persistent knowledge brain. A brain holds markdown **pages** in a hierarchy (max 3 levels deep). Each page is a single CommonMark document. The CLI talks to the user's Magus workspace over HTTP using a Personal Access Token stored on this machine.

## When to use

Invoke `magus` when:

- The user asks you to save research, decisions, or observations for later
- The user asks you to find or recall content from past sessions
- The user references "my brain", "my notes", "my second brain", or "my knowledge base"
- You finished a non-trivial piece of work (a design, a debugging trail, a decision tree) and the user would benefit from a written record they can search later
- The user explicitly says "magus this" or "save that to magus"

## Check setup first

Before any operation, verify magus is configured:

```sh
magus whoami
```

If this prints `no active profile (run \`magus login\`)`, STOP and instruct the user to run `magus login` themselves. The login flow opens their browser for approval and you cannot complete it for them. After they run it once, the token is stored and you can use the CLI freely.

If `magus whoami` succeeds, proceed.

## Pick and pin a brain

```sh
magus brain list --json
```

Pick the brain by `slug` (preferred) or `id`. Then pin it for the session so later commands can drop `--brain`:

```sh
magus brain use project-x
magus brain current            # print the active brain (non-zero exit if unset)
magus brain unset              # clear it
```

Create a new brain with `magus brain create "Project X"`.

**Resolution rule** everywhere a brain is needed: explicit `--brain` wins, then the active brain, else an error.

## Pages are markdown

A page is one markdown body. Change it with explicit verbs:

```sh
magus page create "Title"      # new page, body from stdin or --file
magus page append <ref>        # add to the end
magus page prepend <ref>       # add to the start
magus page replace <ref>       # overwrite the whole body (destructive)
magus page edit <ref> --find "old" --with "new"   # surgical find/replace
magus page clear <ref>         # empty the body (page kept)
magus page undo <ref>          # revert the last body change
magus page show <ref>          # print the markdown body
magus page list [--tree]       # browse the hierarchy
magus page rename <ref> "New Title"
magus page move <ref> --parent <ref|none>
magus page delete <ref>        # soft-delete (recoverable from trash)
```

`<ref>` is a page id, a page slug (active brain), or `brain/page-slug`.

## Save content (the most common operation)

```sh
cat <<'EOF' | magus page create "API Design"
# API Design Decisions

- Bearer token via PAT, one token per workspace
- Soft-delete pages, recover from trash
EOF
```

Add to an existing page instead of creating a new one:

```sh
echo "- Decided to cache embeddings" | magus page append "API Design"
```

Nest a page with `--parent` (there is no slash-path magic):

```sh
echo "..." | magus page create "Auth" --parent "API Design"
```

## Page body syntax

The body is CommonMark plus a few Magus extensions you can write directly:

```text
---
icon: 🧠
tags: [ml, research]
aliases: [Old Name]
---

[[Another Page]]                  link to another page
[[Another Page|label]]            link with custom display text

```source
url: https://example.com
title: Example Article
source_type: web                  web | paper | book | video
```

```callout
variant: info                     info | note | insight | warning | question
text: A highlighted note
```

[📎 caption](magus://file/<id>)    attach an uploaded file
![caption](magus://image/<id>)     embed an uploaded image
#tag #multi-word                   inline tags
```

**Linking is wikilinks.** There is no `link` command: to connect pages, write `[[Target Page]]` into a body with `page append` or `page edit`.

## Search before generating

```sh
magus search "rate limit strategy" --kind unified --limit 5 --json
```

Kinds: `unified` (default; semantic + full-text + file chunks), `semantic` (embeddings only), `text` (keyword). Add `--cross-brain` to span every brain you can access. JSON hits carry `kind`, `score` (or `rank` for whole-page hits), `snippet`, and reference ids.

## Read a page back

```sh
magus page show <ref>
```

Prints the markdown body. Add `--json` for metadata (id, slug, frontmatter, lock_version).

## Scripting patterns

- `--json` returns the raw API response on every read. Pipe to `jq`.
- The CLI exits non-zero on errors; standard shell error handling works.
- `MAGUS_API_TOKEN` and `MAGUS_API_URL` override the stored profile when set.
- Multi-workspace: `magus profiles` lists profiles, `magus --profile <name> ...` overrides per-invocation.

## When NOT to use

- **Source code:** the brain is for notes and decisions, not code dumps.
- **Ephemeral session state:** this conversation's TODO list belongs in the conversation.
- **Quoting a single sentence:** if the user just wants you to remember something for the rest of the session, a normal reply is fine.

## MCP alternative

This same binary serves an MCP stdio server (`magus mcp`). If the user has it configured in their MCP client and you see tools like `page_create`, `page_append`, `page_edit`, `brain_search`, prefer those over shelling out. The MCP path is structured and avoids parsing JSON.

## Full command reference

```
magus brain list|create|show|archive
magus brain use <ref>|current|unset
magus page list|show|create|append|prepend|replace|edit|clear|undo|rename|move|delete
magus search <query> [--kind unified|semantic|text] [--cross-brain] [--limit N]
magus profiles, magus profile use <name>
magus login [--token PAT], magus logout, magus whoami
magus mcp
magus version
magus update [--check] [--force]
```

If the CLI behaves oddly or reports a missing feature, suggest `magus update` before debugging further.

Global flags: `--profile <name>`, `--json`, `--quiet`. Run `magus <cmd> --help` for any subcommand's flags.
````

- [ ] **Step 2: Regenerate the embedded copy**

Run: `cd /Users/daniel/Development/magus-cli && make sync-skill`
Expected: `internal/skill/SKILL.md` is updated to match the canonical copy.

- [ ] **Step 3: Update `README.md`**

Replace the quickstart page example. Change:

```sh
magus brain create "My Research"
echo "# Notes\n\nFirst paragraph." | magus page write my-research "Today/Notes"
```

to:

```sh
magus brain create "My Research"
magus brain use my-research
printf '# Notes\n\nFirst paragraph.\n' | magus page create "Today Notes"
```

And in the `## Commands` list, replace these three lines:

```
- `magus page list|show|write|rename|move|delete`
- `magus search <query> [--brain <id>]`
- `magus block add|edit|delete`
- `magus link <source-block> <target> [--type relates_to]`
```

with:

```
- `magus page list|show|create|append|prepend|replace|edit|clear|undo|rename|move|delete`
- `magus search <query> [--kind unified|semantic|text] [--cross-brain]`
```

- [ ] **Step 4: Replace `docs/QUICKSTART.md` sections**

Replace the "Write a page from markdown", "Search", and "Read back as markdown" sections, and the MCP tools list. New content for those sections:

````markdown
## Write a page

Create a page (body from stdin or `--file`):

```sh
printf '# Heading\n\nBody\n' | magus page create "Today Notes"
magus page create "Today Notes" --file ~/notes.md
```

Add to an existing page instead of creating:

```sh
echo "- another note" | magus page append "Today Notes"
```

Overwrite, surgically edit, or revert:

```sh
echo "fresh body" | magus page replace "Today Notes"
magus page edit "Today Notes" --find "typo" --with "fixed"
magus page undo "Today Notes"
```

Nest with `--parent <ref>` (a page id, slug, or `brain/slug`).

## Search

```sh
magus search "neural networks" --brain research --kind unified --limit 10
```

`--kind` is `unified` (default; semantic + text + file chunks), `semantic` (embeddings only), or `text` (full-text). Add `--cross-brain` to span every accessible brain.

## Read back as markdown

```sh
magus page show <ref>
```

`<ref>` is a page id, a page slug (active brain), or `brain/page-slug`.
````

And replace the MCP tools list at the bottom with:

```
- `brain_list`, `brain_create`
- `page_list`, `page_read`
- `page_create`, `page_append`, `page_prepend`, `page_replace`, `page_edit`
- `page_clear`, `page_undo`, `page_rename`, `page_move`, `page_delete`
- `brain_search`
```

- [ ] **Step 5: Verify skill parity and the suite**

Run: `cd /Users/daniel/Development/magus-cli && go test ./...`
Expected: PASS, including `TestSkillContentMatchesPluginCopy` (the two SKILL.md copies match).

- [ ] **Step 6: Commit**

```bash
cd /Users/daniel/Development/magus-cli
git add plugins/magus/skills/magus/SKILL.md internal/skill/SKILL.md README.md docs/QUICKSTART.md
git commit -m "docs: update skill and docs for markdown-as-storage" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Final verification gate

- [ ] **Step 1: Format check**

Run: `cd /Users/daniel/Development/magus-cli && gofmt -l .`
Expected: no output (all files formatted). If any file is listed, run `gofmt -w <file>` and re-check.

- [ ] **Step 2: Vet, build, race-tested suite**

Run: `cd /Users/daniel/Development/magus-cli && go vet ./... && go build ./... && go test -race ./...`
Expected: all succeed.

- [ ] **Step 3: Skill sync is clean**

Run: `cd /Users/daniel/Development/magus-cli && make sync-skill && git diff --exit-code internal/skill/SKILL.md`
Expected: no diff (already in sync).

- [ ] **Step 4: Binary smoke test of the new surface**

Run: `cd /Users/daniel/Development/magus-cli && go run ./cmd/magus page --help && go run ./cmd/magus --help`
Expected: `page` lists the new verbs; the root help has no `block` or `link` commands.

- [ ] **Step 5: Commit any formatting fixes (if Step 1 changed files)**

```bash
cd /Users/daniel/Development/magus-cli
git add -A
git commit -m "style: gofmt" -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

If nothing changed, skip this commit.

---

## Self-review notes (author)

- **Spec coverage:** every spec section maps to a task: command surface (Tasks 5, 6), api/pages changes (Tasks 2, 7), api/search changes (Tasks 3, 7), blocks/connections removal (Task 1), surgical edit (Task 4 + wired in 5/6), MCP (Task 6), SKILL.md/README/QUICKSTART (Task 8), error handling (surfaced via `api.Error` which already carries `Details`), testing (Tasks 2-4, 6), delivery gate (Task 9).
- **Type consistency:** `CreatePageInput`, `UpdatePageBody`, `ClearPage`, `UndoPage`, `GetPage(ctx, id)` (final), `SearchInput{Query, Kind, Limit, CrossBrain}`, `SearchHit{Kind, Score, Rank, BrainID, PageID, SourceID, FileID, Title, Snippet}`, and `brain.ApplyFindReplace` are used identically across cli and mcp.
- **Expand/contract invariant:** Tasks 1-6 each leave a green tree; Task 7 only removes symbols that Tasks 5-6 stopped using. The `grep` in Task 7 Step 5 is the backstop.
- **Known deferral:** `page create` does not pre-resolve title collisions; it relies on the server's 409 `already_exists`, whose `details.existing_page_id` is available on the returned `api.Error` for the agent to act on. No extra task needed for v1.
