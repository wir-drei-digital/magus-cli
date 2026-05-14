package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks the Magus /api/v2 protocol with a Bearer token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a client. baseURL is e.g. "https://magus.digital".
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Get decodes the "data" field of the JSON response into `out`.
func (c *Client) Get(path string, out any) error {
	return c.do(http.MethodGet, path, nil, out)
}

// GetQuery is like Get but appends url.Values as a query string.
func (c *Client) GetQuery(path string, query url.Values, out any) error {
	if len(query) > 0 {
		path = path + "?" + query.Encode()
	}
	return c.do(http.MethodGet, path, nil, out)
}

func (c *Client) Post(path string, body any, out any) error {
	return c.do(http.MethodPost, path, body, out)
}

func (c *Client) Patch(path string, body any, out any) error {
	return c.do(http.MethodPatch, path, body, out)
}

func (c *Client) Delete(path string, out any) error {
	return c.do(http.MethodDelete, path, nil, out)
}

func (c *Client) do(method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		reqBody = buf
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "magus-cli/dev")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return decodeError(resp.StatusCode, respBody)
	}

	if out == nil {
		return nil
	}

	envelope := struct {
		Data       json.RawMessage `json:"data"`
		NextCursor string          `json:"next_cursor,omitempty"`
	}{}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if len(envelope.Data) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func decodeError(status int, body []byte) error {
	envelope := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details"`
		} `json:"error"`
	}{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &Error{Status: status, Code: "unknown", Message: string(body)}
	}
	return &Error{
		Status:  status,
		Code:    envelope.Error.Code,
		Message: envelope.Error.Message,
		Details: envelope.Error.Details,
	}
}
