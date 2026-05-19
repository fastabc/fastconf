package discovery

import "io/fs"

// MetaFile is the deserialization target for conf.d/_meta.yaml.
// Compile-time defaults are used when the file is absent. Only a subset
// of fields is consumed today; the rest is reserved for forward
// compatibility.
type MetaFile struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Spec       MetaSpec `yaml:"spec"`
}

// MetaSpec mirrors the YAML schema. Apply() copies the subset that the
// discovery scanner needs; the framework consumes the rest separately
// (see ApplyManager in fastconf/manager.go).
type MetaSpec struct {
	BaseDir        string   `yaml:"baseDir"`
	OverlayDir     string   `yaml:"overlayDir"`
	ProfileEnv     string   `yaml:"profileEnv"`
	DefaultProfile string   `yaml:"defaultProfile"`
	PatchSuffixes  []string `yaml:"patchSuffixes"`
	Ordering       string   `yaml:"ordering"`
	Strict         bool     `yaml:"strict"`
	AppendSlices   bool     `yaml:"appendSlices"`
	RedactEnvKeys  []string `yaml:"redactEnvKeys"`
	// MergeKeys enables Kustomize-style strategic merge on
	// list-of-object slices. Each entry maps a dotted merged-tree path
	// to the field name that identifies "the same item" across overlays.
	MergeKeys map[string]string `yaml:"mergeKeys"`
}

// Apply overlays the explicit fields from meta onto ScanOptions. Meta
// takes precedence over compile-time defaults but yields to explicit
// caller-provided Options.
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

// LoadMeta tries to read root/_meta.yaml. Returns (nil, nil) when the
// file is absent — _meta.yaml is optional.
func LoadMeta(fsys fs.FS, root string) ([]byte, error) {
	p := root + "/_meta.yaml"
	data, err := readFile(fsys, p)
	if err != nil {
		return nil, nil
	}
	return data, nil
}
