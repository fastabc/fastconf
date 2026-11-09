//go:build ignore

// Run through tools/modules.sh so the optional age module follows this checkout.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing/fstest"

	fage "filippo.io/age"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/source"
	secretage "github.com/fastabc/fastconf/secrets/age"
)

func run(out io.Writer) error {
	// Generate an ephemeral identity and ciphertext for this offline demo only.
	// Production reads an application-owned identity from its secret store.
	identity, err := fage.GenerateX25519Identity()
	if err != nil {
		return err
	}
	var encrypted bytes.Buffer
	w, err := fage.Encrypt(&encrypted, identity.Recipient())
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, "demo-password"); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	decrypt, err := secretage.NewDecrypter(strings.NewReader(identity.String()))
	if err != nil {
		return err
	}
	ref := secretage.Prefix + base64.StdEncoding.EncodeToString(encrypted.Bytes())
	state, err := fastconf.Load[map[string]string](context.Background(), fastconf.WithFS(fstest.MapFS{}),
		fastconf.WithProvider(source.NewBytes("encrypted", "json", []byte(fmt.Sprintf(`{"password":%q}`, ref)))),
		fastconf.WithSecretResolvers(fastconf.PrefixSecretResolver("age", secretage.Prefix, decrypt)))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "decrypted=%t diagnostic=%v\n", (*state.Value())["password"] == "demo-password", state.Map()["password"])
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
