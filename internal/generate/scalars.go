package generate

import (
	"regexp"
	"strconv"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/token"
)

// addScalarCompatibility is only applied to CloudFormation input schemas.
// Provider literal schemas keep JSON's scalar types. Quoted values retain their
// original spelling; a parsed value must satisfy the complete numeric branch.
// Enums, consts, and opaque applicators conservatively keep exact JSON values.
// cfn-lint also accepts selected string spellings of enum/const members; that
// comparison policy needs separate handling and is not widened here.
//
// In the other direction, CloudFormation converts a number or boolean given
// for a string property to its string form, so MinSize: 1 is valid. A string
// branch also accepts a number or boolean whose string form satisfies it. The
// value itself is unchanged; only its string form is checked.
func addScalarCompatibility(f *ast.File, definitions ...string) (skipped int) {
	return (&scalarRewriter{strings: true}).file(f, definitions)
}

// addQuotedScalarCompatibility applies only the quoted number and boolean
// direction, for template sections whose string fields stay strict.
func addQuotedScalarCompatibility(f *ast.File, definitions ...string) (skipped int) {
	return (&scalarRewriter{}).file(f, definitions)
}

type scalarRewriter struct {
	// strings lets string branches accept numbers and booleans.
	strings  bool
	selected map[string]bool
	skipped  int
	numeric  bool
}

// scalarContext describes the other alternatives of a disjunction. A number
// or boolean already allowed by a sibling alternative must satisfy that
// alternative, so a string branch is not widened to that kind.
type scalarContext struct {
	anyString, number, boolean bool
}

func (r *scalarRewriter) file(f *ast.File, definitions []string) int {
	r.selected = map[string]bool{}
	for _, name := range definitions {
		r.selected[name] = true
	}
	astutil.Apply(f, r.pre, nil)
	if r.numeric {
		addImport(f, "cfnScalarStrings", "strings")
		addImport(f, "cfnScalarStrconv", "strconv")
	}
	return r.skipped
}

func (r *scalarRewriter) pre(cur astutil.Cursor) bool {
	if field, ok := cur.Node().(*ast.Field); ok && len(r.selected) != 0 && cur.Parent() != nil {
		if _, rootField := cur.Parent().Node().(*ast.File); rootField {
			label, ok := field.Label.(*ast.Ident)
			if !ok || !r.selected[label.Name] {
				return false
			}
		}
	}
	if _, ok := cur.Node().(*ast.IfClause); ok {
		return false
	}
	expr, ok := cur.Node().(ast.Expr)
	if !ok {
		return true
	}
	// Labels, including pattern constraints such as [=~".*"], are not values.
	if cur.Parent() != nil {
		if field, ok := cur.Parent().Node().(*ast.Field); ok && field.Label == cur.Node() {
			return false
		}
	}
	if alts := alternatives(expr); r.strings && len(alts) > 1 && hasStringAlternative(alts) {
		cur.Replace(operand(cur, r.union(alts)))
		return false
	}
	out, descend := r.visit(expr, scalarContext{})
	if out != nil {
		cur.Replace(operand(cur, out))
	}
	return descend
}

// operand parenthesizes a disjunction that replaces an operand of another
// operator, such as the right side of x & string.
func operand(cur astutil.Cursor, x ast.Expr) ast.Expr {
	if bin, ok := x.(*ast.BinaryExpr); !ok || bin.Op != token.OR || cur.Parent() == nil {
		return x
	}
	switch parent := cur.Parent().Node().(type) {
	case *ast.UnaryExpr:
		return &ast.ParenExpr{X: x}
	case *ast.BinaryExpr:
		if parent.Op != token.OR {
			return &ast.ParenExpr{X: x}
		}
	}
	return x
}

// union rewrites each alternative of a disjunction that has a string branch,
// in the context of its siblings.
func (r *scalarRewriter) union(alts []ast.Expr) ast.Expr {
	ctx := scalarContext{}
	for _, alt := range alts {
		ctx.anyString = ctx.anyString || permitsAnyString(alt)
		kind := scalarExprInfo(alt).kind
		ctx.number = ctx.number || kind&(scalarInteger|scalarNumber) != 0
		ctx.boolean = ctx.boolean || kind&scalarBoolean != 0
	}
	out := make([]ast.Expr, 0, len(alts))
	for _, alt := range alts {
		x, descend := r.visit(alt, ctx)
		if x == nil {
			x = alt
		}
		if descend {
			x = astutil.Apply(x, r.pre, nil).(ast.Expr)
		}
		out = append(out, x)
	}
	return union(out...)
}

