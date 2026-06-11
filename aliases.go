package fastconf

import (
	"reflect"

	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/pipeline"
	"github.com/fastabc/fastconf/internal/secret"
	discovery "github.com/fastabc/fastconf/overlay"
)

func init() {
	discovery.CodecExtFunc = codec.LookupExt
}

func RegisterCodec(name string, c contracts.Codec) {
	codec.Register(name, c)
}

func RegisterCodecExt(ext, codecName string) {
	codec.RegisterExt(ext, codecName)
}

func LookupCodec(name string) (contracts.Codec, bool) {
	return codec.Lookup(name)
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
