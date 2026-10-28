package playback

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// dialIPC connects to mpv's named pipe (--input-ipc-server=\.\pipe\name).
func dialIPC(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}
