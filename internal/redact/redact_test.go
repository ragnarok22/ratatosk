package redact

import "testing"

func TestStringRedactsIPv4(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("connected to 192.168.1.1 successfully")
	want := "connected to [REDACTED] successfully"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsIPv4WithPort(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("server at 10.0.0.1:3000")
	want := "server at [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsLocalhostPort(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("forwarding to localhost:7000")
	want := "forwarding to localhost:[REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsIPv6(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("listening on [::1]:8080")
	want := "listening on [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsBearerToken(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("Authorization: Bearer abc123xyz")
	want := "Authorization: Bearer [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsBearerCaseInsensitive(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := String("header: bearer secret-token")
	want := "header: bearer [REDACTED]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStringRedactsFilePaths(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	tests := []struct {
		input string
		want  string
	}{
		{"/Users/ragnarok/project/main.go", "[REDACTED]"},
		{"/home/user/app/config.yaml", "[REDACTED]"},
		{"/root/.ssh/id_rsa", "[REDACTED]"},
	}
	for _, tt := range tests {
		got := String(tt.input)
		if got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStringPreservesCleanText(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	input := "hello world, everything is fine"
	got := String(input)
	if got != input {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestStringDisabled(t *testing.T) {
	Enabled = false

	input := "secret at 192.168.1.1:3000 with Bearer token123"
	got := String(input)
	if got != input {
		t.Errorf("expected passthrough when disabled, got %q", got)
	}
}

func TestHeadersRedactsSensitive(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	h := map[string]string{
		"Authorization": "Bearer secret",
		"Content-Type":  "application/json",
		"Cookie":        "session=abc123",
	}
	got := Headers(h)

	if got["Authorization"] != placeholder {
		t.Errorf("Authorization = %q, want %q", got["Authorization"], placeholder)
	}
	if got["Cookie"] != placeholder {
		t.Errorf("Cookie = %q, want %q", got["Cookie"], placeholder)
	}
	if got["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got["Content-Type"], "application/json")
	}
}

func TestHeadersCaseInsensitive(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	h := map[string]string{
		"authorization": "token",
		"X-API-KEY":     "key123",
		"x-real-ip":     "1.2.3.4",
	}
	got := Headers(h)

	for k := range h {
		if got[k] != placeholder {
			t.Errorf("%s = %q, want %q", k, got[k], placeholder)
		}
	}
}

func TestHeadersDisabled(t *testing.T) {
	Enabled = false

	h := map[string]string{
		"Authorization": "Bearer secret",
	}
	got := Headers(h)
	if got["Authorization"] != "Bearer secret" {
		t.Errorf("expected passthrough when disabled, got %q", got["Authorization"])
	}
}

func TestHeadersNilMap(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	got := Headers(nil)
	if got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}
