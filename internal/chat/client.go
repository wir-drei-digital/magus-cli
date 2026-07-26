// internal/chat/client.go
package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type EventKind int

const (
	KindServerHello EventKind = iota
	KindChatStream
	KindMcpCall
	KindError
	KindClosed
)

type Event struct {
	Kind        EventKind
	ServerHello ServerHello
	ChatStream  ChatStream
	McpCall     McpCall
	Err         error
}

// Client is a chat WebSocket connection. Inbound frames surface on Events();
// outbound frames are serialized through a single writer goroutine.
type Client struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	send   chan []byte
	events chan Event
}

// Dial connects to wsURL with a Bearer token. TLS verification uses Go defaults.
func Dial(ctx context.Context, wsURL, token, userAgent string) (*Client, error) {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("User-Agent", userAgent)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, fmt.Errorf("ws dial: %w", err)
	}
	conn.SetReadLimit(8 << 20)

	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:   conn,
		ctx:    cctx,
		cancel: cancel,
		send:   make(chan []byte, 16),
		events: make(chan Event, 16),
	}
	go c.writeLoop()
	go c.readLoop()
	return c, nil
}

func (c *Client) Events() <-chan Event { return c.events }

// Send marshals and enqueues an outbound frame.
func (c *Client) Send(frame any) error {
	data, err := Encode(frame)
	if err != nil {
		return err
	}
	select {
	case c.send <- data:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *Client) Close() {
	c.cancel()
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) writeLoop() {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case data := <-c.send:
			if err := c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
				c.cancel()
				return
			}
		case <-ping.C:
			_ = c.conn.Ping(c.ctx)
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.events)
	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			c.emit(Event{Kind: KindClosed, Err: err})
			c.cancel()
			return
		}
		typ, err := decodeHead(data)
		if err != nil {
			if typ != "" {
				// Envelope parsed but failed validation (protocol-version
				// mismatch): surface it rather than processing an incompatible
				// frame as v1 or silently timing out the handshake.
				c.emit(Event{Kind: KindError, Err: err})
			}
			continue // undecodable frames are dropped per spec §8
		}
		switch typ {
		case "server_hello":
			var sh ServerHello
			if decodePayload(data, &sh) == nil {
				c.emit(Event{Kind: KindServerHello, ServerHello: sh})
			}
		case "chat_stream":
			var cs ChatStream
			if decodePayload(data, &cs) == nil {
				c.emit(Event{Kind: KindChatStream, ChatStream: cs})
			}
		case "mcp_call":
			var mc McpCall
			if decodePayload(data, &mc) == nil {
				c.emit(Event{Kind: KindMcpCall, McpCall: mc})
			}
		case "error":
			var fe FrameError
			_ = decodePayload(data, &fe)
			c.emit(Event{Kind: KindError, ChatStream: ChatStream{Event: "error"}, Err: frameErr(fe)})
		}
	}
}

// frameErr turns a server-sent error frame into a non-nil error, preferring the
// "code: message" form and falling back gracefully when fields are absent.
func frameErr(fe FrameError) error {
	switch {
	case fe.Code != "" && fe.Message != "":
		return fmt.Errorf("%s: %s", fe.Code, fe.Message)
	case fe.Message != "":
		return errors.New(fe.Message)
	case fe.Code != "":
		return errors.New(fe.Code)
	default:
		return errors.New("server error")
	}
}

func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.ctx.Done():
	}
}
