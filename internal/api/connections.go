package api

import "net/url"

type Connection struct {
	ID              string `json:"id"`
	SourceBlockID   string `json:"source_block_id,omitempty"`
	TargetBlockID   string `json:"target_block_id,omitempty"`
	TargetPageID    string `json:"target_page_id,omitempty"`
	SourcePageID    string `json:"source_page_id,omitempty"`
	Type            string `json:"type"`
	ContributorType string `json:"contributor_type"`
	ContributorID   string `json:"contributor_id,omitempty"`
}

type CreateConnectionBlockLevel struct {
	SourceBlockID string `json:"source_block_id"`
	TargetBlockID string `json:"target_block_id,omitempty"`
	TargetPageID  string `json:"target_page_id,omitempty"`
	Type          string `json:"type"`
}

type CreateConnectionPageLevel struct {
	SourcePageID string `json:"source_page_id"`
	TargetPageID string `json:"target_page_id"`
	Type         string `json:"type,omitempty"`
}

func (c *Client) CreateBlockConnection(input CreateConnectionBlockLevel) (*Connection, error) {
	var out Connection
	if err := c.Post("/api/v2/connections", input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreatePageConnection(input CreateConnectionPageLevel) ([]Connection, error) {
	var out []Connection
	if err := c.Post("/api/v2/connections", input, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) DeleteConnection(id string) error {
	return c.Delete("/api/v2/connections/"+url.PathEscape(id), nil)
}
