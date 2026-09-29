package confmap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// patchOp is one RFC 6902 operation; each member keeps its raw JSON so a present-but-null "value"
// is told apart from a missing one.
type patchOp map[string]json.RawMessage

func (op patchOp) str(key string) (string, bool, error) {
	raw, ok := op[key]
	if !ok {
		return "", false, nil
	}
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true, fmt.Errorf("%q must be a string", key)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", true, fmt.Errorf("%q must be a string", key)
	}
	return s, true, nil
}

// pointer returns the parsed pointer stored under key ("path" or "from").
func (op patchOp) pointer(key string) ([]string, error) {
	s, ok, err := op.str(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("missing %q", key)
	}
	if s == "" {
		return nil, nil
	}
	if s[0] != '/' {
		return nil, fmt.Errorf("invalid JSON pointer %q", s)
	}
	toks := strings.Split(s[1:], "/")
	for i, t := range toks {
		for j := 0; j < len(t); j++ {
			if t[j] == '~' {
				if j+1 == len(t) || (t[j+1] != '0' && t[j+1] != '1') {
					return nil, fmt.Errorf("invalid JSON pointer escape in %q", s)
				}
				j++
			}
		}
		// RFC 6901: decode ~1 before ~0.
		toks[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return toks, nil
}

// ApplyPatch applies an RFC 6902 JSON Patch (provided as a JSON-encoded array) to the merged
// document. Operations run on a deep copy of doc, so a failing patch leaves doc untouched and the
// caller keeps its previous document (failure-safe pipeline). Untouched leaves keep their Go
// types; patch values decode with json.Number for numbers, like JSON configuration files.
func ApplyPatch(doc map[string]any, patchJSON []byte) (map[string]any, error) {
	if len(patchJSON) == 0 {
		return doc, nil
	}
	var ops []patchOp
	if err := json.Unmarshal(patchJSON, &ops); err != nil {
		return nil, fmt.Errorf("merger: decode patch: %w", err)
	}
	if ops == nil {
		return nil, errors.New("merger: patch must be an array")
	}
	var root any = DeepClone(doc)
	for i, op := range ops {
		next, err := applyOp(root, op)
		if err != nil {
			name, _, _ := op.str("op")
			return nil, fmt.Errorf("merger: apply patch op %d (%s): %w", i, name, err)
		}
		root = next
	}
	out, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("merger: apply patch: document root must stay an object, got %T", root)
	}
	return out, nil
}

func applyOp(root any, op patchOp) (any, error) {
	name, _, err := op.str("op")
	if err != nil {
		return nil, err
	}
	path, err := op.pointer("path")
	if err != nil {
		return nil, err
	}
	var value any
	switch name {
	case "add", "replace", "test":
		raw, ok := op["value"]
		if !ok {
			return nil, errors.New(`missing "value"`)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
	case "move", "copy":
		from, err := op.pointer("from")
		if err != nil {
			return nil, err
		}
		if value, err = getAt(root, from); err != nil {
			return nil, err
		}
		if name == "copy" {
			return editAt(root, path, addTo(CloneValue(value)))
		}
		if len(from) == len(path) && isPrefix(from, path) {
			return root, nil
		}
		if isPrefix(from, path) {
			return nil, errors.New("cannot move a value into one of its children")
		}
		if root, err = editAt(root, from, removeFrom); err != nil {
			return nil, err
		}
		return editAt(root, path, addTo(value))
	case "remove":
	default:
		return nil, fmt.Errorf("unknown op %q", name)
	}
	switch name {
	case "add":
		return editAt(root, path, addTo(value))
	case "remove":
		if len(path) == 0 {
			return nil, errors.New("cannot remove the document root")
		}
		return editAt(root, path, removeFrom)
	case "replace":
		if root, err = editAt(root, path, removeFrom); err != nil {
			return nil, err
		}
		return editAt(root, path, addTo(value))
	default: // test
		cur, err := getAt(root, path)
		if err != nil {
			return nil, err
		}
		if !patchEqual(cur, value) {
			return nil, errors.New("test failed")
		}
		return root, nil
	}
}

func isPrefix(prefix, path []string) bool {
	return len(prefix) <= len(path) && slices.Equal(prefix, path[:len(prefix)])
}

// arrayIndex parses an array token. forAdd admits len(arr) and "-".
func arrayIndex(tok string, n int, forAdd bool) (int, error) {
	if tok == "-" && forAdd {
		return n, nil
	}
	i, err := strconv.Atoi(tok)
	if err != nil || !isAllDigits(tok) || (len(tok) > 1 && tok[0] == '0') {
		return 0, fmt.Errorf("invalid array index %q", tok)
	}
	if i > n || (i == n && !forAdd) {
		return 0, fmt.Errorf("array index %d out of range", i)
	}
	return i, nil
}

func getAt(node any, path []string) (any, error) {
	for _, tok := range path {
		switch c := node.(type) {
		case map[string]any:
			v, ok := c[tok]
			if !ok {
				return nil, fmt.Errorf("path segment %q not found", tok)
			}
			node = v
		case []any:
			i, err := arrayIndex(tok, len(c), false)
			if err != nil {
				return nil, err
			}
			node = c[i]
		default:
			return nil, fmt.Errorf("cannot index %T with %q", node, tok)
		}
	}
	return node, nil
}

// leafEdit rewrites one container given the last pointer token; it returns the container to store
// back (slices are rebuilt, maps edited in place).
type leafEdit func(container any, tok string) (any, error)

// editAt applies edit to the parent of path and stores the result back up the chain. An empty path
// replaces the whole document.
func editAt(node any, path []string, edit leafEdit) (any, error) {
	if len(path) == 0 {
		return edit(nil, "")
	}
	if patchNull(node) {
		return nil, fmt.Errorf("cannot index null with %q", path[0])
	}
	if len(path) == 1 {
		return edit(node, path[0])
	}
	child, err := getAt(node, path[:1])
	if err != nil {
		return nil, err
	}
	updated, err := editAt(child, path[1:], edit)
	if err != nil {
		return nil, err
	}
	switch c := node.(type) {
	case map[string]any:
		c[path[0]] = updated
	case []any:
		i, _ := arrayIndex(path[0], len(c), false)
		c[i] = updated
	}
	return node, nil
}

func addTo(value any) leafEdit {
	return func(container any, tok string) (any, error) {
		switch c := container.(type) {
		case nil:
			return value, nil // whole-document add/replace
		case map[string]any:
			c[tok] = value
			return c, nil
		case []any:
			i, err := arrayIndex(tok, len(c), true)
			if err != nil {
				return nil, err
			}
			return slices.Insert(slices.Clone(c), i, value), nil
		default:
			return nil, fmt.Errorf("cannot add %q to %T", tok, container)
		}
	}
}

func removeFrom(container any, tok string) (any, error) {
	switch c := container.(type) {
	case nil:
		return nil, nil // replace "" removes the document before re-adding it
	case map[string]any:
		if _, ok := c[tok]; !ok {
			return nil, fmt.Errorf("path segment %q not found", tok)
		}
		delete(c, tok)
		return c, nil
	case []any:
		i, err := arrayIndex(tok, len(c), false)
		if err != nil {
			return nil, err
		}
		return slices.Delete(slices.Clone(c), i, i+1), nil
	default:
		return nil, fmt.Errorf("cannot remove %q from %T", tok, container)
	}
}

// patchEqual compares JSON values structurally; numbers compare by value regardless of Go
// representation (json.Number, int64, float64, ...).
func patchEqual(a, b any) bool {
	if patchNull(a) || patchNull(b) {
		return patchNull(a) && patchNull(b)
	}
	x, aNum := toNumber(a)
	y, bNum := toNumber(b)
	if aNum || bNum {
		return aNum && bNum && x == y
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		return ok && maps.EqualFunc(av, bv, patchEqual)
	case []any:
		bv, ok := b.([]any)
		return ok && slices.EqualFunc(av, bv, patchEqual)
	default:
		return reflect.DeepEqual(a, b)
	}
}

// patchNull follows encoding/json: typed nil maps and slices represent null.
func patchNull(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case map[string]any:
		return v == nil
	case []any:
		return v == nil
	}
	return false
}

// toNumber normalizes a JSON number to a signed coefficient and decimal exponent.
// It neither rounds significant digits nor expands large exponents into huge integers.
func toNumber(v any) (string, bool) {
	if !isNumber(v) {
		return "", false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	s := string(b)
	exp := new(big.Int)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp.SetString(s[i+1:], 10) // json.Marshal validated the exponent.
		s = s[:i]
	}
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exp.Sub(exp, big.NewInt(int64(len(s)-i-1)))
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", true
	}
	coefficient := strings.TrimRight(s, "0")
	exp.Add(exp, big.NewInt(int64(len(s)-len(coefficient))))
	if negative {
		coefficient = "-" + coefficient
	}
	return coefficient + "e" + exp.String(), true
}

