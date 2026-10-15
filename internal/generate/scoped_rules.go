package generate

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"
)

// Scoped rules are cfn-lint rules compiled to CUE comprehensions instead of
// matchIf validators. A validator re-evaluates the whole resource, so a rule
// with several conditions repeats the resource's full schema for each one. A
// scoped rule reads only the fields it names. Comprehension clauses test
// field presence, and values are compared or passed to builtins: unifying a
// field with a type would evaluate the field's schema again. A failing rule
// embeds error("<rule>: <message>") in the properties struct.
//
// The compiled predicate keeps the semantics applyExtensions documents for
// validator rules: const and enum compare loosely, a numeric condition is not
// met by a string, an intrinsic never satisfies a condition, and cfn-lint's
// lowercased Engine comparisons are case-insensitive. Outside conditions an
// intrinsic or dynamic reference satisfies every constraint, and a numeric
// string is compared as a number, as cfn-lint does.

// ruleCompiler compiles one rule. Root names are the properties the rule
// reads at the top level; they must be identifier labels in the struct.
type ruleCompiler struct {
	rule, message   string
	lowercaseEngine bool
	// template admits intrinsics and dynamic references outside conditions.
	template   bool
	properties map[string]any
	names      map[string]bool
	// tested collects the values a condition compares, while one is built.
	tested map[string]valueRef
	vars   int
}

// valueRef is a value the rule reads: the properties struct itself (root),
// one of its fields, or a path below a comprehension variable.
type valueRef struct {
	root     bool
	local    bool // reads a comprehension variable
	expr     ast.Expr
	property string // the last property name, for Engine comparisons
}

func (r *ruleCompiler) child(x valueRef, name string) (valueRef, error) {
	if x.root {
		props, _ := r.properties["properties"].(map[string]any)
		if _, ok := props[name]; !ok || !ruleName.MatchString(name) {
			return valueRef{}, fmt.Errorf("property %q cannot be referenced", name)
		}
		r.names[name] = true
		return valueRef{expr: id(name), property: name}, nil
	}
	var sel ast.Expr
	if ruleName.MatchString(name) {
		sel = ast.NewSel(x.expr, name)
	} else {
		sel = &ast.IndexExpr{X: x.expr, Index: str(name)}
	}
	return valueRef{expr: sel, local: x.local, property: name}, nil
}

func (r *ruleCompiler) variable() valueRef {
	r.vars++
	return valueRef{expr: id(fmt.Sprintf("cfnRuleItem%d", r.vars)), local: true}
}

// scopedRule compiles schema, applied at path below the properties struct,
// to declarations for that struct.
func (r *ruleCompiler) scopedRule(schema map[string]any, path string) ([]ast.Decl, error) {
	r.names = map[string]bool{}
	return r.at(schema, valueRef{root: true}, strings.Split(path, "/"))
}

func (r *ruleCompiler) at(schema map[string]any, x valueRef, steps []string) ([]ast.Decl, error) {
	if len(steps) == 0 || steps[0] == "" {
		return r.check(schema, x)
	}
	if steps[0] == "*" {
		v := r.variable()
		decls, err := r.at(schema, v, steps[1:])
		if err != nil || len(decls) == 0 {
			return decls, err
		}
		return []ast.Decl{&ast.Comprehension{
			Clauses: []ast.Clause{&ast.IfClause{Condition: isList(x)}, &ast.ForClause{Value: v.expr.(*ast.Ident), Source: x.expr}},
			Value:   &ast.StructLit{Elts: decls},
		}}, nil
	}
	next, err := r.child(x, steps[0])
	if err != nil {
		return nil, err
	}
	decls, err := r.at(schema, next, steps[1:])
	if err != nil || len(decls) == 0 {
		return decls, err
	}
	return []ast.Decl{guard(present(next), decls)}, nil
}

