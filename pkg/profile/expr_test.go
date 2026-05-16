package profile

import "testing"

func TestEval(t *testing.T) {
	cases := []struct {
		expr   string
		active []string
		want   bool
	}{
		{"prod", []string{"prod"}, true},
		{"prod", []string{"dev"}, false},
		{"prod & eu", []string{"prod", "eu"}, true},
		{"prod & eu", []string{"prod"}, false},
		{"prod | eu", []string{"eu"}, true},
		{"prod | eu", []string{"us"}, false},
		{"prod & (eu | us)", []string{"prod", "us"}, true},
		{"prod & !canary", []string{"prod"}, true},
		{"prod & !canary", []string{"prod", "canary"}, false},
		{"!(prod & canary)", []string{"prod"}, true},
		{"", []string{}, true},
		{"  prod  ", []string{"prod"}, true},
		{"a-b.c_d", []string{"a-b.c_d"}, true},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, NewSet(c.active...))
		if err != nil {
			t.Fatalf("Eval(%q): %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("Eval(%q, %v) = %v, want %v", c.expr, c.active, got, c.want)
		}
	}
}

func TestEvalErrors(t *testing.T) {
	bad := []string{"prod &", "(prod", "prod ) extra", "& prod", "!"}
	for _, e := range bad {
		if _, err := Eval(e, NewSet("prod")); err == nil {
			t.Fatalf("Eval(%q) expected error", e)
		}
	}
}
