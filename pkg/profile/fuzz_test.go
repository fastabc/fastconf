package profile

import (
	"testing"
)

// FuzzExpr ensures the profile expression parser never panics on
// arbitrary input and always returns either a valid compiled function
// or a clean error.
func FuzzExpr(f *testing.F) {
	// Seed corpus: valid expressions.
	f.Add("prod")
	f.Add("prod & eu")
	f.Add("prod | staging")
	f.Add("!canary")
	f.Add("prod & (eu | us)")
	f.Add("prod & !canary")
	f.Add("(a | b) & !(c & d)")
	// Edge cases.
	f.Add("")
	f.Add("(")
	f.Add(")")
	f.Add("&&")
	f.Add("||")
	f.Add("!!")
	f.Add("a b c")

	f.Fuzz(func(t *testing.T, expr string) {
		fn, err := Compile(expr)
		if err != nil {
			return
		}
		active := NewSet("prod", "eu")
		_ = fn(active)
	})
}