// check splits allOf, if/then/else and properties into nested comprehension
// clauses, which CUE evaluates only when the enclosing clause holds. The
// remaining keywords become one predicate.
func (r *ruleCompiler) check(schema map[string]any, x valueRef) ([]ast.Decl, error) {
	var decls []ast.Decl
	rest := map[string]any{}
	for _, k := range keys(schema) {
		v := schema[k]
		switch k {
		case "description", "then", "else":
		case "allOf":
			for _, item := range asArray(v) {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("allOf item is not a schema")
				}
				d, err := r.check(m, x)
				if err != nil {
					return nil, err
				}
				decls = append(decls, d...)
			}
		case "if":
			condition, err := r.condition(v, x)
			if err != nil {
				return nil, err
			}
			for _, branch := range []struct {
				key  string
				cond ast.Expr
			}{{"then", condition}, {"else", not(condition)}} {
				m, ok := schema[branch.key].(map[string]any)
				if !ok {
					continue
				}
				d, err := r.check(m, x)
				if err != nil {
					return nil, err
				}
				if len(d) > 0 {
					decls = append(decls, guard(branch.cond, d))
				}
			}
		case "properties":
			props, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("properties is not an object")
			}
			for _, name := range keys(props) {
				m, ok := props[name].(map[string]any)
				if !ok {
					if props[name] == true {
						continue
					}
					return nil, fmt.Errorf("property %q schema is not an object", name)
				}
				next, err := r.child(x, name)
				if err != nil {
					return nil, err
				}
				d, err := r.check(m, next)
				if err != nil {
					return nil, err
				}
				if len(d) > 0 {
					decls = append(decls, guard(present(next), d))
				}
			}
		default:
			rest[k] = v
		}
	}
	if names := stringsOf(rest["required"]); x.root && len(names) > 0 {
		// An error for a missing property would hold for the schema itself,
		// which has no data. A required field is only checked against data.
		delete(rest, "required")
		for _, name := range names {
			if _, err := r.child(x, name); err != nil {
				return nil, err
			}
			decls = append(decls, &ast.Field{Label: ast.NewIdent(name), Constraint: token.NOT, Value: id("_")})
		}
	}
	if len(rest) > 0 {
		p, err := r.pred(rest, x, false)
		if err != nil {
			return nil, err
		}
		failure := ast.NewCall(ast.NewPredeclared("error"), str(r.rule+": "+r.message))
		decls = append(decls, guard(not(p), []ast.Decl{&ast.EmbedDecl{Expr: failure}}))
	}
	return decls, nil
}

