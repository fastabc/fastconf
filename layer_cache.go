package fastconf

import (
	"crypto/sha256"

	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/scan"
)

// Bound retained decoded layers under directory rotation. FIFO is sufficient:
// the normal workload scans the same layers in order on every reload.
const maxCachedLayers = 512

type layerCacheKey struct {
	path, codec string
}

// layerCache is owned by the single writer, shared by Reload and Plan. File
// bytes are always read by discovery; mtime and size never bypass hashing.
type layerCache struct {
	entries map[layerCacheKey]*decodedLayer
	keys    []layerCacheKey
	next    int
}

// decodedLayer is immutable after decoding. Assembly may borrow it; only a
// clone may enter merge, whose output can alias its layer inputs.
type decodedLayer struct {
	sum        [32]byte
	generation uint64
	tree       map[string]any
}

func (l *decodedLayer) clone() map[string]any { return confmap.DeepClone(l.tree) }

func (c *layerCache) decoded(layer scan.Layer, sum [32]byte, dec contracts.Decoder, generation uint64) (*decodedLayer, error) {
	key := layerCacheKey{layer.Path, layer.Codec}
	if cached := c.entries[key]; cached != nil && cached.sum == sum && cached.generation == generation {
		return cached, nil
	}
	raw, err := dec.Decode(layer.Bytes)
	if err != nil {
		return nil, err
	}
	if c.entries == nil {
		c.entries = make(map[layerCacheKey]*decodedLayer)
	}
	if c.entries[key] == nil {
		if len(c.keys) == maxCachedLayers {
			delete(c.entries, c.keys[c.next])
			c.keys[c.next] = key
			c.next = (c.next + 1) % maxCachedLayers
		} else {
			c.keys = append(c.keys, key)
		}
	}
	entry := &decodedLayer{sum: sum, generation: generation, tree: raw}
	c.entries[key] = entry
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

func (c *metaKeysCache) combined(metaBytes []byte, build func() map[string]string) map[string]string {
	sum := sha256.Sum256(metaBytes)
	if !c.set || c.sum != sum {
		c.sum, c.keys, c.set = sum, build(), true
	}
	return c.keys
}
