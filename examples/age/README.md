# age field decryption

Run from the repository root with Go 1.26+ using the optional module's dependencies:

```sh
GOPROXY=direct bash tools/modules.sh exec go -C secrets/age run ../../examples/age/main.go
GOPROXY=direct bash tools/modules.sh exec go -C secrets/age test -race ../../examples/age/main.go ../../examples/age/main_test.go
```

[main.go](main.go) generates an ephemeral X25519 identity, encrypts a demo value
with age, and loads an `age:<base64 ciphertext>` field through
`age.NewDecrypter` and `fastconf.PrefixSecretResolver`. Expected output:

```text
decrypted=true diagnostic=***REDACTED***
```

This exercises real cryptography locally, without accounts, sockets or persistent
keys. Production should read an application-owned identity from its secret store;
`NewDecrypter` accepts an `io.Reader` and parses identities once. The adapter caps
ciphertext at 64 KiB and returns sanitized errors. The demo never prints the
identity or plaintext.

Both Go files use `//go:build ignore` to keep age dependencies outside the root
module. Naming the files explicitly runs them; the disposable workspace resolves
the local root and age modules together. `make example` also runs this program and
its test. See [resolver chains](../secrets/README.md).