// pred returns a boolean expression that holds when x, which is present,
// satisfies schema. literal marks a condition, which intrinsics never meet.
// Every operand is guarded, so the expression is never an error.
func (r *ruleCompiler) pred(v any, x valueRef, literal bool) (ast.Expr, error) {
	if b, ok := v.(bool); ok {
		return ast.NewBool(b), nil
	}
	schema, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema is not an object")
	}
	var terms []ast.Expr
	typed := false
	for _, k := range keys(schema) {
		v := schema[k]
		var term ast.Expr
		switch k {
		case "description":
			continue
		case "type":
			typed = true
			var alts []ast.Expr
			for _, t := range stringsOf(append(asArray(v), stringValue(v)...)) {
				alt, err := r.typeTest(t, x)
				if err != nil {
					return nil, err
				}
				alts = append(alts, alt)
			}
			term = r.leaf(x, literal, or(alts...))
		case "const", "enum":
			values := asArray(v)
			if k == "const" {
				values = []any{v}
			}
			term = r.leaf(x, literal, r.oneOf(values, x))
		case "pattern":
			p, _ := v.(string)
			term = r.leaf(x, literal, r.pattern(p, x))
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			continue // numericTerm below combines them
		case "not":
			p, err := r.pred(v, x, true)
			if err != nil {
				return nil, err
			}
			term = not(p)
		case "allOf", "anyOf":
			var items []ast.Expr
			for _, item := range asArray(v) {
				p, err := r.pred(item, x, literal)
				if err != nil {
					return nil, err
				}
				items = append(items, p)
			}
			if k == "allOf" {
				term = andAll(items...)
			} else {
				term = or(items...)
			}
		case "if":
			condition, err := r.condition(v, x)
			if err != nil {
				return nil, err
			}
			var branches []ast.Expr
			for _, b := range []struct {
				key  string
				cond ast.Expr
			}{{"then", not(condition)}, {"else", condition}} {
				if s, ok := schema[b.key]; ok {
					p, err := r.pred(s, x, literal)
					if err != nil {
						return nil, err
					}
					branches = append(branches, or(b.cond, p))
				}
			}
			term = andAll(branches...)
		case "then", "else":
			continue
		case "properties":
			props, _ := v.(map[string]any)
			var items []ast.Expr
			for _, name := range keys(props) {
				next, err := r.child(x, name)
				if err != nil {
					return nil, err
				}
				p, err := r.pred(props[name], next, literal)
				if err != nil {
					return nil, err
				}
				if includes(stringsOf(schema["required"]), name) {
					// required tests presence.
					items = append(items, p)
				} else {
					items = append(items, or(not(present(next)), p))
				}
			}
			term = andAll(items...)
		case "required":
			var items []ast.Expr
			for _, name := range stringsOf(v) {
				next, err := r.child(x, name)
				if err != nil {
					return nil, err
				}
				items = append(items, present(next))
			}
			term = andAll(items...)
			if !x.root {
				// required ignores values that are not objects.
				term = or(term, not(isStruct(x)))
				if !literal && r.template {
					term = or(r.intrinsic(x), term)
				}
			}
		case "items", "contains":
			item := r.variable()
			p, err := r.pred(v, item, literal)
			if err != nil {
				return nil, err
			}
			if k == "items" {
				// No item fails.
				term = count(x, item, not(p), token.EQL, 0)
			} else {
				term = count(x, item, p, token.GEQ, 1)
			}
			term = or(not(isList(x)), term)
		case "minItems", "maxItems":
			n, ok := v.(float64)
			if !ok || n != math.Trunc(n) {
				return nil, fmt.Errorf("%s is not an integer", k)
			}
			op := map[string]token.Token{"minItems": token.GEQ, "maxItems": token.LEQ}[k]
			length := ast.NewCall(ast.NewPredeclared("len"), x.expr)
			term = or(not(isList(x)), safe(&ast.BinaryExpr{X: length, Op: op, Y: ast.NewLit(token.INT, strconv.Itoa(int(n)))}))
		default:
			return nil, fmt.Errorf("unsupported keyword %s", k)
		}
		terms = append(terms, term)
	}
	if bounds := numericBounds(schema); bounds != nil {
		if literal && !typed {
			// A numeric condition is not met by a string or other kind.
			terms = append(terms, safe(bounds(x.expr)))
		} else {
			term := or(not(isNumber(x)), safe(bounds(x.expr)))
			if !literal {
				// cfn-lint compares a numeric string as a number.
				cleaned := ast.NewCall(builtin("cfnRuleStrings", "strings", "Replace"), ast.NewCall(builtin("cfnRuleStrings", "strings", "TrimSpace"), x.expr), str("_"), str(""), &ast.UnaryExpr{Op: token.SUB, X: ast.NewLit(token.INT, "1")})
				parsed := ast.NewCall(builtin("cfnRuleStrconv", "strconv", "ParseFloat"), cleaned, ast.NewLit(token.INT, "64"))
				term = andAll(term, or(not(matches(x, quotedNumberPattern)), safe(bounds(parsed))))
			}
			terms = append(terms, r.leaf(x, literal, term))
		}
	}
	return andAll(terms...), nil
}

func stringValue(v any) []any {
	if s, ok := v.(string); ok {
		return []any{s}
	}
	return nil
}

func (r *ruleCompiler) typeTest(t string, x valueRef) (ast.Expr, error) {
	switch t {
	case "string":
		return isString(x), nil
	case "number":
		return isNumber(x), nil
	case "integer":
		return safe(ast.NewCall(builtin("cfnRuleMath", "math", "MultipleOf"), x.expr, ast.NewLit(token.INT, "1"))), nil
	case "boolean":
		return safe(or(equals(x, ast.NewBool(true)), equals(x, ast.NewBool(false)))), nil
	case "null":
		return safe(equals(x, ast.NewNull())), nil
	case "array":
		return isList(x), nil
	case "object":
		if x.root {
			return ast.NewBool(true), nil
		}
		return isStruct(x), nil
	}
	return nil, fmt.Errorf("unsupported type %q", t)
}