// visit returns a replacement for expr, or nil to keep it, and whether its
// children still need to be visited.
func (r *scalarRewriter) visit(expr ast.Expr, ctx scalarContext) (ast.Expr, bool) {
	// Existing unconstrained strings already accept every quoted scalar.
	// Preserve those unions rather than adding redundant parsing branches.
	if permitsAnyString(expr) {
		if !r.strings {
			return nil, false
		}
		return union(append([]ast.Expr{expr}, missingScalars(ctx)...)...), false
	}
	info := scalarExprInfo(expr)
	// A string branch's string form check covers the whole branch, including
	// enums and opaque references, so no string branch is blocked.
	if info.kind == scalarString {
		if !r.strings {
			return nil, false
		}
		return r.stringBranch(expr, ctx), false
	}
	if info.blocked {
		if info.typed {
			r.skipped++
			return nil, false
		}
		// A matchN predicate must retain its original alternatives. A
		// surrounding object intersection still has independent property
		// schemas to visit, such as requiredXor & close({FromPort: int}).
		if _, call := expr.(*ast.CallExpr); call {
			return nil, false
		}
		return nil, true
	}
	if info.kind == 0 || !info.typed || info.other {
		return nil, true
	}
	// A sibling string alternative already accepts every quoted scalar.
	if ctx.anyString && info.kind&scalarString == 0 {
		return nil, false
	}
	// Do not descend into an enum/const expression: widening its int/bool
	// conjunct separately would create an escape hatch around its allowed values.
	if info.constant {
		r.skipped++
		return nil, false
	}
	if info.kind == scalarBoolean {
		if ident, ok := expr.(*ast.Ident); !ok || ident.Name != "bool" {
			return nil, false
		}
		return booleanCompatibility(expr), false
	}
	if info.kind != scalarInteger && info.kind != scalarNumber {
		return nil, true
	}
	r.numeric = true
	return union(expr, quotedNumber(expr, info.kind == scalarInteger)), false
}

// stringBranch adds the numbers and booleans whose string form satisfies a
// string branch. A literal gains the one scalar spelled exactly like it, so
// "-1" | "tcp" also accepts -1. Other branches check the string form when
// validated, as CUE's interpolation of the value.
func (r *scalarRewriter) stringBranch(expr ast.Expr, ctx scalarContext) ast.Expr {
	kinds := missingScalars(ctx)
	if len(kinds) == 0 {
		return nil
	}
	if literal := stringLiteral(expr); literal != nil {
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil
		}
		switch {
		case !ctx.boolean && (value == "true" || value == "false"):
			return union(expr, ast.NewBool(value == "true"))
		case !ctx.number && canonicalInteger.MatchString(value):
			return union(expr, numberLiteral(token.INT, value))
		case !ctx.number && canonicalFloat.MatchString(value):
			return union(expr, numberLiteral(token.FLOAT, value))
		}
		return nil
	}
	form := &ast.Interpolation{Elts: []ast.Expr{
		&ast.BasicLit{Kind: token.STRING, Value: `"\(`},
		ast.NewPredeclared("self"),
		&ast.BasicLit{Kind: token.STRING, Value: `)"`},
	}}
	checked := &ast.StructLit{Elts: []ast.Decl{
		&ast.EmbedDecl{Expr: union(kinds...)},
		&ast.Field{Label: ast.NewIdent("_cfnString"), Value: and(expr, form)},
	}}
	return union(expr, checked)
}

