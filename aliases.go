package fastconf

import (
	iobs "github.com/fastabc/fastconf/internal/obs"
	"github.com/fastabc/fastconf/internal/provenance"
	"github.com/fastabc/fastconf/internal/secret"
)

// Public aliases preserve the domain types used by snapshot diagnostics.
type (
	// Origin records one source contribution.
	Origin = provenance.Origin
	// ProvenanceLevel controls the granularity of source tracking.
	ProvenanceLevel = provenance.Level
)

const (
	ProvenanceOff      = provenance.Off
	ProvenanceTopLevel = provenance.TopLevel
	ProvenanceFull     = provenance.Full
)

// SourceRef identifies a merged source layer.
type SourceRef = provenance.SourceRef

// LayerKind classifies a source layer.
type LayerKind = provenance.LayerKind

const (
	LayerUnknown   = provenance.LayerUnknown
	LayerMerge     = provenance.LayerMerge
	LayerPatch     = provenance.LayerPatch
	LayerProvider  = provenance.LayerProvider
	LayerSecret    = provenance.LayerSecret
	LayerGenerator = provenance.LayerGenerator
	LayerOverride  = provenance.LayerOverride
)

// SecretRedactor converts secret values into display values.
type SecretRedactor = secret.Redactor

// SecretRef identifies an opaque secret reference.
type SecretRef = secret.Ref

// SecretResolver recognizes and resolves secret references.
type SecretResolver = secret.Resolver

// SecretResolverFunc adapts functions to SecretResolver.
type SecretResolverFunc = secret.ResolverFunc

// Tracer starts reload and pipeline spans.
type Tracer = iobs.Tracer

// Span records stage attributes, errors and completion.
type Span = iobs.Span
