// Package decoder 把不同编码（yaml/json/...）的字节流解码为统一的
// map[string]any 中间表示。该中间表示只在 reload 流水线内部存在，
// 不会出现在公开 API 上。
//
// 解码器通过进程内 registry 注册（见 registry.go）。内置 yaml/yml/json
// 在 init 时注册；外部 codec（toml/hcl/json5/...）可通过 fastconf.RegisterCodec
// 在仓库外注入。
package decoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fastabc/fastconf/contracts"
)

// Decoder 把字节流解码为通用 map。
//
// Decoder 等价于公共契约 contracts.Codec — 内部包以别名形式持有，
// 任何实现 contracts.Codec 的类型都可直接喂进 internal/decoder 的注册表。
type Decoder = contracts.Codec

// ErrUnknownCodec 表示不识别的 codec 名。
var ErrUnknownCodec = errors.New("decoder: unknown codec")

// For 按 codec 名返回 Decoder。codec 不区分大小写。
// 不在注册表中时返回 ErrUnknownCodec。
func For(codec string) (Decoder, error) {
	if c, ok := Lookup(codec); ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
}

// CodecFromExt infers a codec name from a file extension (with or without
// the leading dot). Returns empty string when the extension is unrecognised.
// Queries only codecs registered via RegisterCodec/RegisterCodecExt.
func CodecFromExt(ext string) string {
	return LookupExt(ext)
}

// DecodeAny decodes data as either an object or an array (used for RFC 6902
// patch layers whose top-level node is an array).
// Returns either map[string]any or []any. Returns nil on empty input.
func DecodeAny(codec string, data []byte) (any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	switch strings.ToLower(codec) {
	case "yaml", "yml":
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("yaml: %w", err)
		}
		return normalizeValue(raw), nil
	case "json":
		var raw any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("json: %w", err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownCodec, codec)
	}
}
