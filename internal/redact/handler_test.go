package redact

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestHandlerRedactsMessageAndAttrs(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		// Remove time to make assertions simpler.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	logger := slog.New(NewHandler(inner))

	logger.Info("connected to 192.168.1.5:9000", "addr", "10.0.0.1:3000")

	out := buf.String()
	if strings.Contains(out, "192.168.1.5") {
		t.Errorf("message IP not redacted: %s", out)
	}
	if strings.Contains(out, "10.0.0.1") {
		t.Errorf("attr IP not redacted: %s", out)
	}
	if !strings.Contains(out, placeholder) {
		t.Errorf("expected %s in output: %s", placeholder, out)
	}
}

func TestHandlerDisabled(t *testing.T) {
	Enabled = false

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	logger := slog.New(NewHandler(inner))

	logger.Info("connected to 192.168.1.5:9000", "addr", "10.0.0.1:3000")

	out := buf.String()
	if !strings.Contains(out, "192.168.1.5") {
		t.Errorf("expected raw IP when disabled: %s", out)
	}
	if !strings.Contains(out, "10.0.0.1") {
		t.Errorf("expected raw attr IP when disabled: %s", out)
	}
}

func TestHandlerNonStringAttrs(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	logger := slog.New(NewHandler(inner))

	logger.Info("request", "status", 200, "duration", 5*time.Millisecond)

	out := buf.String()
	if !strings.Contains(out, "200") {
		t.Errorf("expected int attr preserved: %s", out)
	}
	if !strings.Contains(out, "5ms") {
		t.Errorf("expected duration attr preserved: %s", out)
	}
}

func TestHandlerWithAttrs(t *testing.T) {
	Enabled = true
	defer func() { Enabled = false }()

	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	handler := NewHandler(inner).WithAttrs([]slog.Attr{
		slog.String("addr", "192.168.1.1:7000"),
	})
	logger := slog.New(handler)

	logger.Info("test message")

	out := buf.String()
	if strings.Contains(out, "192.168.1.1") {
		t.Errorf("WithAttrs IP not redacted: %s", out)
	}
}
