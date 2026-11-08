// Package age decrypts base64-encoded age ciphertext for a FastConf prefix resolver.
package age

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	fage "filippo.io/age"
)

// Prefix identifies a base64-encoded binary age message.
const Prefix = "age:"
const maxCiphertext = 64 << 10

// NewDecrypter parses identities once, without requiring applications to import
// filippo.io/age. The returned function accepts the body after Prefix.
func NewDecrypter(identities io.Reader) (func(context.Context, string) (string, error), error) {
	ids, err := fage.ParseIdentities(identities)
	if err != nil {
		return nil, errors.New("age resolver: invalid identities")
	}
	if len(ids) == 0 {
		return nil, errors.New("age resolver: no identities")
	}
	return func(ctx context.Context, body string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Bound decoding even for an attacker-controlled base64 string.
		raw, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(body)), maxCiphertext+1))
		if err != nil {
			return "", errors.New("age resolver: invalid base64")
		}
		if len(raw) > maxCiphertext {
			return "", errors.New("age resolver: ciphertext exceeds 64 KiB")
		}
		plain, err := fage.Decrypt(bytes.NewReader(raw), ids...)
		if err != nil {
			return "", errors.New("age resolver: decryption failed")
		}
		out, err := io.ReadAll(plain)
		if err != nil {
			return "", errors.New("age resolver: decryption failed")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return string(out), nil
	}, nil
}
