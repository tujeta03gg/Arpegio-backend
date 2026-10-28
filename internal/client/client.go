// Package client is a small HTTP client for the Arpegio API, shared by the CLI and TUI.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const prefix = "/api/v1"

type Client struct {
	base string
	http *http.Client
}

// New accepts "http://host:port" or "unix:///path/to/arpegio.sock".
func New(addr string) *Client {
	c := &Client{base: addr, http: &http.Client{Timeout: 30 * time.Second}}
	if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
		c.base = "http://arpegio"
		c.http.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		}
	}
	return c
}

// Error is a non-2xx answer from the daemon.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, e.Message) }

// Do sends in as JSON (if non-nil) and decodes the answer into out (if non-nil).
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return fmt.Errorf("client: encode request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+prefix+path, &body)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: is arpegiod running? %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		e := &Error{Status: res.StatusCode, Code: "http", Message: res.Status}
		var b struct {
			Error struct{ Code, Message string }
		}
		if json.NewDecoder(res.Body).Decode(&b) == nil && b.Error.Code != "" {
			e.Code, e.Message = b.Error.Code, b.Error.Message
		}
		return e
	}
	if out == nil || res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusAccepted {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("client: decode response: %w", err)
	}
	return nil
}

// Event is one WebSocket frame from /events.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	At   string          `json:"at"`
}

// Subscribe opens the event stream. The channel closes when ctx ends or the connection drops.
func (c *Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	hc := *c.http
	hc.Timeout = 0 // the websocket library wants cancellation through ctx, not a client timeout
	url := "ws" + strings.TrimPrefix(c.base, "http") + prefix + "/events"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: &hc})
	if err != nil {
		return nil, fmt.Errorf("client: is arpegiod running? %w", err)
	}
	conn.SetReadLimit(1 << 20)
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		defer conn.CloseNow()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var ev Event
			if json.Unmarshal(data, &ev) != nil {
				continue
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Upload sends files as one multipart form (field "files") and decodes the answer into out.
func (c *Client) Upload(ctx context.Context, path string, files []string, out any) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() { // stream from disk so big albums never sit in memory
		err := func() error {
			for _, name := range files {
				f, err := os.Open(name)
				if err != nil {
					return err
				}
				part, err := mw.CreateFormFile("files", filepath.Base(name))
				if err == nil {
					_, err = io.Copy(part, f)
				}
				f.Close()
				if err != nil {
					return err
				}
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", c.base+prefix+path, pr)
	if err != nil {
		return fmt.Errorf("client: build upload: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	hc := *c.http
	hc.Timeout = 0 // uploads can take a while; ctx still cancels
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("client: upload: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		e := &Error{Status: res.StatusCode, Code: "http", Message: res.Status}
		var b struct {
			Error struct{ Code, Message string }
		}
		if json.NewDecoder(res.Body).Decode(&b) == nil && b.Error.Code != "" {
			e.Code, e.Message = b.Error.Code, b.Error.Message
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("client: decode response: %w", err)
	}
	return nil
}
