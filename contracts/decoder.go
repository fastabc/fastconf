package contracts

// Decoder decodes a document into a map. Implementations must be concurrent-safe and reject
// documents whose root is not an object.
type Decoder interface {
	Decode(data []byte) (map[string]any, error)
}
