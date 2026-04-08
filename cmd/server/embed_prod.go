//go:build !dev

package main

import "embed"

//go:embed dashboard/dist
var dashboardFS embed.FS
