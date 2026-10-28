package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDoSendsJSONAndDecodes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/x" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		var in map[string]string
		_ = decodeBody(r, &in)
		w.Write([]byte(`{"echo":"` + in["a"] + `"}`))
	}))
	defer ts.Close()

	var out struct{ Echo string }
	if err := New(ts.URL).Do(context.Background(), "POST", "/x", map[string]string{"a": "hi"}, &out); err != nil || out.Echo != "hi" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDoMapsErrorBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/plain" {
			http.Error(w, "boom", 500)
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"code":"not_found","message":"no such track"}}`))
	}))
	defer ts.Close()
	c := New(ts.URL)

	var e *Error
	err := c.Do(context.Background(), "GET", "/tracks/1", nil, nil)
	if !errors.As(err, &e) || e.Status != 404 || e.Code != "not_found" || e.Message != "no such track" {
		t.Fatalf("err = %#v", err)
	}
	err = c.Do(context.Background(), "GET", "/plain", nil, nil)
	if !errors.As(err, &e) || e.Status != 500 || e.Code != "http" {
		t.Fatalf("plain err = %#v", err)
	}
}

func TestDoNoContentAndUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	var out struct{}
	if err := New(ts.URL).Do(context.Background(), "DELETE", "/x", nil, &out); err != nil {
		t.Errorf("204: %v", err)
	}
	ts.Close()
	if err := New(ts.URL).Do(context.Background(), "GET", "/x", nil, nil); err == nil {
		t.Error("closed server should fail")
	}
}

func TestUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "a.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) })}
	go srv.Serve(l)
	defer srv.Close()
	defer os.Remove(sock)

	var out struct{ OK bool }
	if err := New("unix://"+sock).Do(context.Background(), "GET", "/ping", nil, &out); err != nil || !out.OK {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func decodeBody(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func TestSubscribeDeliversFramesAndClosesWithContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events" {
			t.Errorf("path = %s", r.URL.Path)
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		c.Write(ctx, websocket.MessageText, []byte(`{"type":"snapshot","data":{"a":1},"at":"t0"}`))
		c.Write(ctx, websocket.MessageText, []byte(`not json`))
		c.Write(ctx, websocket.MessageText, []byte(`{"type":"queue.changed","at":"t1"}`))
		<-ctx.Done()
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := New(ts.URL).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, second := <-ch, <-ch // the malformed frame is skipped
	if first.Type != "snapshot" || string(first.Data) != `{"a":1}` || second.Type != "queue.changed" {
		t.Fatalf("events = %+v %+v", first, second)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("unexpected extra event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after cancel")
	}
}

func TestSubscribeFailsWhenDaemonIsDown(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()
	if _, err := New(ts.URL).Subscribe(context.Background()); err == nil {
		t.Error("expected an error")
	}
}
