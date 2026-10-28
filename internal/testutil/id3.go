// Package testutil builds tiny tagged audio files so tests need no binary fixtures.
package testutil

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// ID3 returns an ID3v2.3 blob with the given text frames (e.g. "TIT2": "Song")
// followed by a few filler bytes standing in for audio.
func ID3(frames map[string]string, filler string) []byte {
	var body []byte
	for id, text := range frames {
		payload := utf16Text(text)
		hdr := make([]byte, 10)
		copy(hdr, id)
		binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
		body = append(body, hdr...)
		body = append(body, payload...)
	}
	n := len(body)
	head := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	return append(append(head, body...), filler...)
}

// WriteMP3 writes a tagged fake mp3 at dir/rel (creating folders) and returns its path.
func WriteMP3(t testing.TB, dir, rel string, frames map[string]string, filler string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, ID3(frames, filler), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// utf16Text encodes text as an ID3v2.3 UTF-16 (BOM, little endian) frame payload.
func utf16Text(s string) []byte {
	out := []byte{1, 0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}
