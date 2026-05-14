package api

import (
	"fmt"
	"net/url"
)

type Block struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	Content         map[string]any `json:"content"`
	Position        float64        `json:"position"`
	Depth           int            `json:"depth"`
	ParentBlockID   *string        `json:"parent_block_id"`
	ContributorType string         `json:"contributor_type"`
	ContributorID   string         `json:"contributor_id"`
	UpdatedAt       string         `json:"updated_at"`
}

type AddBlockInput struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Level    int    `json:"level,omitempty"`
	Language string `json:"language,omitempty"`
	Variant  string `json:"variant,omitempty"`
}

func (c *Client) AddBlock(pageID string, input AddBlockInput) (*Block, error) {
	var out Block
	if err := c.Post(fmt.Sprintf("/api/v2/pages/%s/blocks", url.PathEscape(pageID)), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetBlock(id string) (*Block, error) {
	var out Block
	if err := c.Get("/api/v2/blocks/"+url.PathEscape(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type UpdateBlockContent struct {
	Content map[string]any `json:"content"`
}

type UpdateBlockReplace struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (c *Client) UpdateBlockContent(id string, content map[string]any) (*Block, error) {
	var out Block
	if err := c.Patch("/api/v2/blocks/"+url.PathEscape(id), UpdateBlockContent{Content: content}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ReplaceBlockText(id, oldText, newText string, replaceAll bool) (*Block, error) {
	var out Block
	input := UpdateBlockReplace{OldText: oldText, NewText: newText, ReplaceAll: replaceAll}
	if err := c.Patch("/api/v2/blocks/"+url.PathEscape(id), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteBlock(id string) error {
	return c.Delete("/api/v2/blocks/"+url.PathEscape(id), nil)
}
