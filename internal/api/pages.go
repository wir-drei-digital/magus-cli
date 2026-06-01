package api

import (
	"context"
	"fmt"
	"net/url"
)

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

func (c *Client) ListPages(ctx context.Context, brainID string, asFlat bool) ([]Page, error) {
	q := url.Values{}
	if asFlat {
		q.Set("as", "flat")
	}
	var out []Page
	if err := c.GetQuery(ctx, fmt.Sprintf("/api/v2/brains/%s/pages", url.PathEscape(brainID)), q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetPage(ctx context.Context, pageID string) (*Page, error) {
	var out Page
	if err := c.Get(ctx, fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPageBySlug resolves a page by its slug within a brain. brainIDOrSlug can be
// either a brain UUID or slug; slug is the page slug.
func (c *Client) GetPageBySlug(ctx context.Context, brainIDOrSlug, slug string) (*Page, error) {
	var out Page
	path := fmt.Sprintf("/api/v2/brains/%s/pages/%s", url.PathEscape(brainIDOrSlug), url.PathEscape(slug))
	if err := c.Get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type UpdatePageInput struct {
	Title        *string `json:"title,omitempty"`
	ParentPageID *string `json:"parent_page_id,omitempty"`
}

func (c *Client) UpdatePage(ctx context.Context, pageID string, input UpdatePageInput) (*Page, error) {
	var out Page
	if err := c.Patch(ctx, fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type DeletePageResult struct {
	ID        string `json:"id"`
	DeletedAt string `json:"deleted_at"`
}

func (c *Client) DeletePage(ctx context.Context, pageID string) (*DeletePageResult, error) {
	var out DeletePageResult
	if err := c.Delete(ctx, fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

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
