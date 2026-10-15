package semantic

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Truth classifies a condition formula without choosing parameter values.
type Truth int

const (
	// Unknown means the formula has too many unresolved comparisons to decide.
	Unknown Truth = iota
	// Never means no assignment of the unresolved values makes it true.
	Never
	// Sometimes means the result depends on unresolved values.
	Sometimes
	// Always means every assignment of the unresolved values makes it true.
	Always
)

func (t Truth) String() string {
	return [...]string{"unknown", "never", "sometimes", "always"}[t]
}

// maxAtoms bounds the truth-table enumeration in Classify.
const maxAtoms = 16

// formula is a boolean expression over atoms. An atom is a comparison whose
// operands are not both literal, so its value is unresolved here.
type formula struct {
	op   byte // 'c' constant, 'a' atom, '!' not, '&' and, '|' or
	val  bool
	atom int
	args []*formula
}

var (
	alwaysTrue  = &formula{op: 'c', val: true}
	alwaysFalse = &formula{op: 'c', val: false}
)

func not(f *formula) *formula {
	if f.op == 'c' {
		return &formula{op: 'c', val: !f.val}
	}
	return &formula{op: '!', args: []*formula{f}}
}

func and(fs ...*formula) *formula { return combine('&', fs) }
func or(fs ...*formula) *formula  { return combine('|', fs) }

func combine(op byte, fs []*formula) *formula {
	// Identity for and is true; for or it is false.
	identity := op == '&'
	var args []*formula
	for _, f := range fs {
		if f.op == 'c' {
			if f.val != identity {
				return &formula{op: 'c', val: f.val}
			}
			continue
		}
		args = append(args, f)
	}
	switch len(args) {
	case 0:
		return &formula{op: 'c', val: identity}
	case 1:
		return args[0]
	}
	return &formula{op: op, args: args}
}

func (f *formula) eval(assign uint32) bool {
	switch f.op {
	case 'c':
		return f.val
	case 'a':
		return assign&(1<<f.atom) != 0
	case '!':
		return !f.args[0].eval(assign)
	case '&':
		for _, a := range f.args {
			if !a.eval(assign) {
				return false
			}
		}
		return true
	default:
		for _, a := range f.args {
			if a.eval(assign) {
				return true
			}
		}
		return false
	}
}

func (f *formula) atoms(seen map[int]bool) {
	if f.op == 'a' {
		seen[f.atom] = true
	}
	for _, a := range f.args {
		a.atoms(seen)
	}
}

// atom records one unresolved comparison. When one operand is literal, two
// atoms comparing the same expression with different literals exclude each
// other: one value cannot equal both.
type atom struct {
	key     string
	symbol  string
	literal *string
}

// Conditions compiles a template's Conditions section into formulas. Fn::Equals
// over two literals is evaluated; a comparison involving a parameter, pseudo
// parameter or other intrinsic stays unresolved. Identical expressions are
// equal to themselves because intrinsics are deterministic within one stack
// operation. Parameter defaults are not values: a deployment may override them.
// AllowedValues are constraints the template declares, so they narrow a
// comparison's possible results.
type Conditions struct {
	defs map[string]any
	// allowed maps a parameter's Ref, in canonical JSON, to its declared
	// AllowedValues. A deployment must choose one of them.
	allowed  map[string]map[string]bool
	compiled map[string]*formula
	state    map[string]int
	atoms    []atom
	index    map[string]int
	cycles   []string
}

func newConditions(defs map[string]any, allowed map[string]map[string]bool) *Conditions {
	c := &Conditions{defs: defs, allowed: allowed, compiled: map[string]*formula{}, state: map[string]int{}, index: map[string]int{}}
	for _, name := range sortedKeys(defs) {
		c.named(name)
	}
	return c
}

// Defined reports whether the template declares the condition.
func (c *Conditions) Defined(name string) bool {
	_, ok := c.defs[name]
	return ok
}

// Guard is one Fn::If branch or Condition attribute on the path to a value:
// the value exists only when Condition evaluates to Value.
type Guard struct {
	Condition string `json:"condition"`
	Value     bool   `json:"value"`
}

// Classify reports whether every guard can hold together.
func (c *Conditions) Classify(guards []Guard) Truth {
	return c.classify(c.guards(guards))
}

func (c *Conditions) guards(guards []Guard) *formula {
	fs := make([]*formula, 0, len(guards))
	for _, g := range guards {
		f := c.named(g.Condition)
		if !g.Value {
			f = not(f)
		}
		fs = append(fs, f)
	}
	return and(fs...)
}

