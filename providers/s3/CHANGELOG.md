# Changelog

## [v1.1.0]

### Added

- `WithMaxBodyBytes(n)` configures the maximum object body accepted by `Load`.
  Pass it to `New(cfg, opts...)` or `NewWithClient(cfg, client, opts...)`.
  The default remains 10 MiB; zero and negative values restore that default.
  Oversized responses wrap `contracts.ErrTooLarge` and do not update the cache.

### Changed

- Body limits use the shared bounded reader. Existing constructor calls remain
  valid; code assigning a constructor to a fixed-signature function variable
  must account for the new variadic `...Option` parameter.
