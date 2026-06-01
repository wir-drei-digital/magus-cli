package api

import (
	"context"
	"fmt"
	"net/url"
)

type SearchHit struct {
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	PageID     string  `json:"page_id"`
	PageTitle  string  `json:"page_title,omitempty"`
	BrainID    string  `json:"brain_id,omitempty"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
	PageNumber int     `json:"page_number,omitempty"`
	Rank       float64 `json:"rank,omitempty"`
	SourceID   string  `json:"source_id,omitempty"`
	FileID     string  `json:"file_id,omitempty"`
	Title      string  `json:"title,omitempty"`
}

type SearchInput struct {
	Query      string `json:"query"`
	Mode       string `json:"mode,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	PageID     string `json:"page,omitempty"`
	Kind       string `json:"kind,omitempty"`
	CrossBrain bool   `json:"cross_brain,omitempty"`
}

func (c *Client) Search(ctx context.Context, brainID string, input SearchInput) ([]SearchHit, error) {
	var out []SearchHit
	if err := c.Post(ctx, fmt.Sprintf("/api/v2/brains/%s/search", url.PathEscape(brainID)), input, &out); err != nil {
		return nil, err
	}
	return out, nil
}
