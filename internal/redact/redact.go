package redact

import (
	"regexp"
	"strings"
)

// Enabled controls whether redaction is active. Set once at startup
// before any goroutines are launched.
var Enabled bool

const placeholder = "[REDACTED]"

var (
	ipv4Re          = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d{1,5})?\b`)
	localhostPortRe = regexp.MustCompile(`localhost:\d{1,5}`)
	ipv6Re          = regexp.MustCompile(`\[([0-9a-fA-F]*:){2,}[0-9a-fA-F]*\](:\d{1,5})?`)
	bearerRe        = regexp.MustCompile(`(?i)(Bearer\s+)\S+`)
	filePathRe      = regexp.MustCompile(`(/(?:Users|home|root)/\S+)`)
)

var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"x-forwarded-for":     true,
	"x-real-ip":           true,
}

// String redacts sensitive patterns from an arbitrary string.
// Returns the input unchanged if Enabled is false.
func String(s string) string {
	if !Enabled {
		return s
	}
	s = ipv4Re.ReplaceAllString(s, placeholder)
	s = localhostPortRe.ReplaceAllString(s, "localhost:"+placeholder)
	s = ipv6Re.ReplaceAllString(s, placeholder)
	s = bearerRe.ReplaceAllString(s, "${1}"+placeholder)
	s = filePathRe.ReplaceAllString(s, placeholder)
	return s
}

// Headers returns a copy of the header map with sensitive header values redacted.
// Returns the input unchanged if Enabled is false.
func Headers(h map[string]string) map[string]string {
	if !Enabled || h == nil {
		return h
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = placeholder
		} else {
			out[k] = v
		}
	}
	return out
}
