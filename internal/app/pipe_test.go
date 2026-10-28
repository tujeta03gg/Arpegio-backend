package app

import "testing"

// The pipe path must be exactly what `mpv --input-ipc-server=` accepts on Windows.
func TestWindowsPipeNameIsWhatMPVExpects(t *testing.T) {
	want := string([]byte{'\\', '\\', '.', '\\'}) + "pipe" + string([]byte{'\\'}) + "arpegio-mpv"
	if windowsMPVPipe != want {
		t.Errorf("windowsMPVPipe = %q, want %q", windowsMPVPipe, want)
	}
}
