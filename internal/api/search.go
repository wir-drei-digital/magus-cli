package api

import (
	"context"
	"fmt"
	"net/url"
)

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

func (c *Client) Search(ctx context.Context, brainID string, input SearchInput) ([]SearchHit, error) {
	var out []SearchHit
	if err := c.Post(ctx, fmt.Sprintf("/api/v2/brains/%s/search", url.PathEscape(brainID)), input, &out); err != nil {
		return nil, err
	}
	return out, nil
}