func (c *Conditions) classify(f *formula) Truth {
	if f.op == 'c' {
		if f.val {
			return Always
		}
		return Never
	}
	seen := map[int]bool{}
	f.atoms(seen)
	if len(seen) > maxAtoms {
		return Unknown
	}
	atoms := make([]int, 0, len(seen))
	for a := range seen {
		atoms = append(atoms, a)
	}
	sort.Ints(atoms)
	// A parameter with AllowedValues must equal one of them. If the formula
	// compares it with every allowed value, one comparison must hold.
	compared := map[string]map[string]bool{}
	for _, a := range atoms {
		if at := c.atoms[a]; at.literal != nil && c.allowed[at.symbol] != nil {
			if compared[at.symbol] == nil {
				compared[at.symbol] = map[string]bool{}
			}
			compared[at.symbol][*at.literal] = true
		}
	}
	var exhaustive []string
	for symbol, literals := range compared {
		covered := true
		for value := range c.allowed[symbol] {
			covered = covered && literals[value]
		}
		if covered {
			exhaustive = append(exhaustive, symbol)
		}
	}
	var anyTrue, anyFalse bool
	for bits := uint32(0); bits < 1<<len(atoms); bits++ {
		var assign uint32
		literal := map[string]string{}
		feasible := true
		for i, a := range atoms {
			if bits&(1<<i) == 0 {
				continue
			}
			assign |= 1 << a
			if l := c.atoms[a].literal; l != nil {
				symbol := c.atoms[a].symbol
				if prior, ok := literal[symbol]; ok && prior != *l {
					feasible = false
					break
				}
				if allowed := c.allowed[symbol]; allowed != nil && !allowed[*l] {
					feasible = false
					break
				}
				literal[symbol] = *l
			}
		}
		for _, symbol := range exhaustive {
			if _, ok := literal[symbol]; !ok {
				feasible = false
			}
		}
		if !feasible {
			continue
		}
		if f.eval(assign) {
			anyTrue = true
		} else {
			anyFalse = true
		}
		if anyTrue && anyFalse {
			return Sometimes
		}
	}
	switch {
	case anyTrue:
		return Always
	case anyFalse:
		return Never
	}
	return Unknown
}

// named compiles a condition by name. An undefined or cyclic condition
// becomes its own unresolved atom; the checks report those separately.
func (c *Conditions) named(name string) *formula {
	if f, ok := c.compiled[name]; ok {
		return f
	}
	def, ok := c.defs[name]
	if !ok {
		return c.atom("undefined:"+name, "", nil)
	}
	if c.state[name] == 1 {
		c.cycles = append(c.cycles, name)
		return c.atom("cycle:"+name, "", nil)
	}
	c.state[name] = 1
	f := c.expr(def)
	c.state[name] = 2
	c.compiled[name] = f
	return f
}

func (c *Conditions) expr(v any) *formula {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return c.opaque(v)
	}
	for key, arg := range m {
		args, _ := arg.([]any)
		switch key {
		case "Condition":
			if name, ok := arg.(string); ok {
				return c.named(name)
			}
		case "Fn::Not":
			if len(args) == 1 {
				return not(c.expr(args[0]))
			}
		case "Fn::And", "Fn::Or":
			if len(args) == 0 {
				break
			}
			fs := make([]*formula, len(args))
			for i, a := range args {
				fs[i] = c.expr(a)
			}
			if key == "Fn::And" {
				return and(fs...)
			}
			return or(fs...)
		case "Fn::Equals":
			if len(args) == 2 {
				return c.equals(args[0], args[1])
			}
		}
	}
	return c.opaque(v)
}

func (c *Conditions) equals(a, b any) *formula {
	la, okA := literal(a)
	lb, okB := literal(b)
	switch {
	case okA && okB:
		if la == lb {
			return alwaysTrue
		}
		return alwaysFalse
	case okA:
		return c.atom(canonical(b)+"=="+canonical(la), canonical(b), &la)
	case okB:
		return c.atom(canonical(a)+"=="+canonical(lb), canonical(a), &lb)
	}
	x, y := canonical(a), canonical(b)
	if x == y {
		return alwaysTrue
	}
	if y < x {
		x, y = y, x
	}
	return c.atom(x+"=="+y, "", nil)
}

// opaque is a well-formed but unanalyzed expression; the schema reports
// malformed condition functions.
func (c *Conditions) opaque(v any) *formula { return c.atom("opaque:"+canonical(v), "", nil) }

func (c *Conditions) atom(key, symbol string, literal *string) *formula {
	i, ok := c.index[key]
	if !ok {
		i = len(c.atoms)
		c.atoms = append(c.atoms, atom{key, symbol, literal})
		c.index[key] = i
	}
	return &formula{op: 'a', atom: i}
}

// literal returns the string CloudFormation compares for a scalar.
func literal(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case bool:
		return fmt.Sprint(v), true
	case json.Number:
		return v.String(), true
	case float64:
		b, _ := json.Marshal(v)
		return string(b), true
	}
	return "", false
}

func canonical(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
