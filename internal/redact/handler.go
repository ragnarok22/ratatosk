package redact

import (
	"context"
	"fmt"
	"log/slog"
)

// Handler wraps an slog.Handler and redacts sensitive data from log
// attributes when Enabled is true.
type Handler struct {
	inner slog.Handler
}

// NewHandler returns a Handler that wraps inner.
func NewHandler(inner slog.Handler) *Handler {
	return &Handler{inner: inner}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if !Enabled {
		return h.inner.Handle(ctx, r)
	}
	r2 := slog.NewRecord(r.Time, r.Level, String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		r2.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, r2)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if !Enabled {
		return &Handler{inner: h.inner.WithAttrs(attrs)}
	}
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &Handler{inner: h.inner.WithAttrs(redacted)}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	value := a.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, String(value.String()))
	case slog.KindGroup:
		attrs := value.Group()
		for i := range attrs {
			attrs[i] = redactAttr(attrs[i])
		}
		return slog.Group(a.Key, attrsToAny(attrs)...)
	case slog.KindAny:
		switch item := value.Any().(type) {
		case error:
			return slog.String(a.Key, String(item.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, String(item.String()))
		}
	}
	return slog.Attr{Key: a.Key, Value: value}
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}
