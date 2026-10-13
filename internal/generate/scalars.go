package generate

import (
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
func addScalarCompatibility(f *ast.File, definitions ...string) (skipped int) {
	numeric := false
	selected := map[string]bool{}
	for _, name := range definitions {
		selected[name] = true
	}
	astutil.Apply(f, func(cur astutil.Cursor) bool {
		if field, ok := cur.Node().(*ast.Field); ok && len(selected) != 0 && cur.Parent() != nil {
			if _, rootField := cur.Parent().Node().(*ast.File); rootField {
				label, ok := field.Label.(*ast.Ident)
				if !ok || !selected[label.Name] {
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
		// Existing unconstrained strings already accept every quoted scalar.
		// Preserve those unions rather than adding redundant parsing branches.
		if permitsAnyString(expr) {
			return false
		}
		info := scalarExprInfo(expr)
		if info.blocked {
			if info.typed {
				skipped++
				return false
			}
			// A matchN predicate must retain its original alternatives. A
			// surrounding object intersection still has independent property
			// schemas to visit, such as requiredXor & close({FromPort: int}).
			if _, call := expr.(*ast.CallExpr); call {
				return false
			}
			return true
		}
		if info.kind == 0 || !info.typed || info.other {
			return true
		}
		// Do not descend into an enum/const expression: widening its int/bool
		// conjunct separately would create an escape hatch around its allowed values.
		if info.constant {
			skipped++
			return false
		}
		if info.kind == scalarBoolean {
			if ident, ok := expr.(*ast.Ident); !ok || ident.Name != "bool" {
				return false
			}
			cur.Replace(booleanCompatibility(expr))
			return false
		}
		if info.kind != scalarInteger && info.kind != scalarNumber {
			return true
		}
		numeric = true
		cur.Replace(union(expr, quotedNumber(expr, info.kind == scalarInteger)))
		return false
	}, nil)
	if numeric {
		addImport(f, "cfnScalarStrings", "strings")
		addImport(f, "cfnScalarStrconv", "strconv")
	}
	return skipped
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
			return info
		case token.ADD, token.SUB:
			return scalarExprInfo(x.X)
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
