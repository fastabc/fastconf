// Package migration lets FastConf rewrite the merged map from one
// schema version to another before it is decoded into the strongly
// typed snapshot. It addresses the "long-lived config + evolving
// struct" pain point: instead of forcing every operator to hand-edit
// every overlay file, a Migration shifts ageing keys (e.g. db.url ->
// db.dsn) on the fly during reload.
//
// Migrations form an explicit chain keyed by _meta.schemaVersion. The
// chain runs after merge but before transform/decode; the final version
// is written back into _meta.schemaVersion so subsequent reloads
// fast-skip already-migrated input.
package migration

import (
	"errors"
	"fmt"
	"sort"
)

// Migration upgrades a merged configuration map from From to To.
type Migration struct {
	From  int
	To    int
	Apply func(m map[string]any) error
}

// Chain is an ordered, validated set of Migrations that can be applied
// in sequence. Use New(...) to construct one.
type Chain struct {
	target int
	byFrom map[int]Migration
}

// New builds a Chain ensuring every migration's From == previous.To
// (gap- and dup-free). target is the highest schemaVersion the chain
// can reach; clients should set it to the latest version their typed
// struct understands.
func New(target int, migrations ...Migration) (*Chain, error) {
	sort.SliceStable(migrations, func(i, j int) bool { return migrations[i].From < migrations[j].From })
	byFrom := map[int]Migration{}
	for i, m := range migrations {
		if m.Apply == nil {
			return nil, fmt.Errorf("migration: nil Apply at index %d", i)
		}
		if m.From >= m.To {
			return nil, fmt.Errorf("migration: From=%d must be < To=%d", m.From, m.To)
		}
		if _, dup := byFrom[m.From]; dup {
			return nil, fmt.Errorf("migration: duplicate From=%d", m.From)
		}
		byFrom[m.From] = m
	}
	return &Chain{target: target, byFrom: byFrom}, nil
}

// MetaKey is the conventional path inside the merged map storing the
// current schema version: _meta.schemaVersion.
const MetaKey = "_meta"

// FieldKey is the field inside MetaKey holding an int.
const FieldKey = "schemaVersion"

// CurrentVersion reads m._meta.schemaVersion. Missing or non-int
// values are treated as version 0 (assume the oldest schema).
func CurrentVersion(m map[string]any) int {
	meta, _ := m[MetaKey].(map[string]any)
	if meta == nil {
		return 0
	}
	switch v := meta[FieldKey].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// SetVersion writes m._meta.schemaVersion = v, creating _meta if absent.
func SetVersion(m map[string]any, v int) {
	meta, ok := m[MetaKey].(map[string]any)
	if !ok {
		meta = map[string]any{}
		m[MetaKey] = meta
	}
	meta[FieldKey] = v
}

// Run upgrades m to c.target, returning the final version. It is a
// no-op when m is already at or above target. Returns an error if the
// chain has a gap or any Apply fails (m may be partially mutated; the
// caller should discard m and roll back on error).
func (c *Chain) Run(m map[string]any) (int, error) {
	if c == nil {
		return 0, errors.New("migration: nil chain")
	}
	cur := CurrentVersion(m)
	if cur >= c.target {
		return cur, nil
	}
	for cur < c.target {
		mig, ok := c.byFrom[cur]
		if !ok {
			return cur, fmt.Errorf("migration: no migration from version %d", cur)
		}
		if err := mig.Apply(m); err != nil {
			return cur, fmt.Errorf("migration: %d->%d: %w", mig.From, mig.To, err)
		}
		cur = mig.To
	}
	SetVersion(m, cur)
	return cur, nil
}

// Target returns the highest version the chain can reach.
func (c *Chain) Target() int { return c.target }
