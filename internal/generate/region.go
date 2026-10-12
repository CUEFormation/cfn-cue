package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
	"fmt"
)

func Region(module string, resources map[string]string, extensions bool) *ast.File {
	f := file("cf")
	addImport(f, "base", module+"/template:template")
	types := &ast.StructLit{}
	alternatives := []ast.Expr{}
	for i, typ := range sortedStringKeys(resources) {
		alias := fmt.Sprintf("r%d", i)
		addImport(f, alias, module+"/resources/h"+resources[typ]+":schema")
		v := sel(alias, "Resource")
		alternatives = append(alternatives, v)
		types.Elts = append(types.Elts, &ast.Field{Label: ast.NewString(typ), Value: v})
	}
	f.Decls = append(f.Decls, def("ResourceTypes", types), def("Resource", union(alternatives...)))
	collection := pattern(sel("base", "LogicalID"), id("#Resource"))
	if extensions {
		addImport(f, "fn", module+"/fn:fn")
		f.Decls = append(f.Decls, def("ForEach", tuple(id("string"), union(list(id("string")), sel("fn", "Ref")), id("#Fragment"))))
		fragment := pattern(and(id("string"), &ast.UnaryExpr{Op: token.NMAT, X: str("^Fn::ForEach::")}), id("#Resource"))
		// Fragment keys may include ${Identifier} / &{Identifier} substitutions.
		fragment.Elts = append(fragment.Elts, &ast.Field{Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^Fn::ForEach::[A-Za-z0-9]+$")}), Value: id("#ForEach")})
		f.Decls = append(f.Decls, def("Fragment", fragment))
		collection.Elts = append(collection.Elts, &ast.Field{Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^Fn::ForEach::[A-Za-z0-9]+$")}), Value: id("#ForEach")})
	}
	f.Decls = append(f.Decls, def("Resources", collection), def("Template", and(sel("base", "Template"), obj(field("Resources", id("#Resources"), false)))))
	return f
}