// oneOf compares x with values as cfn-lint's equal() does.
func (r *ruleCompiler) oneOf(values []any, x valueRef) ast.Expr {
	var literals []ast.Expr
	if r.lowercaseEngine && x.property == "Engine" && allStrings(values) {
		var lowered []string
		for _, value := range values {
			lowered = append(lowered, strings.ToLower(value.(string)))
		}
		sort.Strings(lowered)
		for _, value := range lowered {
			literals = append(literals, str(value))
		}
		x = valueRef{expr: ast.NewCall(builtin("cfnRuleStrings", "strings", "ToLower"), x.expr), local: x.local}
	} else {
		for _, value := range looseValues(values) {
			literals = append(literals, jsonLiteral(value))
		}
	}
	var floats []ast.Expr
	for _, value := range values {
		// Python's str(8.0) is "8.0", so cfn-lint finds the float 8.0, but
		// not the integer 8, in ["8.0"]. looseValues cannot express this;
		// a float's interpolation keeps its point or exponent.
		if v, ok := value.(string); ok && integralFloat.MatchString(v) {
			n, _ := strconv.ParseFloat(v, 64)
			spelled := &ast.Interpolation{Elts: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: `"\(`}, x.expr, &ast.BasicLit{Kind: token.STRING, Value: `)"`},
			}}
			floats = append(floats, safe(andAll(equals(x, jsonLiteral(n)), &ast.BinaryExpr{X: spelled, Op: token.MAT, Y: str("[.eE]")})))
		}
	}
	if len(literals) > 8 {
		// A list scan in Go is shorter than a chain of comparisons for
		// engine version tables.
		return or(append([]ast.Expr{safe(ast.NewCall(builtin("cfnRuleList", "list", "Contains"), ast.NewList(literals...), x.expr))}, floats...)...)
	}
	var alts []ast.Expr
	for _, l := range literals {
		alts = append(alts, equals(x, l))
	}
	return or(append([]ast.Expr{safe(or(alts...))}, floats...)...)
}

var integralFloat = regexp.MustCompile(`^-?[1-9][0-9]{0,14}\.0$|^-?0\.0$`)

func (r *ruleCompiler) pattern(p string, x valueRef) ast.Expr {
	// pattern ignores values that are not strings.
	if m := negativePrefix.FindStringSubmatch(p); m != nil {
		return not(matches(x, "^"+m[1]))
	}
	p = re2Pattern(p)
	if r.lowercaseEngine && x.property == "Engine" {
		p = "(?i)" + p
	}
	return or(not(isString(x)), matches(x, p))
}

// leaf admits, outside conditions in template schemas, an intrinsic or a
// dynamic reference wherever a value is constrained.
func (r *ruleCompiler) leaf(x valueRef, literal bool, term ast.Expr) ast.Expr {
	if literal && r.tested != nil && !x.root && !x.local {
		b, _ := format.Node(x.expr)
		r.tested[string(b)] = x
	}
	if literal || !r.template || x.root {
		return term
	}
	return or(r.intrinsic(x), matches(x, dynamicReferencePattern), term)
}

// condition compiles an if schema. An intrinsic never satisfies a
// condition, including one under not: BillingMode: {Ref: Mode} does not
// require ProvisionedThroughput, as cfn-lint does not when Mode may be
// PAY_PER_REQUEST.
func (r *ruleCompiler) condition(v any, x valueRef) (ast.Expr, error) {
	saved := r.tested
	r.tested = map[string]valueRef{}
	defer func() { r.tested = saved }()
	p, err := r.pred(v, x, true)
	if err != nil || !r.template {
		return p, err
	}
	names := make([]string, 0, len(r.tested))
	for k := range r.tested {
		names = append(names, k)
	}
	sort.Strings(names)
	terms := []ast.Expr{p}
	for _, k := range names {
		terms = append(terms, not(r.intrinsic(r.tested[k])))
	}
	return andAll(terms...), nil
}

