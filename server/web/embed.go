// Package web is the site the server hands a browser.
//
// It lives here rather than on a static host so that one deployment is the
// whole product: a self-hosted instance explains itself at its own address, and
// an invite link and the page it opens share a domain.
package web

import "embed"

// Files is the site, rooted at index.html.
//
//go:embed index.html styles.css assets
var Files embed.FS
