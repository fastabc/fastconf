package fastconf

import (
	"crypto/sha256"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/scan"
)

// Bound admission by entry count and source-byte weight. This is not a decoded
// heap/RSS limit: codecs may expand documents. Oversized documents still load.
const (
	maxCachedLayers      = 512
	maxCachedSourceBytes = 16 << 20
)

type layerCacheKey struct {
	path, codec string
}

// layerCache is owned by the single writer, shared by Reload and Plan. File
// bytes are always read by discovery; mtime and size never bypass hashing.
type layerCache struct {
	entries     map[layerCacheKey]*decodedLayer
	sourceBytes int
}

// decodedLayer payload is immutable after decoding. Only a cloneTree may enter
// merge, whose output can alias its layer inputs. active is writer-owned cache bookkeeping.
type decodedLayer struct {
	sum         [32]byte
	generation  uint64
	tree        map[string]any
	sourceBytes int
	active      bool
}

func (l *decodedLayer) cloneTree() map[string]any { return confmap.DeepClone(l.tree) }

// retain releases paths absent from this scan. During decoding, active entries
// are never evicted to admit another path: a sequential scan larger than the
// cache must not evict every hit before it is visited.
func (c *layerCache) retain(layers []stagedLayer) {
	if len(c.entries) == 0 {
		return
	}
	for _, entry := range c.entries {
		entry.active = false
	}
	for _, l := range layers {
		if l.fileSum != ([32]byte{}) && l.src.Kind != LayerPatch {
			if entry := c.entries[layerCacheKey{l.src.Path, l.src.Codec}]; entry != nil {
				entry.active = true
			}
		}
	}
	for key, entry := range c.entries {
		if !entry.active {
			c.sourceBytes -= entry.sourceBytes
			delete(c.entries, key)
		}
	}
}

func (c *layerCache) getOrDecode(layer scan.Layer, sum [32]byte, dec contracts.Decoder, generation uint64) (*decodedLayer, error) {
	key := layerCacheKey{layer.Path, layer.Codec}
	if cached := c.entries[key]; cached != nil && cached.sum == sum && cached.generation == generation {
		return cached, nil
	}
	raw, err := dec.Decode(layer.Bytes)
	if err != nil {
		return nil, err
	}
	entry := &decodedLayer{sum: sum, generation: generation, tree: raw, sourceBytes: len(layer.Bytes)}
	if old := c.entries[key]; old != nil {
		c.sourceBytes -= old.sourceBytes
		delete(c.entries, key)
	}
	if len(c.entries) >= maxCachedLayers || entry.sourceBytes > maxCachedSourceBytes-c.sourceBytes {
		return entry, nil
	}
	if c.entries == nil {
		c.entries = make(map[layerCacheKey]*decodedLayer)
	}
	c.entries[key] = entry
	c.sourceBytes += entry.sourceBytes
	return entry, nil
}

// metaKeysCache remembers the merge-key table built for the last
// _meta.yaml content. The table is read-only once built, so every reload
// and Plan with unchanged _meta.yaml shares it.
type metaKeysCache struct {
	sum  [32]byte
	keys map[string]string
	set  bool
}

func (c *metaKeysCache) getOrBuild(metaBytes []byte, build func() map[string]string) map[string]string {
	sum := sha256.Sum256(metaBytes)
	if !c.set || c.sum != sum {
		c.sum, c.keys, c.set = sum, build(), true
	}
	return c.keys
}