// intrinsic holds for an object with a single Ref or Fn:: key. It iterates
// over keys rather than unifying x, which may be a large object.
func (r *ruleCompiler) intrinsic(x valueRef) ast.Expr {
	key := ast.NewIdent("cfnRuleKey")
	isFunction := or(
		&ast.BinaryExpr{X: key, Op: token.EQL, Y: str("Ref")},
		ast.NewCall(builtin("cfnRuleStrings", "strings", "HasPrefix"), key, str("Fn::")),
	)
	keys := &ast.ListLit{Elts: []ast.Expr{&ast.Comprehension{
		Clauses: []ast.Clause{&ast.ForClause{Key: key, Value: ast.NewIdent("_"), Source: x.expr}, &ast.IfClause{Condition: isFunction}},
		Value:   &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: ast.NewLit(token.INT, "0")}}},
	}}}
	return safe(andAll(
		&ast.BinaryExpr{X: ast.NewCall(ast.NewPredeclared("len"), x.expr), Op: token.EQL, Y: ast.NewLit(token.INT, "1")},
		&ast.BinaryExpr{X: ast.NewCall(ast.NewPredeclared("len"), keys), Op: token.EQL, Y: ast.NewLit(token.INT, "1")},
	))
}

// numericBounds returns a function that tests a number against the
// schema's numeric keywords, or nil if it has none.
func numericBounds(schema map[string]any) func(ast.Expr) ast.Expr {
	ops := []struct {
		key string
		op  token.Token
	}{{"minimum", token.GEQ}, {"maximum", token.LEQ}, {"exclusiveMinimum", token.GTR}, {"exclusiveMaximum", token.LSS}}
	var found bool
	for _, k := range numericKeywords {
		_, ok := schema[k].(float64)
		found = found || ok
	}
	if !found {
		return nil
	}
	return func(x ast.Expr) ast.Expr {
		var tests []ast.Expr
		for _, o := range ops {
			if n, ok := schema[o.key].(float64); ok {
				tests = append(tests, &ast.BinaryExpr{X: x, Op: o.op, Y: jsonLiteral(n)})
			}
		}
		if n, ok := schema["multipleOf"].(float64); ok {
			tests = append(tests, ast.NewCall(builtin("cfnRuleMath", "math", "MultipleOf"), x, jsonLiteral(n)))
		}
		return andAll(tests...)
	}
}

func jsonLiteral(v any) ast.Expr {
	switch x := v.(type) {
	case string:
		return str(x)
	case bool:
		return ast.NewBool(x)
	case nil:
		return ast.NewNull()
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return ast.NewLit(token.INT, strconv.FormatInt(int64(x), 10))
		}
		return ast.NewLit(token.FLOAT, strconv.FormatFloat(x, 'g', -1, 64))
	}
	panic(fmt.Sprintf("unsupported literal %T", v))
}

// builtin refers to a standard library package under a private alias.
// astutil.Sanitize adds the import.
func builtin(alias, path, name string) ast.Expr {
	ident := ast.NewIdent(alias)
	ident.Node = ast.NewImport(ast.NewIdent(alias), path)
	return ast.NewSel(ident, name)
}

func paren(x ast.Expr) ast.Expr {
	switch x.(type) {
	case *ast.Ident, *ast.ParenExpr, *ast.CallExpr, *ast.BasicLit, *ast.SelectorExpr, *ast.IndexExpr, *ast.UnaryExpr:
		return x
	}
	return &ast.ParenExpr{X: x}
}

func not(x ast.Expr) ast.Expr {
	if p, ok := x.(*ast.ParenExpr); ok {
		return not(p.X)
	}
	if b, ok := x.(*ast.BasicLit); ok && (b.Value == "true" || b.Value == "false") {
		return ast.NewBool(b.Value == "false")
	}
	if b, ok := x.(*ast.BinaryExpr); ok {
		if _, bottom := b.Y.(*ast.BottomLit); bottom && (b.Op == token.EQL || b.Op == token.NEQ) {
			return bottomTest(b.X, map[token.Token]token.Token{token.EQL: token.NEQ, token.NEQ: token.EQL}[b.Op])
		}
	}
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		if p, ok := u.X.(*ast.ParenExpr); ok {
			return p.X
		}
		return u.X
	}
	return &ast.UnaryExpr{Op: token.NOT, X: paren(x)}
}

func logical(op token.Token, identity bool, xs []ast.Expr) ast.Expr {
	var kept []ast.Expr
	for _, x := range xs {
		if b, ok := x.(*ast.BasicLit); ok && (b.Value == "true" || b.Value == "false") {
			if (b.Value == "true") == identity {
				continue
			}
			return ast.NewBool(!identity)
		}
		kept = append(kept, paren(x))
	}
	switch len(kept) {
	case 0:
		return ast.NewBool(identity)
	case 1:
		return kept[0]
	}
	return ast.NewBinExpr(op, kept...)
}

