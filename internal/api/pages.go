package api

import (
	"fmt"
	"net/url"
)

type Page struct {
	ID           string  `json:"id"`
	Slug         string  `json:"slug"`
	Title        string  `json:"title"`
	BrainID      string  `json:"brain_id"`
	ParentPageID *string `json:"parent_page_id"`
	Depth        int     `json:"depth"`
	UpdatedAt    string  `json:"updated_at"`
	Markdown     string  `json:"markdown,omitempty"`
	BlocksAdded  int     `json:"blocks_added,omitempty"`
	Blocks       []Block `json:"blocks,omitempty"`
	Children     []Page  `json:"children,omitempty"`
	DeletedAt    string  `json:"deleted_at,omitempty"`
}

func (c *Client) ListPages(brainID string, asFlat bool) ([]Page, error) {
	q := url.Values{}
	if asFlat {
		q.Set("as", "flat")
	}
	var out []Page
	if err := c.GetQuery(fmt.Sprintf("/api/v2/brains/%s/pages", url.PathEscape(brainID)), q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type WritePageInput struct {
	Title        string `json:"title"`
	Content      string `json:"content,omitempty"`
	ParentPageID string `json:"parent_page_id,omitempty"`
	Mode         string `json:"mode,omitempty"`
}

func (c *Client) WritePage(brainID string, input WritePageInput) (*Page, error) {
	var out Page
	if err := c.Post(fmt.Sprintf("/api/v2/brains/%s/pages", url.PathEscape(brainID)), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetPage(pageID, format string) (*Page, error) {
	q := url.Values{}
	if format != "" {
		q.Set("format", format)
	}
	var out Page
	if err := c.GetQuery(fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type UpdatePageInput struct {
	Title        *string `json:"title,omitempty"`
	ParentPageID *string `json:"parent_page_id,omitempty"`
}

func (c *Client) UpdatePage(pageID string, input UpdatePageInput) (*Page, error) {
	var out Page
	if err := c.Patch(fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type DeletePageResult struct {
	ID        string `json:"id"`
	DeletedAt string `json:"deleted_at"`
}

func (c *Client) DeletePage(pageID string) (*DeletePageResult, error) {
	var out DeletePageResult
	if err := c.Delete(fmt.Sprintf("/api/v2/pages/%s", url.PathEscape(pageID)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
