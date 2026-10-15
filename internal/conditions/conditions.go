// Package conditions decides which combinations of CloudFormation condition
// values a template can produce. It does not resolve parameters or other
// template values; it treats each comparison it cannot decide as unknown.
//
// The model is propositional. Fn::And, Fn::Or, Fn::Not and Condition are
// followed exactly. Fn::Equals is modelled as follows:
//
//   - Two scalar literals are compared by their string forms, so the result is
//     a known boolean.
//   - An expression compared with a scalar literal becomes a finite-domain
//     variable. One expression cannot equal two different literals. A Ref to a
//     String or Number parameter with AllowedValues takes only those values.
//   - Two identical expressions are equal. Any other comparison is an
//     independent unknown boolean.
//
// A condition that is not declared, refers to itself, or has an unrecognized
// shape is an independent unknown boolean, never true or false.
package conditions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Literal fixes one named condition to a value.
type Literal struct {
	Condition string
	Value     bool
}

func (l Literal) String() string {
	return fmt.Sprintf("%s is %t", l.Condition, l.Value)
}

// MaxAssignments bounds the assignments of underlying comparisons examined by
// one query. Larger queries are reported as undecided.
const MaxAssignments = 1 << 14

// Model holds a template's condition definitions.
type Model struct {
	defs     map[string]any
	allowed  map[string][]string
	compiled map[string]formula
}

// New reads Conditions and Parameters from a decoded template. Values must be
// decoded with json.Decoder.UseNumber so numeric literals keep their spelling.
func New(template map[string]any) *Model {
	m := &Model{defs: map[string]any{}, allowed: map[string][]string{}, compiled: map[string]formula{}}
	if defs, ok := template["Conditions"].(map[string]any); ok {
		m.defs = defs
	}
	params, _ := template["Parameters"].(map[string]any)
	for name, raw := range params {
		param, _ := raw.(map[string]any)
		typ, _ := param["Type"].(string)
		values, _ := param["AllowedValues"].([]any)
		if (typ != "String" && typ != "Number") || len(values) == 0 {
			continue
		}
		var domain []string
		for _, value := range values {
			s, ok := scalar(value)
			if !ok {
				domain = nil
				break
			}
			domain = append(domain, s)
		}
		if domain != nil {
			m.allowed[canonical(map[string]any{"Ref": name})] = domain
		}
	}
	return m
}

// Declared reports whether the template declares the named condition.
func (m *Model) Declared(name string) bool {
	_, ok := m.defs[name]
	return ok
}

// Combinations returns every assignment of names that the template can
// produce while each literal in given holds. Each result has one value per
// name, in the order of names. ok is false when the query is too large to
// decide; the result is then nil.
func (m *Model) Combinations(names []string, given []Literal) (combos [][]bool, ok bool) {
	var constraint formula = constant(true)
	for _, literal := range given {
		var f formula = m.condition(literal.Condition)
		if !literal.Value {
			f = not{f}
		}
		constraint = and{constraint, f}
	}
	targets := make([]formula, len(names))
	for i, name := range names {
		targets[i] = m.condition(name)
	}
	vars := newVariables()
	vars.collect(constraint)
	for _, target := range targets {
		vars.collect(target)
	}
	seen := map[string]bool{}
	ok = vars.each(func(a assignment) {
		if !constraint.eval(a) {
			return
		}
		combo := make([]bool, len(targets))
		key := make([]byte, len(targets))
		for i, target := range targets {
			combo[i] = target.eval(a)
			key[i] = '0'
			if combo[i] {
				key[i] = '1'
			}
		}
		if !seen[string(key)] {
			seen[string(key)] = true
			combos = append(combos, combo)
		}
	})
	if !ok {
		return nil, false
	}
	sort.Slice(combos, func(i, j int) bool {
		for k := range combos[i] {
			if combos[i][k] != combos[j][k] {
				return !combos[i][k]
			}
		}
		return false
	})
	return combos, true
}

// Satisfiable reports whether every literal can hold at once. decided is false
// when the query is too large to decide.
func (m *Model) Satisfiable(literals []Literal) (satisfiable, decided bool) {
	combos, ok := m.Combinations(nil, literals)
	return len(combos) > 0, ok
}

// condition compiles a named condition. A cycle or an undeclared name becomes
// an unknown boolean keyed by the name.
func (m *Model) condition(name string) formula {
	if f, ok := m.compiled[name]; ok {
		if f == nil {
			return unknown("condition:" + name)
		}
		return f
	}
	def, ok := m.defs[name]
	if !ok {
		return unknown("condition:" + name)
	}
	// A nil entry marks the definition in progress, so a reference back to it
	// becomes unknown instead of recursing.
	m.compiled[name] = nil
	f := m.compile(def)
	m.compiled[name] = f
	return f
}

