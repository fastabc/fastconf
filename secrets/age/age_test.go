package age

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	fage "filippo.io/age"
)

func TestDecrypter_Decrypts(t *testing.T) {
	id, err := fage.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var ciphertext bytes.Buffer
	w, err := fage.Encrypt(&ciphertext, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("s3cret")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	body := base64.StdEncoding.EncodeToString(ciphertext.Bytes())
	dec, err := NewDecrypter(strings.NewReader(id.String()))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := dec(context.Background(), body)
	if err != nil || plain != "s3cret" {
		t.Fatalf("%q %v", plain, err)
	}
	other, _ := fage.GenerateX25519Identity()
	wrong, err := NewDecrypter(strings.NewReader(other.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong(context.Background(), body); err == nil {
		t.Fatal("accepted wrong identity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dec(ctx, body); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestDecrypter_BadInput(t *testing.T) {
	if _, err := NewDecrypter(strings.NewReader("")); err == nil {
		t.Fatal("accepted empty identities")
	}
	id, _ := fage.GenerateX25519Identity()
	dec, err := NewDecrypter(strings.NewReader(id.String()))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"!!", base64.StdEncoding.EncodeToString(make([]byte, 65537))} {
		if _, err := dec(context.Background(), body); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
}