// PatchPaths returns the dotted destination paths named by an RFC 6902 ops array. Only "path" is
// reported: for move/copy the value lands at "path", so that is the field a patch actually
// contributes. JSON Pointer escapes (~1 → "/", ~0 → "~") are decoded. A path is truncated at the
// first numeric array-index segment because the provenance index records a whole []any under its
// parent map path (recordTreeDepth's slice branch) — the slice's parent is the real attributable
// leaf, and anything past the index would be a phantom key. Empty / root paths and a leading-index
// path yield "".
func PatchPaths(patchJSON []byte) ([]string, error) {
	if len(patchJSON) == 0 {
		return nil, nil
	}
	var ops []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(patchJSON, &ops); err != nil {
		return nil, fmt.Errorf("merger: decode patch paths: %w", err)
	}
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, pointerToDotted(op.Path))
	}
	return out, nil
}

// pointerToDotted converts an RFC 6901 JSON Pointer to the dotted path grammar used by the
// provenance index.
func pointerToDotted(ptr string) string {
	if ptr == "" || ptr == "/" {
		return ""
	}
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		// RFC 6901: decode ~1 before ~0.
		s = strings.ReplaceAll(s, "~1", "/")
		s = strings.ReplaceAll(s, "~0", "~")
		if isAllDigits(s) {
			// Array index: provenance records the whole slice under its
			// parent map path, so truncate — the parent is the real leaf.
			break
		}
		out = append(out, s)
	}
	return strings.Join(out, ".")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PatchBytesFromAny converts a decoded patch payload (either []any or already-encoded raw JSON) to
// the JSON byte form expected by ApplyPatch.
func PatchBytesFromAny(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return json.Marshal(v)
}
