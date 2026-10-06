// Package safe cleans text that comes from outside stackk (agent messages, transcripts, window
// names) before it is stored or drawn: an agent that read a hostile repo or web page could
// otherwise make stackk print terminal control sequences (change titles, write the clipboard
// with OSC 52, move the cursor to fake the dashboard) or reorder text with bidi overrides.
package safe

import "strings"

// Text removes control characters (C0, DEL, C1, which covers ESC and every escape sequence's
// introducer) and invisible direction overrides. Tabs and newlines become spaces.
func Text(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// dropped
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
			// bidi embeddings, overrides, isolates and marks: dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
