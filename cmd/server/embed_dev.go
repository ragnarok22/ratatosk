//go:build dev

package main

import "embed"

// Empty filesystem — dashboard served by Vite dev server in development.
var dashboardFS embed.FS
