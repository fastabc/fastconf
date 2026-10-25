// Command reload previews local file changes, rejects invalid input, and rolls back.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/policy"
	"github.com/fastabc/fastconf/providers/source"
)

//go:embed config/*.yaml
var files embed.FS

type Config struct {
	Port int `json:"port" yaml:"port" fc:"min=1,max=65535"`
}

func run(out io.Writer) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "fastconf-reload-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "app.yaml")
	write := func(name string) error {
		data, err := files.ReadFile("config/" + name + ".yaml")
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	if err := write("initial"); err != nil {
		return err
	}

	// No automatic watcher: each change is inspected before manual publication.
	mgr, err := fastconf.New[Config](ctx,
		fastconf.WithDir(dir),
		fastconf.WithProvider(source.NewFile(path)),
		fastconf.WithHistory(4),
		fastconf.WithUnknownFields(fastconf.UnknownError),
		fastconf.WithPolicy(policy.Func[Config]{N: "port-policy", Fn: func(_ context.Context, in policy.Input[Config]) ([]policy.Violation, error) {
			if in.Config.Port == 7070 {
				return []policy.Violation{{Rule: "reserved-port", Path: "port", Severity: policy.SeverityError}}, nil
			}
			if in.Config.Port == 9090 {
				return []policy.Violation{{Rule: "review-port", Path: "port", Severity: policy.SeverityWarning}}, nil
			}
			return nil, nil
		}}),
	)
	if err != nil {
		return err
	}
	defer func() { _ = mgr.Close() }()
	initial := mgr.Snapshot()
	if err := write("invalid"); err != nil {
		return err
	}
	preview, err := mgr.Plan(ctx)
	if err != nil {
		return err
	}
	if err := checkPlan(preview); err == nil {
		return fmt.Errorf("invalid preview was accepted")
	}
	violations := 0
	for _, report := range preview.Validators {
		if report.Err != nil {
			violations++
		}
	}
	if _, err := fmt.Fprintf(out, "invalid preview: violations=%d live=%d\n", violations, mgr.Get().Port); err != nil {
		return err
	}
	if err := mgr.Reload(ctx); !errors.Is(err, fastconf.ErrInvalid) {
		return fmt.Errorf("expected validation failure, got %v", err)
	}
	if _, err := fmt.Fprintf(out, "rejected: preserved=%t\n", mgr.Snapshot() == initial); err != nil {
		return err
	}
	if err := write("policy-invalid"); err != nil {
		return err
	}
	preview, err = mgr.Plan(ctx)
	if err != nil {
		return err
	}
	if err := checkPlan(preview); err == nil {
		return fmt.Errorf("policy preview was accepted")
	}
	if _, err := fmt.Fprintf(out, "policy preview: rejected=true live=%d\n", mgr.Get().Port); err != nil {
		return err
	}
	if err := write("proposed"); err != nil {
		return err
	}
	preview, err = mgr.Plan(ctx)
	if err != nil {
		return err
	}
	if err := checkPlan(preview); err != nil {
		return err
	}
	if err := printChange(out, mgr.Snapshot(), preview); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "valid preview: proposed=%d live=%d changes=%d\n", preview.Proposed.Value().Port, mgr.Get().Port, len(preview.Diff)); err != nil {
		return err
	}
	if err := mgr.Reload(ctx); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "committed: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation()); err != nil {
		return err
	}
	if err := mgr.History().Rollback(initial); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "rollback: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation()); err != nil {
		return err
	}

	// The file is still proposed.yaml: restoring memory does not repair it.
	if err := mgr.Reload(ctx); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "unchanged source: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation()); err != nil {
		return err
	}
	if err := write("initial"); err != nil {
		return err
	}
	preview, err = mgr.Plan(ctx)
	if err != nil {
		return err
	}
	if err := checkPlan(preview); err != nil {
		return err
	}
	if err := mgr.Reload(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "source repaired: port=%d generation=%d\n", mgr.Get().Port, mgr.Snapshot().Generation())
	return err
}

// A nil Plan error only means a preview was produced. Inspect both report sets.
func checkPlan[T any](p *fastconf.PlanResult[T]) error {
	for _, r := range p.Validators {
		if r.Err != nil {
			return fmt.Errorf("validator %s: %w", r.Name, r.Err)
		}
	}
	for _, v := range p.Policies {
		if v.Severity == policy.SeverityError {
			return fmt.Errorf("policy %s rejected %s", v.Rule, v.Path)
		}
	}
	return nil
}

func printChange[T any](out io.Writer, before *fastconf.State[T], p *fastconf.PlanResult[T]) error {
	changed := before.Hash() != p.Proposed.Hash()
	if changed && len(p.Diff) == 0 {
		_, err := fmt.Fprintln(out, "configuration changed; no visible JSON field differences")
		return err
	}
	_, err := fmt.Fprintf(out, "content changed=%t warnings=%d\n", changed, len(p.Policies))
	return err
}

func main() {
	if err := run(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
