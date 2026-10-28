//go:build !windows

package playback

import (
	"context"
	"net"
)

// dialIPC connects to mpv's Unix socket (--input-ipc-server=/run/user/1000/arpegio-mpv.sock).
func dialIPC(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
