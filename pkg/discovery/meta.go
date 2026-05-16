package discovery

import "io/fs"

// MetaFile 是 conf.d/_meta.yaml 的反序列化目标。
// 不存在时使用编译期默认值。Phase 2 仅消费其中字段子集；其余保留以便前向兼容。
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
	// MergeKeys (Phase 132) enables Kustomize-style strategic merge on
	// list-of-object slices. Each entry maps a dotted merged-tree path
	// to the field name that identifies "the same item" across overlays.
	MergeKeys map[string]string `yaml:"mergeKeys"`
}

// Apply 把 meta 中显式字段叠加到 ScanOptions（meta 优先于代码默认值，但低于显式 Option）。
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

// LoadMeta 尝试从 root/_meta.yaml 加载；不存在则返回 nil, nil。
func LoadMeta(fsys fs.FS, root string) ([]byte, error) {
	p := root + "/_meta.yaml"
	data, err := readFile(fsys, p)
	if err != nil {
		// 不存在视为可选
		return nil, nil
	}
	return data, nil
}
