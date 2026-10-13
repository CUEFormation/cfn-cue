package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// presenceOnlyAlternatives identifies the extractor's predicates for anyOf
// branches that merely require named properties to exist. Keep these as
// matchN predicates: a CUE disjunction also retains branches whose required
// fields are absent, which can leave otherwise concrete objects incomplete.
// Recursive value schemas still use direct disjunctions to avoid repeatedly
// evaluating every intrinsic alternative through matchN.
func presenceOnlyAlternatives(alternatives []ast.Expr) bool {
	if len(alternatives) == 0 {
		return false
	}
	for _, alternative := range alternatives {
		for {
			parenthesized, ok := alternative.(*ast.ParenExpr)
			if !ok {
				break
			}
			alternative = parenthesized.X
		}
		predicate, ok := alternative.(*ast.StructLit)
		if !ok {
			return false
		}
		fields, open := 0, false
		for _, declaration := range predicate.Elts {
			switch x := declaration.(type) {
			case *ast.Field:
				if x.Constraint != token.NOT {
					return false
				}
				value, ok := x.Value.(*ast.Ident)
				if !ok || value.Name != "_" {
					return false
				}
				// Only concrete field names are presence checks; patterns
				// and aliases can also constrain values or bind variables.
				switch label := x.Label.(type) {
				case *ast.Ident:
					if ast.IsValidIdent(label.Name) && label.Name[0] != '#' && label.Name[0] != '_' {
						break
					}
					return false
				case *ast.BasicLit:
					if label.Kind != token.STRING {
						return false
					}
				default:
					return false
				}
				fields++
			case *ast.Ellipsis:
				if x.Type != nil {
					return false
				}
				open = true
			default:
				return false
			}
		}
		if fields == 0 || !open {
			return false
		}
	}
	return true
}
