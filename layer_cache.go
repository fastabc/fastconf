package fastconf

import (
	"crypto/sha256"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/internal/scan"
)

// Bound retained decoded layers under directory rotation. FIFO is sufficient:
// the normal workload scans the same layers in order on every reload.
const maxCachedLayers = 512

type layerCacheKey struct {
	path, codec string
	sum         [32]byte
	generation  uint64
}

// layerCache is owned by the single writer, shared by Reload and Plan. File
// bytes are always read by discovery; mtime and size never bypass hashing.
type layerCache struct {
	entries map[layerCacheKey]map[string]any
	keys    []layerCacheKey
	next    int
}

func (c *layerCache) decoded(layer scan.Layer, sum [32]byte, dec codec.Decoder, generation uint64) (map[string]any, error) {
	key := layerCacheKey{layer.Path, layer.Codec, sum, generation}
	if raw, ok := c.entries[key]; ok {
		return confmap.DeepClone(raw), nil
	}
	raw, err := dec.Decode(layer.Bytes)
	if err != nil {
		return nil, err
	}
	if c.entries == nil {
		c.entries = make(map[layerCacheKey]map[string]any)
	}
	if len(c.keys) == maxCachedLayers {
		delete(c.entries, c.keys[c.next])
		c.keys[c.next] = key
		c.next = (c.next + 1) % maxCachedLayers
	} else {
		c.keys = append(c.keys, key)
	}
	c.entries[key] = raw
	// Merge aliases layer subtrees, which later stages mutate. Never hand the
	// cached instance to the pipeline, including on the first decode.
	return confmap.DeepClone(raw), nil
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
