// Package invite is the shared shape of the codes that get somebody into a
// group. The server mints them, the daemon parses them, and both compare them
// the same way.
package invite

import "strings"

// Length is how many characters a code has. Eight is 40 bits, and short enough
// to read out.
const Length = 8

// Normalize is how a code is compared. Case and punctuation are dropped rather
// than rejected, since a pasted code arrives with whatever wrapped it.
func Normalize(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Parse takes the code out of a bare code or the link it arrived in. Links are
// read from the end, so the server and the path in front do not matter.
func Parse(input string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(input), "/")
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	// A query or a fragment is not part of the code.
	trimmed, _, _ = strings.Cut(trimmed, "?")
	trimmed, _, _ = strings.Cut(trimmed, "#")
	return Normalize(trimmed)
}

// Path is where a server serves invite links, and the path half of the app's
// URI scheme.
const Path = "/join/"

// Link is base + code, the address to send somebody. The base comes from the
// server's discovery document.
func Link(base, code string) string {
	if base == "" || code == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + code
}

// Scheme is the URI scheme setup registers the Windows app for.
const Scheme = "192168"

// DeepLink is the address that opens the app on a code.
func DeepLink(code string) string {
	if code == "" {
		return ""
	}
	return Scheme + ":/" + Path + code
}
