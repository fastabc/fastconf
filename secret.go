package fastconf

import "github.com/fastabc/fastconf/internal/secret"

// SecretRedactor converts secret values into display values.
type SecretRedactor = secret.Redactor

// SecretRef identifies an opaque secret reference.
type SecretRef = secret.Ref

// SecretResolver recognizes and resolves secret references.
type SecretResolver = secret.Resolver

// SecretResolverFunc adapts functions to SecretResolver.
type SecretResolverFunc = secret.ResolverFunc
