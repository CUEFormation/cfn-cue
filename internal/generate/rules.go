package generate

import (
	"fmt"
	"regexp"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/token"
)

// fieldRules are presence rules between the properties of one object. They
// are emitted as CUE instead of JSON Schema so failures name the fields:
// "B cannot be used with A" rather than "2 matched, expected 1".
type fieldRules struct {
	excluded [][3]string // field, other field, message suffix
	atLeast  [][]string  // at least one field of each group must be set
}

// A placeholder property marks the object's struct for applyRules.
const ruleMarker = "__cfn_field_rules_"

// Rule fields become identifier labels so comprehensions can reference
// them. Imports, builtins and generated definitions never start with an
// uppercase letter, so these labels cannot capture other references.
var ruleName = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// ruleNames reports whether each name is a property of s that rules can
// reference by identifier.
func ruleNames(s map[string]any, names []string) bool {
	props, _ := s["properties"].(map[string]any)
	for _, n := range names {
		if _, ok := props[n]; !ok || !ruleName.MatchString(n) {
			return false
		}
	}
	return len(names) > 0
}

// singleRequired returns the names of a oneOf/anyOf whose alternatives are
// each {"required": [name]}.
func singleRequired(v any) []string {
	var names []string
	for _, x := range asArray(v) {
		m, ok := x.(map[string]any)
		req := asArray(m["required"])
		if !ok || len(m) != 1 || len(req) != 1 {
			return nil
		}
		name, ok := req[0].(string)
		if !ok {
			return nil
		}
		names = append(names, name)
	}
	return names
}

func isObject(v any) bool { _, ok := v.(map[string]any); return ok }

// excludedNames lists every property named by a dependentExcluded map.
func excludedNames(v any) []string {
	m := v.(map[string]any)
	names := keys(m)
	for _, k := range keys(m) {
		names = append(names, stringsOf(m[k])...)
	}
	return names
}

func stringsOf(v any) []string {
	var out []string
	for _, x := range asArray(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (r *fieldRules) exactlyOne(names []string) {
	set := "; set exactly one of " + strings.Join(names, ", ")
	for i, a := range names {
		for _, b := range names[i+1:] {
			r.excluded = append(r.excluded, [3]string{a, b, set})
		}
	}
	r.atLeast = append(r.atLeast, names)
}

// exclude records a dependentExcluded map once per unordered pair.
func (r *fieldRules) exclude(m map[string]any) {
	seen := map[[2]string]bool{}
	for _, a := range keys(m) {
		for _, b := range stringsOf(m[a]) {
			if !seen[[2]string{b, a}] {
				seen[[2]string{a, b}] = true
				r.excluded = append(r.excluded, [3]string{a, b, ""})
			}
		}
	}
}

// mark adds a placeholder property for r to normalized object m.
func (c *converter) mark(m map[string]any, r *fieldRules) {
	if len(r.excluded) == 0 && len(r.atLeast) == 0 {
		return
	}
	props, _ := m["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		m["properties"] = props
	}
	props[fmt.Sprintf("%s%d", ruleMarker, len(c.rules))] = map[string]any{}
	c.rules = append(c.rules, r)
}

// applyRules replaces each placeholder with comprehensions, and conjoins
// at-least-one checks with the struct. matchN stays outside the struct:
// embedded in a definition it would admit undeclared fields.
func (c *converter) applyRules(f *ast.File) error {
	placed := map[int]bool{}
	wrap := map[*ast.StructLit]ast.Expr{}
	astutil.Apply(f, nil, func(cur astutil.Cursor) bool {
		switch n := cur.Node().(type) {
		case *ast.StructLit:
			for i, d := range n.Elts {
				field, ok := d.(*ast.Field)
				if !ok {
					continue
				}
				name, _, _ := ast.LabelName(field.Label)
				var index int
				if _, e := fmt.Sscanf(name, ruleMarker+"%d", &index); e != nil || index >= len(c.rules) {
					continue
				}
				placed[index] = true
				decls, check := c.rules[index].cue()
				n.Elts = append(n.Elts[:i], append(decls, n.Elts[i+1:]...)...)
				// The extractor quotes labels, which do not bind identifiers.
				for _, d := range n.Elts {
					if f, ok := d.(*ast.Field); ok {
						if name, _, _ := ast.LabelName(f.Label); c.rules[index].names()[name] {
							f.Label = ast.NewIdent(name)
						}
					}
				}
				if check == nil {
					break
				}
				if call, ok := cur.Parent().Node().(*ast.CallExpr); ok && isIdent(call.Fun, "close") {
					wrap[n] = check
				} else {
					cur.Replace(and(n, check))
				}
				break
			}
		case *ast.CallExpr:
			if len(n.Args) == 1 && isIdent(n.Fun, "close") {
				if s, ok := n.Args[0].(*ast.StructLit); ok && wrap[s] != nil {
					cur.Replace(and(n, wrap[s]))
					delete(wrap, s)
				}
			}
		}
		return true
	})
	for i, r := range c.rules {
		if r != nil && !placed[i] {
			return fmt.Errorf("field rule %d was not placed by the JSON Schema extractor", i)
		}
	}
	return nil
}

func isIdent(x ast.Expr, name string) bool { i, ok := x.(*ast.Ident); return ok && i.Name == name }

func (r *fieldRules) names() map[string]bool {
	out := map[string]bool{}
	for _, e := range r.excluded {
		out[e[0]], out[e[1]] = true, true
	}
	for _, g := range r.atLeast {
		for _, n := range g {
			out[n] = true
		}
	}
	return out
}

// conditional matches an Fn::If. Either branch may be AWS::NoValue, so a
// conditional property may be absent after CloudFormation resolves it, and an
// exclusion between it and another property cannot be decided.
func conditional() ast.Expr {
	return ast.NewCall(id("close"), &ast.StructLit{Elts: []ast.Decl{&ast.Field{Label: ast.NewString("Fn::If"), Value: id("_")}}})
}

func (r *fieldRules) cue() ([]ast.Decl, ast.Expr) {
	var decls []ast.Decl
	for i := 0; i < len(r.excluded); {
		a := r.excluded[i][0]
		body := &ast.StructLit{}
		for ; i < len(r.excluded) && r.excluded[i][0] == a; i++ {
			b, suffix := r.excluded[i][1], r.excluded[i][2]
			body.Elts = append(body.Elts, field(b, union(conditional(), ast.NewCall(id("error"), str(b+" cannot be used with "+a+suffix))), true))
		}
		decls = append(decls, &ast.Comprehension{
			Clauses: []ast.Clause{
				&ast.IfClause{Condition: &ast.BinaryExpr{X: id(a), Op: token.NEQ, Y: &ast.BottomLit{}}},
				&ast.IfClause{Condition: &ast.BinaryExpr{X: &ast.SelectorExpr{X: id(a), Sel: ast.NewString("Fn::If")}, Op: token.EQL, Y: &ast.BottomLit{}}},
			},
			Value: body,
		})
	}
	var checks []ast.Expr
	for _, names := range r.atLeast {
		alternatives := []ast.Expr{}
		for _, n := range names {
			alternatives = append(alternatives, obj(field(n, id("_"), false)))
		}
		checks = append(checks, ast.NewCall(id("matchN"), &ast.UnaryExpr{Op: token.GEQ, X: ast.NewLit(token.INT, "1")}, ast.NewList(alternatives...)))
	}
	if len(checks) == 0 {
		return decls, nil
	}
	return decls, and(checks...)
}
