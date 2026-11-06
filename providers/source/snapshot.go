package source

import (
	"fmt"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
)

// snapshotFrom turns a read result into a Snapshot with the codec matching ct. An empty document is an
// empty layer.
func snapshotFrom(name string, data []byte, ct, rev string, err error) (contracts.Snapshot, error) {
	if err != nil {
		return contracts.Snapshot{}, err
	}
	if len(data) == 0 {
		return contracts.Snapshot{Map: map[string]any{}, Revision: rev}, nil
	}
	c, ok := codec.ByContentType(ct)
	if !ok {
		return contracts.Snapshot{}, fmt.Errorf("source %q: %w: content-type %q", name, codec.ErrUnknownCodec, ct)
	}
	m, err := c.Decode(data)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	return contracts.Snapshot{Map: m, Revision: rev}, nil
}
