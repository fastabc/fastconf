package fastconf

import (
	"reflect"

	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/pipeline"
	"github.com/fastabc/fastconf/internal/secret"
	"github.com/fastabc/fastconf/pkg/decoder"
	"github.com/fastabc/fastconf/pkg/discovery"
)

func init() {
	discovery.CodecExtFunc = decoder.LookupExt
}

func RegisterCodec(name string, c contracts.Codec) {
	decoder.Register(name, c)
}

func RegisterCodecExt(ext, codec string) {
	decoder.RegisterExt(ext, codec)
}

func LookupCodec(name string) (contracts.Codec, bool) {
	return decoder.Lookup(name)
}

type SecretRedactor = secret.Redactor

func DefaultSecretRedactor(path string, value any) any {
	return secret.DefaultRedactor(path, value)
}

type SecretRef = secret.Ref
type SecretResolver = secret.Resolver
type SecretResolverFunc = secret.ResolverFunc

type FieldSpec = pipeline.FieldSpec

func ParseFieldTag(tag string) FieldSpec { return pipeline.ParseFieldTag(tag) }

func FieldMetaFor(t reflect.Type) []FieldSpec { return pipeline.FieldMetaFor(t) }