func (m *Model) compile(node any) formula {
	fn, args, ok := function(node)
	if !ok {
		return unknown("expression:" + canonical(node))
	}
	list, _ := args.([]any)
	switch fn {
	case "Condition":
		if name, ok := args.(string); ok {
			return m.condition(name)
		}
	case "Fn::Not":
		if len(list) == 1 {
			return not{m.compile(list[0])}
		}
	case "Fn::And", "Fn::Or":
		if len(list) >= 2 && len(list) <= 10 {
			var out formula = constant(fn == "Fn::And")
			for _, arg := range list {
				if fn == "Fn::And" {
					out = and{out, m.compile(arg)}
				} else {
					out = or{out, m.compile(arg)}
				}
			}
			return out
		}
	case "Fn::Equals":
		if len(list) == 2 {
			return m.equals(list[0], list[1])
		}
	}
	return unknown("expression:" + canonical(node))
}

func (m *Model) equals(a, b any) formula {
	sa, aScalar := scalar(a)
	sb, bScalar := scalar(b)
	switch {
	case aScalar && bScalar:
		return constant(sa == sb)
	case aScalar:
		return m.member(b, sa)
	case bScalar:
		return m.member(a, sb)
	}
	ca, cb := canonical(a), canonical(b)
	if ca == cb {
		return constant(true)
	}
	if cb < ca {
		ca, cb = cb, ca
	}
	return unknown("equals:" + ca + "=" + cb)
}

func (m *Model) member(expr any, value string) formula {
	key := canonical(expr)
	if domain, ok := m.allowed[key]; ok {
		found := false
		for _, allowed := range domain {
			found = found || allowed == value
		}
		if !found {
			return constant(false)
		}
		return is{key: key, value: value, domain: domain}
	}
	return is{key: key, value: value}
}

// scalar returns the string form CloudFormation compares for a literal.
func scalar(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case json.Number:
		return value.String(), true
	case float64:
		b, _ := json.Marshal(value)
		return string(b), true
	case bool:
		if value {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

func function(node any) (string, any, bool) {
	object, ok := node.(map[string]any)
	if !ok || len(object) != 1 {
		return "", nil, false
	}
	for key, value := range object {
		return key, value, true
	}
	return "", nil, false
}

// canonical gives decoded JSON a stable spelling. encoding/json sorts keys.
func canonical(value any) string {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(bytes.TrimSpace(b.Bytes()))
}

type assignment struct {
	unknowns map[string]bool
	values   map[string]string
}

type formula interface {
	eval(assignment) bool
}

type constant bool
type unknown string
type not struct{ f formula }
type and struct{ a, b formula }
type or struct{ a, b formula }

// is holds when the expression identified by key has value. An empty domain
// means the expression may also have a value no comparison names.
type is struct {
	key, value string
	domain     []string
}

func (f constant) eval(assignment) bool  { return bool(f) }
func (f unknown) eval(a assignment) bool { return a.unknowns[string(f)] }
func (f not) eval(a assignment) bool     { return !f.f.eval(a) }
func (f and) eval(a assignment) bool     { return f.a.eval(a) && f.b.eval(a) }
func (f or) eval(a assignment) bool      { return f.a.eval(a) || f.b.eval(a) }
func (f is) eval(a assignment) bool      { return a.values[f.key] == f.value }

type variables struct {
	unknowns []string
	seen     map[string]bool
	domains  map[string]map[string]bool
	closed   map[string]bool
}

func newVariables() *variables {
	return &variables{seen: map[string]bool{}, domains: map[string]map[string]bool{}, closed: map[string]bool{}}
}

func (v *variables) collect(f formula) {
	switch f := f.(type) {
	case unknown:
		if !v.seen[string(f)] {
			v.seen[string(f)] = true
			v.unknowns = append(v.unknowns, string(f))
		}
	case not:
		v.collect(f.f)
	case and:
		v.collect(f.a)
		v.collect(f.b)
	case or:
		v.collect(f.a)
		v.collect(f.b)
	case is:
		if v.domains[f.key] == nil {
			v.domains[f.key] = map[string]bool{}
		}
		if f.domain != nil {
			v.closed[f.key] = true
			for _, value := range f.domain {
				v.domains[f.key][value] = true
			}
		} else {
			v.domains[f.key][f.value] = true
		}
	}
}

// each calls visit for every assignment, or returns false without calling it
// when there are more than MaxAssignments.
func (v *variables) each(visit func(assignment)) bool {
	keys := make([]string, 0, len(v.domains))
	for key := range v.domains {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.Strings(v.unknowns)
	choices := make([][]string, len(keys))
	total := 1
	for i, key := range keys {
		for value := range v.domains[key] {
			choices[i] = append(choices[i], value)
		}
		sort.Strings(choices[i])
		if !v.closed[key] {
			// A value that no comparison names. Real values cannot contain
			// NUL, so this sentinel never equals a literal.
			choices[i] = append(choices[i], "\x00other")
		}
		total *= len(choices[i])
		if total > MaxAssignments {
			return false
		}
	}
	for range v.unknowns {
		total *= 2
		if total > MaxAssignments {
			return false
		}
	}
	a := assignment{unknowns: map[string]bool{}, values: map[string]string{}}
	var walk func(int)
	walk = func(i int) {
		if i < len(keys) {
			for _, value := range choices[i] {
				a.values[keys[i]] = value
				walk(i + 1)
			}
			return
		}
		if j := i - len(keys); j < len(v.unknowns) {
			for _, value := range []bool{false, true} {
				a.unknowns[v.unknowns[j]] = value
				walk(i + 1)
			}
			return
		}
		visit(a)
	}
	walk(0)
	return true
}
