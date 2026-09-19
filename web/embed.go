// Package web holds the single-page app that ships inside the binary.
package web

import "embed"

// FS contains every asset served to the browser. Nothing is loaded from the
// network at runtime, so the app works on hosts without outbound access.
//
//go:embed index.html styles.css manifest.webmanifest icon.svg icon-180.png icon-192.png icon-512.png js marks
var FS embed.FS
