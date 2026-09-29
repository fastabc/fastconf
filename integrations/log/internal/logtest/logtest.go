// Package logtest holds slog behavior cases shared by the backend adapters.
package logtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// GroupCases checks that attrs keep the group scope active when they were added. newHandler
// returns a handler writing one JSON object per record to buf.
func GroupCases(t *testing.T, newHandler func(buf *bytes.Buffer) slog.Handler) {
	for _, tc := range []struct {
		name string
		log  func(*slog.Logger)
		want map[string]any
		deny []string
	}{
		{"with before group", func(l *slog.Logger) {
			l.With("outer", 1).WithGroup("g").Info("probe", "inner", 2)
		}, map[string]any{"outer": 1.0, "g.inner": 2.0}, []string{"g.outer", "inner"}},
		{"with inside nested groups", func(l *slog.Logger) {
			l.WithGroup("a").With("x", 1).WithGroup("b").With("y", 2).Info("probe", "z", 3)
		}, map[string]any{"a.x": 1.0, "a.b.y": 2.0, "a.b.z": 3.0}, []string{"a.b.x"}},
		{"empty group is ignored", func(l *slog.Logger) {
			l.WithGroup("").With("k", 1).Info("probe")
		}, map[string]any{"k": 1.0}, nil},
		{"empty-key group attr is inlined", func(l *slog.Logger) {
			l.WithGroup("g").Info("probe", slog.Group("", slog.Int("k", 1)))
		}, map[string]any{"g.k": 1.0}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.log(slog.New(newHandler(&buf)))
			var got map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
				t.Fatalf("json: %v; raw=%s", err, buf.String())
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v (full=%v)", k, got[k], v, got)
				}
			}
			for _, k := range tc.deny {
				if _, ok := got[k]; ok {
					t.Errorf("unexpected key %s (full=%v)", k, got)
				}
			}
		})
	}
}

// HandlerCases verifies shared slog behavior against each real backend.
func HandlerCases(t *testing.T, newHandler func(*bytes.Buffer) slog.Handler) {
	t.Helper()
	for _, tc := range []struct {
		name string
		log  func(*slog.Logger)
		want map[string]any
	}{
		{"structured fields", func(l *slog.Logger) { l.Info("reload swap", "reason", "watcher", "generation", int64(7)) }, map[string]any{"level": "info", "message": "reload swap", "reason": "watcher", "generation": float64(7)}},
		{"attrs accumulate", func(l *slog.Logger) { l.With("tenant", "acme").With("generation", 3).Info("ok") }, map[string]any{"tenant": "acme", "generation": float64(3)}},
		{"error attr", func(l *slog.Logger) { l.Error("boom", "err", errors.New("disk full")) }, map[string]any{"err": "disk full"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.log(slog.New(newHandler(&buf)))
			var got map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
				t.Fatalf("decode log: %v; raw=%s", err, buf.String())
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s=%v; want %v", key, got[key], want)
				}
			}
		})
	}
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		t.Run(level.String(), func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(newHandler(&buf)).Log(context.Background(), level, "message")
			var got map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
				t.Fatal(err)
			}
			if want := strings.ToLower(level.String()); got["level"] != want {
				t.Fatalf("level=%v; want %q", got["level"], want)
			}
		})
	}
	GroupCases(t, newHandler)
}
