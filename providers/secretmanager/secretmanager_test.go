package secretmanager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/fastabc/fastconf/contracts"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
)

type fakeAPI struct {
	response *secretmanagerpb.AccessSecretVersionResponse
	err      error
	names    []string
}

func (f *fakeAPI) AccessSecretVersion(_ context.Context, in *secretmanagerpb.AccessSecretVersionRequest, _ ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.names = append(f.names, in.Name)
	return f.response, f.err
}
func payload(body, version string) *secretmanagerpb.AccessSecretVersionResponse {
	checksum := int64(crc32.Checksum([]byte(body), crc32.MakeTable(crc32.Castagnoli)))
	return &secretmanagerpb.AccessSecretVersionResponse{Name: "projects/p/secrets/s/versions/" + version, Payload: &secretmanagerpb.SecretPayload{Data: []byte(body), DataCrc32C: &checksum}}
}

func TestVersionAccessIntegrityAndNoStaleCache(t *testing.T) {
	f := &fakeAPI{response: payload(`{"password":"secret"}`, "1")}
	p, err := New(Config{Name: "projects/p/secrets/s/versions/latest", Codec: "json"}, f)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Load(context.Background())
	if err != nil || first.Revision != f.response.Name {
		t.Fatalf("load = %+v %v", first, err)
	}
	f.response = payload(`{"password":"rotated"}`, "2")
	second, err := p.Load(context.Background())
	if err != nil || second.Revision == first.Revision || second.Map["password"] != "rotated" {
		t.Fatalf("rotation = %+v %v", second, err)
	}
	*f.response.Payload.DataCrc32C++
	if _, err := p.Load(context.Background()); !errors.Is(err, ErrChecksum) {
		t.Fatal(err)
	}
	f.err = errors.New("disabled secret with sensitive details")
	if snap, err := p.Load(context.Background()); !errors.Is(err, f.err) || snap.Map != nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("stale/leak = %+v %v", snap, err)
	}
	for _, name := range f.names {
		if name != p.cfg.Name {
			t.Fatal("alias request rewritten to cached version")
		}
	}
}

func TestRejectedPayloadAndCancellation(t *testing.T) {
	f := &fakeAPI{response: payload(`secret-bad-json`, "1")}
	p, _ := New(Config{Name: "projects/p/secrets/s/versions/1", Codec: "json"}, f)
	if _, err := p.Load(context.Background()); err == nil || strings.Contains(err.Error(), "secret-bad") {
		t.Fatalf("decode: %v", err)
	}
	p, _ = New(p.cfg, f, WithMaxBodyBytes(1))
	if _, err := p.Load(context.Background()); !errors.Is(err, contracts.ErrTooLarge) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := len(f.names)
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) || len(f.names) != before {
		t.Fatal("cancellation ignored")
	}
	for _, name := range []string{"", "projects/p/secrets/s", "projects//secrets/s/versions/1", "projects/p/secrets/s/versions/.."} {
		if _, err := New(Config{Name: name, Codec: "json"}, f); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestRealRESTClient(t *testing.T) {
	body := `{"host":"local"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/p/secrets/s/versions/latest:access" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "projects/p/secrets/s/versions/7", "payload": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(body)), "dataCrc32c": payload(body, "7").Payload.GetDataCrc32C()}})
	}))
	defer srv.Close()
	client, err := sdk.NewRESTClient(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication(), option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	p, _ := New(Config{Name: "projects/p/secrets/s/versions/latest", Codec: "json"}, client)
	snap, err := p.Load(context.Background())
	if err != nil || snap.Map["host"] != "local" {
		t.Fatalf("SDK load: %+v %v", snap, err)
	}
}
