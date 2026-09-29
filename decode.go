package fastconf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	"github.com/fastabc/fastconf/codec"
	"gopkg.in/yaml.v3"
)

// jsonBufPool reuses the byte slice that backs decodeInto's
// encoder/decoder pair so a steady reload cadence does not balloon
// allocations on every cycle. Buffers grow to whatever the merged map
// needs and are recycled at 64 KiB — anything larger is released to GC
// instead of pinning peak memory forever.
var jsonBufPool = sync.Pool{
	New: func() any {
		buf := bytes.NewBuffer(make([]byte, 0, 4096))
		return buf
	},
}

const jsonBufRetainMax = 64 * 1024

// decodeInto populates *T through the selected JSON or YAML bridge. When
// strict is set, keys with no matching field fail the decode.
func decodeInto[T any](m map[string]any, target *T, yamlBridge, strict bool) error {
	if yamlBridge {
		node, err := orderedYAMLNode(m)
		if err != nil {
			return err
		}
		b, err := yaml.Marshal(node)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(strict)
		if err := dec.Decode(target); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= jsonBufRetainMax {
			jsonBufPool.Put(buf)
		}
	}()
	if err := json.NewEncoder(buf).Encode(m); err != nil {
		return err
	}
	dec := json.NewDecoder(buf)
	dec.UseNumber()
	if strict {
		dec.DisallowUnknownFields()
	}
	return dec.Decode(target)
}

// isUnknownField reports whether err is a strict decode's unknown-key error.
func isUnknownField(err error) bool {
	if err == nil {
		return false
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return true
	}
	var te *yaml.TypeError
	if errors.As(err, &te) {
		for _, msg := range te.Errors {
			if strings.Contains(msg, "not found in type") {
				return true
			}
		}
	}
	return false
}

func buildTypedHookPlan[T any](o *options) *codec.TypedHookPlan {
	// Build the typed hook plan once. Defaults are included unless
	// WithoutDefaultTypedHooks was set.
	hooks := []codec.TypedHook{}
	if !o.TypedHooksOff {
		hooks = append(hooks, codec.DefaultTypedHooks()...)
	}
	hooks = append(hooks, o.TypedHooks...)
	if len(hooks) == 0 {
		return nil
	}
	// Field selection must match the bridge that decodes the rewritten map.
	var planOpts []codec.PlanOption
	if o.YAMLBridge {
		planOpts = append(planOpts, codec.WithYAMLFields())
	}
	return codec.BuildTypedHookPlan(reflect.TypeFor[T](), hooks, planOpts...)
}

// warnIfYAMLOnlyTags scans T's exported fields once at construction time
// and emits a single warn log when *T has only `yaml:` tags but no
// `json:` tags. The default decoder is JSON,
// which silently ignores `yaml:` tags — a common new-user trap when
// migrating from Koanf/Viper. The warning steers operators toward either
// adding json tags or selecting WithDecoder(YAML).
//
// Cheap: walks fields with reflect.Type, no value introspection, no
// recursion. Runs once per Manager construction.
func warnIfYAMLOnlyTags[T any](logger *slog.Logger) {
	if logger == nil {
		return
	}
	t := reflect.TypeFor[T]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	var (
		hasYAML bool
		hasJSON bool
	)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Tag.Get("json") != "" {
			hasJSON = true
			break
		}
		if f.Tag.Get("yaml") != "" {
			hasYAML = true
		}
	}
	if hasJSON || !hasYAML {
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf: T has yaml tags but no json tags; the default JSON decoder ignores yaml tags. "+
		"Add WithDecoder(YAML) or json struct tags.", slog.String("type", t.String()))
}

// warnUnknownField logs err unless it repeats the previously logged one.
func (m *Manager[T]) warnUnknownField(err error) {
	msg := err.Error()
	if prev, _ := m.lastUnknownField.Load().(string); prev == msg {
		return
	}
	m.lastUnknownField.Store(msg)
	m.opts.Logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf: configuration key has no matching field; ignored (WithUnknownFields(UnknownError) rejects it)", slog.String("err", msg))
}
