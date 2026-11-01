package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// MetaFile is the deserialization target for conf.d/_meta.yaml. Compile-time defaults are used
// when the file is absent. Only a subset of fields is consumed today; the rest is reserved for
// forward compatibility.
type MetaFile struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Spec       MetaSpec `yaml:"spec"`
}

// MetaSpec mirrors the YAML schema. Apply() copies the subset that the scanner needs
// (BaseDir/OverlayDir/PatchSuffixes/Strict); the profile, append, and merge-key fields are
// consumed separately by the manager's buildScanOptions (root assemble.go).
type MetaSpec struct {
	BaseDir        string   `yaml:"baseDir"`
	OverlayDir     string   `yaml:"overlayDir"`
	ProfileEnv     string   `yaml:"profileEnv"`
	DefaultProfile string   `yaml:"defaultProfile"`
	PatchSuffixes  []string `yaml:"patchSuffixes"`
	Strict         bool     `yaml:"strict"`
	AppendSlices   bool     `yaml:"appendSlices"`
	// MergeKeys enables Kustomize-style strategic merge on
	// list-of-object slices. Each entry maps a dotted merged-tree path
	// to the field name that identifies "the same item" across overlays.
	MergeKeys map[string]string `yaml:"mergeKeys"`
}

// Apply overlays non-empty fields from meta onto ScanOptions. A true Strict
// enables strict handling even when the caller supplied false; this bool does
// not distinguish an explicit false from an unset option.
func (m *MetaFile) Apply(opt *ScanOptions) {
	if m == nil {
		return
	}
	if m.Spec.BaseDir != "" {
		opt.BaseDir = m.Spec.BaseDir
	}
	if m.Spec.OverlayDir != "" {
		opt.OverlayDir = m.Spec.OverlayDir
	}
	if len(m.Spec.PatchSuffixes) > 0 {
		opt.PatchSuffixes = m.Spec.PatchSuffixes
	}
	if m.Spec.Strict {
		opt.Strict = true
	}
}

// LoadMeta reads optional root/_meta.yaml with the discovery file limits.
// Missing returns (nil, nil); every other error propagates because metadata changes
// merge semantics and must not silently fall back to defaults.
func LoadMeta(fsys fs.FS, root string) ([]byte, error) {
	p := path.Join(root, "_meta.yaml")
	data, _, err := readLayer(fsys, p, true)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan: read _meta.yaml: %w", err)
	}
	return data, nil
}
