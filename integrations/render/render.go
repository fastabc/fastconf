// Package render writes configuration for file-based daemons. Wire subscribes
// to snapshots, renders into a temporary file, atomically renames it, then runs
// change hooks. Rendering or write failures preserve the previous destination.
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// Renderer converts a typed configuration snapshot to file contents.
type Renderer[T any] interface {
	Render(value *T) ([]byte, error)
}

// RendererFunc adapts a free function to the Renderer interface.
type RendererFunc[T any] func(*T) ([]byte, error)

// Render implements Renderer.
func (f RendererFunc[T]) Render(v *T) ([]byte, error) { return f(v) }

// Hook runs after a successful atomic write. Hooks should be fast and
// non-fatal; an error is logged but does not roll back the write.
type Hook func(ctx context.Context, outPath string) error

// Options configures Wire. The zero value is valid: temp files are
// created next to the destination, the hook context defaults to
// context.Background(), and write mode falls back to 0o644.
type Options struct {
	// FileMode for the rendered file (default 0o644).
	FileMode os.FileMode
	// HookTimeout caps each hook invocation (default 5s).
	HookTimeout time.Duration
	// OnError, when non-nil, receives every render/write/hook failure
	// in addition to the slog logger (the manager's default).
	OnError func(err error)
	// SkipFirstHook, when true, writes the file once on Wire() but does
	// NOT fire hooks for that initial render. Use when downstream
	// daemons are not yet up at boot and a SIGHUP would race with their
	// own startup.
	SkipFirstHook bool
}

func (o *Options) applyDefaults() {
	if o.FileMode == 0 {
		o.FileMode = 0o644
	}
	if o.HookTimeout == 0 {
		o.HookTimeout = 5 * time.Second
	}
}

// Wire subscribes to mgr's typed snapshot stream, atomically writes the
// rendered bytes to outPath, then runs every hook in order. The returned
// cancel removes the subscription; the renderer fires once eagerly for
// the current snapshot so the file exists before the function returns.
func Wire[T any](
	mgr *fastconf.Manager[T],
	r Renderer[T],
	outPath string,
	opts Options,
	hooks ...Hook,
) (cancel func(), err error) {
	if mgr == nil {
		return nil, errors.New("render: nil manager")
	}
	if r == nil {
		return nil, errors.New("render: nil renderer")
	}
	if strings.TrimSpace(outPath) == "" {
		return nil, errors.New("render: empty outPath")
	}
	opts.applyDefaults()

	// mu serializes render, write and hooks so a slow initial render cannot
	// land after a newer reload; lastGen drops snapshots older than the one
	// already handled. lastBytes is only trusted once written is set.
	var (
		mu        sync.Mutex
		lastGen   uint64
		lastBytes []byte
		written   bool
	)
	apply := func(snap *fastconf.State[T], initial bool) {
		mu.Lock()
		defer mu.Unlock()
		if snap == nil || snap.Value() == nil || (lastGen != 0 && snap.Generation() <= lastGen) {
			return
		}
		lastGen = snap.Generation()
		buf, rerr := r.Render(snap.Value())
		if rerr != nil {
			opts.report(fmt.Errorf("render: %w", rerr))
			return
		}
		if written && bytes.Equal(buf, lastBytes) {
			return
		}
		if werr := atomicWrite(outPath, buf, opts.FileMode); werr != nil {
			opts.report(fmt.Errorf("render: write %s: %w", outPath, werr))
			return
		}
		// Copy: renderers may reuse the returned buffer.
		lastBytes, written = bytes.Clone(buf), true
		if initial && opts.SkipFirstHook {
			return
		}
		ctx, ccancel := context.WithTimeout(context.Background(), opts.HookTimeout)
		defer ccancel()
		for _, h := range hooks {
			if h == nil {
				continue
			}
			if herr := h(ctx, outPath); herr != nil {
				opts.report(fmt.Errorf("render: hook: %w", herr))
			}
		}
	}

	// The callback only signals a change; the current snapshot carries the
	// generation that orders it against the eager first render.
	cancel = fastconf.Subscribe(mgr, func(t *T) *T { return t }, func(_, _ *T) {
		apply(mgr.Snapshot(), false)
	})
	apply(mgr.Snapshot(), true)
	return cancel, nil
}

func (o *Options) report(err error) {
	if o.OnError != nil {
		o.OnError(err)
	}
}

// atomicWrite uses a same-directory temporary file and rename so readers see
// only the complete old or new file, never a partial write.
func atomicWrite(outPath string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".fastconf-render-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// GoTemplate returns a Renderer that loads the named text/template file
// once and re-executes it on every snapshot. The template's data is the
// dereferenced *T (so users write `{{ .Server.Port }}`), and FuncMap can
// inject helpers (e.g. quote, indent). Pass nil FuncMap for the defaults.
func GoTemplate[T any](tmplPath string, funcs template.FuncMap) (Renderer[T], error) {
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("render: read template %s: %w", tmplPath, err)
	}
	tpl := template.New(filepath.Base(tmplPath))
	if funcs != nil {
		tpl = tpl.Funcs(funcs)
	}
	if _, err := tpl.Parse(string(raw)); err != nil {
		return nil, fmt.Errorf("render: parse template %s: %w", tmplPath, err)
	}
	return RendererFunc[T](func(v *T) ([]byte, error) {
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, v); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}), nil
}

// SignalProcess returns a Hook that reads a PID from pidFile and sends
// the signal to it. Useful for "kill -HUP nginx" style reloads.
func SignalProcess(pidFile string, sig os.Signal) Hook {
	return func(ctx context.Context, _ string) error {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return fmt.Errorf("render: read pid %s: %w", pidFile, err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			return fmt.Errorf("render: bad pid in %s: %v", pidFile, err)
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		s, ok := sig.(syscall.Signal)
		if !ok {
			return fmt.Errorf("render: signal %v is not a syscall.Signal", sig)
		}
		return p.Signal(s)
	}
}

// HTTPGet returns a Hook that issues a GET to url and treats any
// 2xx/3xx response as success without following redirects. Requests time out
// after 20 seconds or when ctx is canceled. Use it to poke a webhook or kick off
// an external orchestrator after every config change.
func HTTPGet(url string) Hook {
	client := providerutil.HTTPClient(20 * time.Second)
	return func(ctx context.Context, _ string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 400 {
			return fmt.Errorf("render: http %s -> %d", url, resp.StatusCode)
		}
		return nil
	}
}

// ReloadSystemd runs systemctl reload for the named unit.
func ReloadSystemd(unit string) Hook {
	return func(ctx context.Context, _ string) error {
		return exec.CommandContext(ctx, "systemctl", "reload", unit).Run()
	}
}
