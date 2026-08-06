// internal/chat/client.go
package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
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

	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, dialErr(resp, err)
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

// dialErr explains the two handshake rejections a user can actually fix. The
// transport's own message stops at "got 403", which says nothing about which
// token is wrong or what to do about it.
func dialErr(resp *http.Response, err error) error {
	if resp != nil {
		switch resp.StatusCode {
		case http.StatusForbidden:
			return fmt.Errorf("ws dial: this token lacks write scope (insufficient_scope); chat needs a write-scoped token — run `magus login` again: %w", err)
		case http.StatusUnauthorized:
			return fmt.Errorf("ws dial: this token is invalid or expired; run `magus login`: %w", err)
		}
	}
	return fmt.Errorf("ws dial: %w", err)
}

// Send marshals and enqueues an outbound frame.
//
// mcp_result frames pass through FitMcpResult first: the server caps inbound
// frames at 1MB and CLOSES the connection on oversize, so the budget is enforced
// here — the one choke point every front-end shares — instead of being trusted
// to each producer (the ACP bridge forwards editor reads it never sized).
func (c *Client) Send(frame any) error {
	if res, ok := frame.(McpResult); ok {
		frame = FitMcpResult(res)
	}
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

// IsExpectedClose reports whether err is an ordinary end of connection — a
// clean close handshake, EOF, or a Close this side initiated — as opposed to a
// transport failure whose reason is worth putting in front of the user. Callers
// get the classification from here because this is the only package that knows
// what shape the underlying transport's errors take.
func IsExpectedClose(err error) bool {
	if err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return true
	}
	return false
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
