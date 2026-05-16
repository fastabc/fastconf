// Package provider abstracts external configuration sources (env, CLI, KV,
// Vault, ...). Providers are second-class citizens of the reload pipeline:
// their output is merged into the same map[string]any that file discovery
// produces, after which the merged document is decoded into the user's
// strongly-typed *T.
//
// The Provider/Event interface itself lives in fastconf/contracts; this
// package re-exports it (see aliases.go) and ships the built-in Env/CLI/
// File implementations.
package provider
