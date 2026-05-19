// Package profile implements FastConf's tiny boolean profile-expression
// language. An expression is a propositional formula over
// profile names with the operators '&' (AND), '|' (OR), '!' (NOT) and
// parentheses. An identifier evaluates to true iff it is in the active
// profile set.
//
// Examples:
//
//	"prod"                  → active("prod")
//	"prod & eu"             → active("prod") && active("eu")
//	"prod & (eu | us)"      → active("prod") && (active("eu") || active("us"))
//	"prod & !canary"        → active("prod") && !active("canary")
//
// The parser is recursive-descent and dependency-free; FastConf's main
// module never grows a third-party expression library.
package profile

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Set is a small string-set helper used by the evaluator.
type Set map[string]struct{}

// NewSet builds a Set from the given profile names.
func NewSet(active ...string) Set {
	s := make(Set, len(active))
	for _, a := range active {
		a = strings.TrimSpace(a)
		if a != "" {
			s[a] = struct{}{}
		}
	}
	return s
}

// Has reports whether name is a member of the active set.
func (s Set) Has(name string) bool { _, ok := s[name]; return ok }

// Eval parses and evaluates the expression against the active set.
func Eval(expr string, active Set) (bool, error) {
	fn, err := Compile(expr)
	if err != nil {
		return false, err
	}
	return fn(active), nil
}

// Compile parses expr once and returns a reusable evaluator. An empty
// expression compiles to a constant-true predicate, which lets callers
// treat "no `match:` field in _meta.yaml" as "always match" without a
// nil check. Callers can also use Compile to validate user-supplied
// expressions at startup time and surface errors before the first
// reload.
func Compile(expr string) (func(Set) bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return func(Set) bool { return true }, nil
	}
	p := &parser{src: expr}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("profile: trailing input at %d: %q", p.pos, p.src[p.pos:])
	}
	return func(s Set) bool { return v(s) }, nil
}

type evalFn func(Set) bool

type parser struct {
	src string
	pos int
}

func (p *parser) peek() byte {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) parseExpr() (evalFn, error) { return p.parseOr() }

func (p *parser) parseOr() (evalFn, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek() == '|' {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(s Set) bool { return l(s) || r(s) }
	}
	return left, nil
}

func (p *parser) parseAnd() (evalFn, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.peek() == '&' {
		p.pos++
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(s Set) bool { return l(s) && r(s) }
	}
	return left, nil
}

func (p *parser) parseNot() (evalFn, error) {
	if p.peek() == '!' {
		p.pos++
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return func(s Set) bool { return !inner(s) }, nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (evalFn, error) {
	c := p.peek()
	if c == 0 {
		return nil, errors.New("profile: unexpected end of expression")
	}
	if c == '(' {
		p.pos++
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("profile: missing ')' at %d", p.pos)
		}
		p.pos++
		return inner, nil
	}
	id, err := p.parseIdent()
	if err != nil {
		return nil, err
	}
	return func(s Set) bool { return s.Has(id) }, nil
}

func (p *parser) parseIdent() (string, error) {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if isIdentChar(c) {
			p.pos++
			continue
		}
		break
	}
	if p.pos == start {
		return "", fmt.Errorf("profile: expected identifier at %d", p.pos)
	}
	return p.src[start:p.pos], nil
}

func isIdentChar(c byte) bool {
	return c == '_' || c == '-' || c == '.' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}
