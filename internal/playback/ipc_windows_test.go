package playback

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// TestMPVTalksOverANamedPipe runs the whole engine against a fake mpv listening on a real
// Windows named pipe, which is what `mpv --input-ipc-server=\\.\pipe\name` creates.
func TestMPVTalksOverANamedPipe(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\arpegio-test-%d`, time.Now().UnixNano())
	l, err := winio.ListenPipe(pipe, nil)
	if err != nil {
		t.Skipf("named pipes unavailable: %v", err)
	}
	defer l.Close()

	var mu sync.Mutex
	var cmds []string
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				sc := bufio.NewScanner(nc)
				for sc.Scan() {
					var m struct {
						Command   []any `json:"command"`
						RequestID int   `json:"request_id"`
					}
					if json.Unmarshal(sc.Bytes(), &m) != nil {
						continue
					}
					parts := make([]string, len(m.Command))
					for i, p := range m.Command {
						parts[i] = fmt.Sprint(p)
					}
					mu.Lock()
					cmds = append(cmds, strings.Join(parts, " "))
					mu.Unlock()
					fmt.Fprintf(nc, `{"request_id":%d,"error":"success","data":null}`+"\n", m.RequestID)
					if parts[0] == "loadfile" {
						fmt.Fprintln(nc, `{"event":"property-change","id":1,"name":"time-pos","data":1.5}`)
					}
				}
			}()
		}
	}()

	started := make(chan struct{}, 4)
	m := NewMPV(MPVOptions{
		Socket:     pipe,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		MinBackoff: 5 * time.Millisecond,
		Start: func(context.Context) (process, error) {
			started <- struct{}{}
			return &fakeProc{die: make(chan struct{})}, nil
		},
		// Dial is left unset on purpose: the default must use the named pipe.
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.After(3 * time.Second)
	for connected := false; !connected; {
		select {
		case ev := <-m.Events():
			connected = ev.Kind == EvRestarted
		case <-deadline:
			t.Fatal("never connected to the pipe")
		}
	}
	if err := m.Load(ctx, `C:\Music\a.flac`, 0, false); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-m.Events():
		if ev.Kind != EvPosition || ev.PositionMS != 1500 {
			t.Errorf("event = %+v", ev)
		}
	case <-deadline:
		t.Fatal("no position event over the pipe")
	}
	mu.Lock()
	got := strings.Join(cmds, " | ")
	mu.Unlock()
	if !strings.Contains(got, `loadfile C:\Music\a.flac replace`) || !strings.Contains(got, "observe_property 1 time-pos") {
		t.Errorf("commands = %q", got)
	}
}