func andAll(xs ...ast.Expr) ast.Expr { return logical(token.LAND, true, xs) }
func or(xs ...ast.Expr) ast.Expr     { return logical(token.LOR, false, xs) }

func bottomTest(x ast.Expr, op token.Token) ast.Expr {
	return &ast.BinaryExpr{X: paren(x), Op: op, Y: &ast.BottomLit{}}
}

func present(x valueRef) ast.Expr { return bottomTest(x.expr, token.NEQ) }

// The tests below compare x or pass it to builtins. Unifying x with a type,
// as in (x & string) != _|_, would evaluate x's schema again, including
// its intrinsic alternatives.

func equals(x valueRef, v ast.Expr) ast.Expr {
	return &ast.BinaryExpr{X: paren(x.expr), Op: token.EQL, Y: v}
}

func matches(x valueRef, pattern string) ast.Expr {
	return safe(&ast.BinaryExpr{X: paren(x.expr), Op: token.MAT, Y: str(pattern)})
}

// safe turns an expression that may fail, such as a comparison or builtin
// given the wrong kind, into a boolean that is false on failure.
func safe(x ast.Expr) ast.Expr { return bottomTest(and(paren(x), ast.NewBool(true)), token.NEQ) }

func isString(x valueRef) ast.Expr { return matches(x, "") }

func isNumber(x valueRef) ast.Expr {
	abs := ast.NewCall(builtin("cfnRuleMath", "math", "Abs"), x.expr)
	return safe(&ast.BinaryExpr{X: abs, Op: token.GEQ, Y: ast.NewLit(token.INT, "0")})
}

func isList(x valueRef) ast.Expr {
	return safe(ast.NewCall(builtin("cfnRuleList", "list", "MinItems"), x.expr, ast.NewLit(token.INT, "0")))
}

func isStruct(x valueRef) ast.Expr {
	length := bottomTest(ast.NewCall(ast.NewPredeclared("len"), x.expr), token.NEQ)
	return andAll(length, not(isList(x)), not(isString(x)))
}

// count compares the number of items of list x for which cond holds with n.
func count(x, item valueRef, cond ast.Expr, op token.Token, n int) ast.Expr {
	matches := &ast.ListLit{Elts: []ast.Expr{&ast.Comprehension{
		Clauses: []ast.Clause{&ast.ForClause{Value: item.expr.(*ast.Ident), Source: x.expr}, &ast.IfClause{Condition: cond}},
		Value:   &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: ast.NewLit(token.INT, "0")}}},
	}}}
	return safe(&ast.BinaryExpr{X: ast.NewCall(ast.NewPredeclared("len"), matches), Op: op, Y: ast.NewLit(token.INT, strconv.Itoa(n))})
}

// guard emits decls when cond holds. Each operand of a top-level && becomes
// its own clause, presence tests first: CUE evaluates a clause only when the
// previous ones hold, so a rule about EngineVersion costs little when
// EngineVersion is absent.
func guard(cond ast.Expr, decls []ast.Decl) ast.Decl {
	var presence, rest []ast.Clause
	for _, c := range conjuncts(cond) {
		if b, ok := c.(*ast.BinaryExpr); ok && b.Op == token.NEQ && isReference(b.X) {
			if _, bottom := b.Y.(*ast.BottomLit); bottom {
				presence = append(presence, &ast.IfClause{Condition: c})
				continue
			}
		}
		rest = append(rest, &ast.IfClause{Condition: c})
	}
	return &ast.Comprehension{Clauses: append(presence, rest...), Value: &ast.StructLit{Elts: decls}}
}

func isReference(x ast.Expr) bool {
	switch n := x.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		return isReference(n.X)
	case *ast.IndexExpr:
		return isReference(n.X)
	}
	return false
}

func conjuncts(x ast.Expr) []ast.Expr {
	switch n := x.(type) {
	case *ast.ParenExpr:
		return conjuncts(n.X)
	case *ast.BinaryExpr:
		if n.Op == token.LAND {
			return append(conjuncts(n.X), conjuncts(n.Y)...)
		}
	}
	return []ast.Expr{x}
}
