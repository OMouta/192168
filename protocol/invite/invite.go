// Package invite is the shared rules for the codes that let somebody into a
// group.
//
// A code is what you send a friend. It is short enough to read out and long
// enough that guessing one is pointless, and it stands in for the group name
// and password a person used to have to be told.
package invite

import "strings"

// Length is how many characters a code has. Forty bits of randomness, which is
// far more than a rate-limited guess can work through, and still short enough
// to say over a call.
const Length = 8

// Normalize is how a code is compared. Somebody pasting one will bring
// whatever it was wrapped in with it, so case, spaces, and punctuation are all
// thrown away rather than turned into a failure.
func Normalize(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Parse pulls the code out of whatever was pasted, which is either a code or
// the link one was sent in.
//
// A link is read from the end, so it does not matter which server it points at
// or how the path in front of the code is spelled.
func Parse(input string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(input), "/")
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	// A link may carry a query or a fragment, and neither is part of the code.
	trimmed, _, _ = strings.Cut(trimmed, "?")
	trimmed, _, _ = strings.Cut(trimmed, "#")
	return Normalize(trimmed)
}
