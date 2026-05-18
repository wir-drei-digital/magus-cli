package api

import (
	"context"
	"fmt"
	"net/url"
)

type Brain struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	IsArchived  bool   `json:"is_archived"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}

type ListBrainsOpts struct {
	Cursor string
	Limit  int
}

func (c *Client) ListBrains(ctx context.Context, opts ListBrainsOpts) ([]Brain, error) {
	q := url.Values{}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	var out []Brain
	if err := c.GetQuery(ctx, "/api/v2/brains", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type CreateBrainInput struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
}

func (c *Client) CreateBrain(ctx context.Context, input CreateBrainInput) (*Brain, error) {
	var out Brain
	if err := c.Post(ctx, "/api/v2/brains", input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetBrain(ctx context.Context, idOrSlug string) (*Brain, error) {
	var out Brain
	if err := c.Get(ctx, "/api/v2/brains/"+url.PathEscape(idOrSlug), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type UpdateBrainInput struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	Color       *string `json:"color,omitempty"`
}

func (c *Client) UpdateBrain(ctx context.Context, idOrSlug string, input UpdateBrainInput) (*Brain, error) {
	var out Brain
	if err := c.Patch(ctx, "/api/v2/brains/"+url.PathEscape(idOrSlug), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ArchiveBrain(ctx context.Context, idOrSlug string) error {
	return c.Delete(ctx, "/api/v2/brains/"+url.PathEscape(idOrSlug), nil)
}
