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

	"github.com/fastabc/fastconf/internal/typeinfo"
	"gopkg.in/yaml.v3"
)

// jsonBufPool reuses bridge buffers up to 64 KiB; larger buffers are left to GC
// so a large reload does not pin peak memory.
var jsonBufPool = sync.Pool{
	New: func() any {
		buf := bytes.NewBuffer(make([]byte, 0, 4096))
		return buf
	},
}

const jsonBufRetainMax = 64 * 1024

// bridgeInput owns the bridge bytes for one pipeline decode, including a
// possible strict-to-loose retry. JSON buffers return to the bounded pool.
type bridgeInput struct {
	data   []byte
	buffer *bytes.Buffer
	yaml   bool
}

func encodeBridgeInput(m map[string]any, yamlBridge bool) (bridgeInput, error) {
	if yamlBridge {
		node, err := sortedYAMLNode(m)
		if err != nil {
			return bridgeInput{}, err
		}
		data, err := yaml.Marshal(node)
		return bridgeInput{data: data, yaml: true}, err
	}
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	input := bridgeInput{buffer: buf}
	if err := json.NewEncoder(buf).Encode(m); err != nil {
		input.release()
		return bridgeInput{}, err
	}
	input.data = buf.Bytes()
	return input, nil
}

func (e bridgeInput) release() {
	if e.buffer != nil && e.buffer.Cap() <= jsonBufRetainMax {
		jsonBufPool.Put(e.buffer)
	}
}

func (e bridgeInput) decode(target any, strict bool) error {
	if e.yaml {
		dec := yaml.NewDecoder(bytes.NewReader(e.data))
		dec.KnownFields(strict)
		if err := dec.Decode(target); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
	// Reset only the reader position; retries reuse the same encoded bytes.
	*e.buffer = *bytes.NewBuffer(e.data)
	dec := json.NewDecoder(e.buffer)
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

// warnIfYAMLOnlyTags checks top-level exported fields once per construction,
// warning when the default JSON bridge would ignore YAML-only tags.
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
	typeinfo.StructFields(t, 0, func(f reflect.StructField, _ []int) {
		hasJSON = hasJSON || f.Tag.Get("json") != ""
		hasYAML = hasYAML || f.Tag.Get("yaml") != ""
	})
	if hasJSON || !hasYAML {
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelWarn, "fastconf: T has yaml tags but no json tags; the default JSON decoder ignores yaml tags. "+
		"Add WithDecodeFormat(YAML) or json struct tags.", slog.String("type", t.String()))
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

// Defaulter supplies computed defaults after decoding and struct-tag defaults,
// before validation. It is called once per pipeline execution when *T implements it.
type Defaulter interface {
	Defaults()
}
