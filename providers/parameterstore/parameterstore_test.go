package parameterstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/providers/parameterstore"
)

type fakeAPI struct {
	pages map[string]*ssm.GetParametersByPathOutput
	err   error
	calls int
	check func(*ssm.GetParametersByPathInput)
}

func (f *fakeAPI) GetParametersByPath(ctx context.Context, in *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	f.calls++
	if f.check != nil {
		f.check(in)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	page, ok := f.pages[aws.ToString(in.NextToken)]
	if !ok {
		return nil, errors.New("unexpected page")
	}
	return page, nil
}

func param(name, value string, version int64) types.Parameter {
	return types.Parameter{Name: aws.String(name), Value: aws.String(value), Version: version}
}

func TestLoadPagesAndRevision(t *testing.T) {
	api := &fakeAPI{pages: map[string]*ssm.GetParametersByPathOutput{
		"":     {Parameters: []types.Parameter{param("/app/server/host", "localhost", 1)}, NextToken: aws.String("next")},
		"next": {Parameters: []types.Parameter{param("/app/enabled", "true", 2)}},
	}}
	api.check = func(in *ssm.GetParametersByPathInput) {
		if aws.ToString(in.Path) != "/app" || !aws.ToBool(in.Recursive) || !aws.ToBool(in.WithDecryption) {
			t.Fatalf("request = %+v", in)
		}
	}
	p, err := parameterstore.New(parameterstore.Config{Path: "/app/", Decrypt: true, Priority: 41}, api)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Load(context.Background())
	want := map[string]any{"server": map[string]any{"host": "localhost"}, "enabled": "true"}
	if err != nil || !reflect.DeepEqual(first.Map, want) || first.Revision == "" || api.calls != 2 {
		t.Fatalf("Load = %+v, %v; calls=%d", first, err, api.calls)
	}
	first.Map["server"].(map[string]any)["host"] = "mutated"
	api.pages = map[string]*ssm.GetParametersByPathOutput{"": {Parameters: []types.Parameter{
		param("/app/enabled", "true", 2), param("/app/server/host", "localhost", 1),
	}}}
	second, err := p.Load(context.Background())
	if err != nil || second.Revision != first.Revision || !reflect.DeepEqual(second.Map, want) {
		t.Fatalf("reordered Load = %+v, %v", second, err)
	}
	api.pages[""].Parameters[0].Version++
	third, err := p.Load(context.Background())
	if err != nil || third.Revision == second.Revision {
		t.Fatalf("updated version = %+v, %v", third, err)
	}
	api.pages[""].Parameters = nil
	empty, err := p.Load(context.Background())
	if err != nil || len(empty.Map) != 0 || empty.Revision == third.Revision {
		t.Fatalf("deleted parameters = %+v, %v", empty, err)
	}
	if p.Name() != "ssm:/app" || p.Describe().Priority != 41 {
		t.Fatalf("metadata = %s %+v", p.Name(), p.Describe())
	}
}

func TestLoadRejectsInvalidTrees(t *testing.T) {
	for name, params := range map[string][]types.Parameter{
		"leaf branch conflict": {param("/app/a", "x", 1), param("/app/a/b", "y", 1)},
		"outside prefix":       {param("/application/a", "x", 1)},
		"empty segment":        {param("/app/a//b", "x", 1)},
		"duplicate":            {param("/app/a", "x", 1), param("/app/a", "y", 1)},
		"missing value":        {{Name: aws.String("/app/a")}},
	} {
		t.Run(name, func(t *testing.T) {
			api := &fakeAPI{pages: map[string]*ssm.GetParametersByPathOutput{"": {Parameters: params}}}
			p, _ := parameterstore.New(parameterstore.Config{Path: "/app"}, api)
			if snap, err := p.Load(context.Background()); err == nil || snap.Map != nil {
				t.Fatalf("Load = %+v, %v; want atomic failure", snap, err)
			}
		})
	}
}

func TestLoadErrorsLimitsAndCancellation(t *testing.T) {
	denied := errors.New("access denied")
	api := &fakeAPI{err: denied}
	p, _ := parameterstore.New(parameterstore.Config{Path: "/app"}, api)
	if _, err := p.Load(context.Background()); !errors.Is(err, denied) {
		t.Fatalf("Load = %v", err)
	}
	api.err = nil
	api.pages = map[string]*ssm.GetParametersByPathOutput{"": {NextToken: aws.String("next")}}
	if snap, err := p.Load(context.Background()); err == nil || snap.Map != nil {
		t.Fatalf("partial pagination = %+v, %v", snap, err)
	}
	api.pages["next"] = &ssm.GetParametersByPathOutput{NextToken: aws.String("next")}
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("repeated token must fail")
	}
	api.pages = map[string]*ssm.GetParametersByPathOutput{"": {Parameters: []types.Parameter{param("/app/a", "xy", 1)}}}
	for _, limit := range []int64{7, 8, 0, -1} {
		p, _ := parameterstore.New(parameterstore.Config{Path: "/app"}, api, parameterstore.WithMaxBodyBytes(limit))
		_, err := p.Load(context.Background())
		if limit == 7 && !errors.Is(err, contracts.ErrTooLarge) || limit != 7 && err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := api.calls
	if _, err := p.Load(ctx); !errors.Is(err, context.Canceled) || api.calls != before {
		t.Fatalf("canceled Load = %v; calls=%d", err, api.calls)
	}
}

func TestNewValidation(t *testing.T) {
	for _, path := range []string{"", "/", "app", "/a//b", "/a/../b"} {
		if _, err := parameterstore.New(parameterstore.Config{Path: path}, &fakeAPI{}); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	if _, err := parameterstore.New(parameterstore.Config{Path: "/app"}, nil); err == nil {
		t.Fatal("accepted nil client")
	}
}

func TestManagerIntegration(t *testing.T) {
	api := &fakeAPI{pages: map[string]*ssm.GetParametersByPathOutput{"": {Parameters: []types.Parameter{param("/app/host", "production", 1)}}}}
	p, _ := parameterstore.New(parameterstore.Config{Path: "/app"}, api)
	type config struct {
		Host string `json:"host"`
	}
	snap, err := fastconf.Load[config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(p))
	if err != nil || snap.Value().Host != "production" {
		t.Fatalf("Load = %+v, %v", snap, err)
	}
}