// Spellings that CUE's interpolation produces for an integer or decimal
// literal. Other numeric spellings, such as 01 or 1e3, have no such value.
var canonicalInteger = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)
var canonicalFloat = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)\.[0-9]+$`)

func numberLiteral(kind token.Token, value string) ast.Expr {
	if value[0] == '-' {
		return &ast.UnaryExpr{Op: token.SUB, X: ast.NewLit(kind, value[1:])}
	}
	return ast.NewLit(kind, value)
}

func missingScalars(ctx scalarContext) []ast.Expr {
	var kinds []ast.Expr
	if !ctx.number {
		kinds = append(kinds, ast.NewIdent("number"))
	}
	if !ctx.boolean {
		kinds = append(kinds, ast.NewIdent("bool"))
	}
	return kinds
}

// stringLiteral returns the literal of "x" or "x" & string.
func stringLiteral(expr ast.Expr) *ast.BasicLit {
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			return x
		}
	case *ast.ParenExpr:
		return stringLiteral(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.AND {
			break
		}
		if ident, ok := x.Y.(*ast.Ident); ok && ident.Name == "string" {
			return stringLiteral(x.X)
		}
		if ident, ok := x.X.(*ast.Ident); ok && ident.Name == "string" {
			return stringLiteral(x.Y)
		}
	}
	return nil
}

// alternatives flattens a disjunction, including parenthesized ones.
func alternatives(expr ast.Expr) []ast.Expr {
	switch x := expr.(type) {
	case *ast.ParenExpr:
		if alts := alternatives(x.X); len(alts) > 1 {
			return alts
		}
	case *ast.BinaryExpr:
		if x.Op == token.OR {
			return append(alternatives(x.X), alternatives(x.Y)...)
		}
	}
	return []ast.Expr{expr}
}

func hasStringAlternative(alts []ast.Expr) bool {
	for _, alt := range alts {
		if scalarExprInfo(alt).kind&scalarString != 0 {
			return true
		}
	}
	return false
}

func permitsAnyString(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name == "string"
	case *ast.ParenExpr:
		return permitsAnyString(x.X)
	case *ast.BinaryExpr:
		return x.Op == token.OR && scalarOnlyExpr(x) && (permitsAnyString(x.X) || permitsAnyString(x.Y))
	}
	return false
}

func scalarOnlyExpr(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		switch x.Name {
		case "string", "int", "number", "float", "bool", "null":
			return true
		}
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return scalarOnlyExpr(x.X)
	case *ast.UnaryExpr:
		return scalarOnlyExpr(x.X)
	case *ast.BinaryExpr:
		return scalarOnlyExpr(x.X) && scalarOnlyExpr(x.Y)
	}
	return false
}

type scalarKind uint8

const (
	scalarInteger scalarKind = 1 << iota
	scalarNumber
	scalarBoolean
	scalarString
)

type scalarInfo struct {
	kind                   scalarKind
	typed, constant, other bool
	blocked                bool
	integral               bool
}

// scalarExprInfo recognizes complete scalar branches, rather than rewriting
// individual type identifiers and accidentally bypassing adjacent constraints.
// Opaque validators or references in scalar intersections stay strict.
// Disjunctions with opaque alternatives are visited separately.
func scalarExprInfo(expr ast.Expr) scalarInfo {
	switch x := expr.(type) {
	case *ast.ParenExpr:
		return scalarExprInfo(x.X)
	case *ast.Ident:
		switch x.Name {
		case "int":
			return scalarInfo{kind: scalarInteger, typed: true, integral: true}
		case "number", "float":
			return scalarInfo{kind: scalarNumber, typed: true}
		case "bool":
			return scalarInfo{kind: scalarBoolean, typed: true}
		case "string":
			return scalarInfo{kind: scalarString, typed: true}
		default:
			return scalarInfo{other: true}
		}
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			return scalarInfo{kind: scalarInteger, constant: true}
		case token.FLOAT:
			return scalarInfo{kind: scalarNumber, constant: true}
		case token.TRUE, token.FALSE:
			return scalarInfo{kind: scalarBoolean, constant: true}
		case token.STRING:
			return scalarInfo{kind: scalarString, constant: true}
		default:
			return scalarInfo{other: true}
		}
	case *ast.UnaryExpr:
		switch x.Op {
		case token.LSS, token.LEQ, token.GTR, token.GEQ:
			info := scalarExprInfo(x.X)
			info.constant = false
			if info.kind == scalarInteger || info.kind == scalarNumber {
				info.kind = scalarNumber
				info.typed = true
			}
			if info.kind == scalarString {
				info.typed = true
			}
			return info
		case token.ADD, token.SUB:
			return scalarExprInfo(x.X)
		case token.MAT, token.NMAT:
			return scalarInfo{kind: scalarString, typed: true}
		}
	case *ast.BinaryExpr:
		if x.Op != token.AND && x.Op != token.OR {
			break
		}
		a, b := scalarExprInfo(x.X), scalarExprInfo(x.Y)
		info := scalarInfo{kind: a.kind | b.kind, typed: a.typed || b.typed, constant: a.constant || b.constant, other: a.other || b.other, blocked: a.blocked || b.blocked, integral: a.integral || b.integral}
		if x.Op == token.OR {
			info.integral = a.integral && b.integral
		}
		if info.kind == scalarInteger|scalarNumber {
			info.kind = scalarNumber
		}
		if x.Op == token.AND && info.integral && (info.kind == scalarInteger || info.kind == scalarNumber) {
			info.kind = scalarInteger
		}
		// Opaque references can impose exact enum values. Keep a type plus
		// such a constraint strict rather than coercing strings past it.
		if x.Op == token.AND && info.typed && info.kind != 0 && info.other {
			info.blocked = true
		}
		return info
	case *ast.CallExpr:
		if ident, ok := x.Fun.(*ast.Ident); ok && ident.Name == "matchN" {
			return scalarInfo{other: true, blocked: true}
		}
		// The extractor's maxLength, minLength and date-time validators.
		if fun, ok := x.Fun.(*ast.SelectorExpr); ok {
			pkg, _ := fun.X.(*ast.Ident)
			name, _ := fun.Sel.(*ast.Ident)
			if pkg != nil && name != nil && (pkg.Name == "strings" && (name.Name == "MinRunes" || name.Name == "MaxRunes") || pkg.Name == "time" && name.Name == "Time") {
				return scalarInfo{kind: scalarString, typed: true}
			}
		}
	}
	return scalarInfo{other: true}
}

// Decimal spellings accepted here mirror Python's int/float conversions for
// ASCII inputs, including whitespace, signs, underscores between digits, and
// number exponents. Hexadecimal/CUE suffixes and non-finite values are excluded.
// Unicode digits and Python's non-finite float spellings remain unsupported.
const quotedIntegerPattern = `^[[:space:]]*[+-]?[0-9](?:_?[0-9])*[[:space:]]*$`
const quotedNumberPattern = `^[[:space:]]*[+-]?(?:[0-9](?:_?[0-9])*(?:\.(?:[0-9](?:_?[0-9])*)?)?|\.[0-9](?:_?[0-9])*)(?:[eE][+-]?[0-9](?:_?[0-9])*)?[[:space:]]*$`

func booleanCompatibility(literal ast.Expr) ast.Expr {
	return union(literal, str("true"), str("True"), str("TRUE"), str("false"), str("False"), str("FALSE"))
}

func quotedNumber(constraints ast.Expr, integer bool) ast.Expr {
	call := func(pkg, name string, args ...ast.Expr) ast.Expr {
		path := map[string]string{"cfnScalarStrings": "strings", "cfnScalarStrconv": "strconv"}[pkg]
		ident := ast.NewIdent(pkg)
		ident.Node = ast.NewImport(ast.NewIdent(pkg), path)
		return &ast.CallExpr{Fun: ast.NewSel(ident, name), Args: args}
	}
	raw := ast.NewPredeclared("self")
	cleaned := call("cfnScalarStrings", "Replace", call("cfnScalarStrings", "TrimSpace", raw), str("_"), str(""), &ast.UnaryExpr{Op: token.SUB, X: ast.NewLit(token.INT, "1")})
	pattern := quotedIntegerPattern
	var parsed, formatted ast.Expr
	if integer {
		// CUE's strconv.Atoi and FormatInt use big.Int, unlike Go's standard
		// strconv.Atoi. Quoted integers therefore retain unlimited precision.
		parsed = call("cfnScalarStrconv", "Atoi", cleaned)
		formatted = call("cfnScalarStrconv", "FormatInt", and(parsed, constraints), ast.NewLit(token.INT, "10"))
	} else {
		pattern = quotedNumberPattern
		parsed = call("cfnScalarStrconv", "ParseFloat", cleaned, ast.NewLit(token.INT, "64"))
		formatted = call("cfnScalarStrconv", "FormatFloat", and(parsed, constraints), str("g"), &ast.UnaryExpr{Op: token.SUB, X: ast.NewLit(token.INT, "1")}, ast.NewLit(token.INT, "64"))
	}
	// len forces the parsed value and its constraints to be evaluated. The
	// resulting repeat count is exactly one, preserving the original string.
	// Formatting first also avoids feeding a float-valued 1.0 to Repeat's int
	// argument. Scalar embedding makes self refer to each concrete scalar,
	// including values reached through definitions, list items, or patterns.
	n := &ast.CallExpr{Fun: ast.NewPredeclared("len"), Args: []ast.Expr{formatted}}
	count := ast.NewBinExpr(token.ADD, ast.NewBinExpr(token.MUL, n, ast.NewLit(token.INT, "0")), ast.NewLit(token.INT, "1"))
	checked := and(ast.NewPredeclared("string"), &ast.UnaryExpr{Op: token.MAT, X: str(pattern)}, call("cfnScalarStrings", "Repeat", raw, count))
	return &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: checked}}}
}
