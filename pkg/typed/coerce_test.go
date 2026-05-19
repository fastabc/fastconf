package typed_test

import (
	"testing"

	"github.com/fastabc/fastconf/pkg/typed"
)

func TestCoerce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		opts typed.CoerceOptions
		want any
	}{
		{name: "bool lowercase exact", in: "true", want: true},
		{name: "bool false", in: "false", want: false},
		{name: "int positive", in: "42", want: int64(42)},
		{name: "int negative", in: "-7", want: int64(-7)},
		{name: "float", in: "1.5", want: 1.5},
		{name: "plain string passthrough", in: "hello", want: "hello"},
		{name: "uppercase bool without ignore_case stays string", in: "TRUE", want: "TRUE"},
		{
			name: "uppercase bool with ignore_case parses",
			in:   "TRUE",
			opts: typed.CoerceOptions{IgnoreCase: true},
			want: true,
		},
		{
			name: "whitespace bool needs trim",
			in:   " true ",
			opts: typed.CoerceOptions{TrimSpace: true},
			want: true,
		},
		{
			name: "whitespace bool without trim stays string",
			in:   " true ",
			want: " true ",
		},
		{
			name: "trim returns trimmed string fallback",
			in:   "  hello  ",
			opts: typed.CoerceOptions{TrimSpace: true},
			want: "hello",
		},
		{
			name: "mixed-case bool with both opts",
			in:   "  True  ",
			opts: typed.CoerceOptions{TrimSpace: true, IgnoreCase: true},
			want: true,
		},
		{
			name: "leading-zero int",
			in:   "007",
			want: int64(7),
		},
		{
			name: "exponent float",
			in:   "1e3",
			want: 1000.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := typed.Coerce(tt.in, tt.opts)
			if got != tt.want {
				t.Fatalf("Coerce(%q, %+v) = %v (%T); want %v (%T)", tt.in, tt.opts, got, got, tt.want, tt.want)
			}
		})
	}
}
