package azappconfig

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	sdk "github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"github.com/fastabc/fastconf/contracts"
)

type fakeAPI struct {
	pages    [][]sdk.Setting
	err      error
	selector sdk.SettingSelector
}

func (f *fakeAPI) NewListSettingsPager(s sdk.SettingSelector, _ *sdk.ListSettingsOptions) *runtime.Pager[sdk.ListSettingsPageResponse] {
	f.selector = s
	n := 0
	return runtime.NewPager(runtime.PagingHandler[sdk.ListSettingsPageResponse]{
		More: func(sdk.ListSettingsPageResponse) bool { return n < len(f.pages) },
		Fetcher: func(context.Context, *sdk.ListSettingsPageResponse) (sdk.ListSettingsPageResponse, error) {
			if f.err != nil && n > 0 {
				return sdk.ListSettingsPageResponse{}, f.err
			}
			page := f.pages[n]
			n++
			return sdk.ListSettingsPageResponse{Settings: page}, nil
		},
	})
}
func setting(key, label, v string) sdk.Setting {
	return sdk.Setting{Key: &key, Label: &label, Value: &v}
}

func TestPagesLabelsAndRevision(t *testing.T) {
	f := &fakeAPI{pages: [][]sdk.Setting{{setting("app:server:host", "prod", "one")}, {setting("app:literal.dot", "prod", "true")}}}
	p, err := New(Config{Prefix: "app:", Label: "prod"}, f)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"server": map[string]any{"host": "one"}, "literal.dot": "true"}
	if !reflect.DeepEqual(first.Map, want) || *f.selector.LabelFilter != "prod" || *f.selector.KeyFilter != "app:*" {
		t.Fatalf("load = %+v, selector=%+v", first, f.selector)
	}
	f.pages[0], f.pages[1] = f.pages[1], f.pages[0]
	second, err := p.Load(context.Background())
	if err != nil || second.Revision != first.Revision {
		t.Fatalf("order affects revision: %v", err)
	}
	f.pages = [][]sdk.Setting{{}}
	empty, err := p.Load(context.Background())
	if err != nil || len(empty.Map) != 0 || empty.Revision == first.Revision {
		t.Fatalf("delete = %+v %v", empty, err)
	}
	p, _ = New(Config{Prefix: "a*,\\"}, f)
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *f.selector.LabelFilter != "\x00" || *f.selector.KeyFilter != `a\*\,\\*` {
		t.Fatalf("literal filter: %+v", f.selector)
	}
}

func TestAtomicFailures(t *testing.T) {
	special := setting("a", "", "secret")
	contentType := "application/vnd.microsoft.appconfig.keyvaultref+json;charset=utf-8"
	special.ContentType = &contentType
	for _, rows := range [][]sdk.Setting{
		{setting("a", "other", "x")}, {setting("a", "", "x"), setting("a:b", "", "y")},
		{setting("a", "", "x"), setting("a", "", "y")}, {setting("a::b", "", "x")}, {special},
	} {
		p, _ := New(Config{}, &fakeAPI{pages: [][]sdk.Setting{rows}})
		if snap, err := p.Load(context.Background()); err == nil || snap.Map != nil {
			t.Fatalf("accepted invalid tree: %+v %v", snap, err)
		}
	}
	denied := errors.New("secret upstream error")
	p, _ := New(Config{}, &fakeAPI{pages: [][]sdk.Setting{{setting("a", "", "x")}, {}}, err: denied})
	if snap, err := p.Load(context.Background()); !errors.Is(err, denied) || snap.Map != nil {
		t.Fatalf("partial page: %+v %v", snap, err)
	}
	p, _ = New(Config{}, &fakeAPI{pages: [][]sdk.Setting{{setting("a", "", "too large")}}}, WithMaxBodyBytes(2))
	if _, err := p.Load(context.Background()); !errors.Is(err, contracts.ErrTooLarge) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
