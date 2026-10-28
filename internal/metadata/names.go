package metadata

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// CleanName trims and collapses internal whitespace.
func CleanName(s string) string { return strings.Join(strings.Fields(s), " ") }

var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// SortKey is the normalized key artists are matched by: case-, accent- and
// leading "The"-insensitive ("The Beatles" and "beatles" are the same artist).
func SortKey(name string) string {
	s := strings.ToLower(CleanName(name))
	if t, _, err := transform.String(stripMarks, s); err == nil {
		s = t
	}
	if rest, ok := strings.CutPrefix(s, "the "); ok {
		s = rest
	}
	return s
}

// SplitArtists turns a raw artist tag into main and featured artists.
// ";" separates main artists; "feat."/"ft."/"featuring" introduce featured ones.
// "&" is not a separator because it usually names a single act.
func SplitArtists(raw string) (main, featured []string) {
	raw = CleanName(raw)
	lower := strings.ToLower(raw)
	cut := len(raw)
	for _, sep := range []string{" feat. ", " feat ", " ft. ", " featuring "} {
		if i := strings.Index(lower, sep); i >= 0 && i < cut {
			cut = i
			featured = splitList(raw[i+len(sep):])
		}
	}
	return splitList(raw[:cut]), featured
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = CleanName(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
