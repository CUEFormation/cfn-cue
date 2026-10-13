package generate

import (
	"strings"

	"github.com/cueformation/cfn-cue/internal/cfn"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

func Region(module string, resources map[string]string, extensions bool) *ast.File {
	f := file("cf")
	addImport(f, "base", module+"/template:template")
	types := &ast.StructLit{}
	alternatives := []ast.Expr{}
	for _, typ := range sortedStringKeys(resources) {
		alias := strings.ReplaceAll(typ, "::", "_")
		addImport(f, alias, module+"/resources/h"+resources[typ]+":schema")
		v := sel(alias, "Resource")
		alternatives = append(alternatives, v)
		types.Elts = append(types.Elts, &ast.Field{Label: ast.NewString(typ), Value: v})
	}
	f.Decls = append(f.Decls, def("ResourceTypes", types), def("Resource", union(alternatives...)))
	regionTemplate(f, module, extensions)
	return f
}

// RegionTypes checks regional availability without importing resource property
// schemas. Keep it in a separate package so loading it never loads the full
// registry or resource union produced by Region.
func RegionTypes(module string, resources map[string]string, extensions bool) *ast.File {
	// A different package name prevents CUE from inheriting the parent region's
	// cf files and accidentally loading its full registry.
	f := file("types")
	addImport(f, "base", module+"/template:template")
	names := []ast.Expr{}
	for _, typ := range sortedStringKeys(resources) {
		names = append(names, str(typ))
	}
	// Custom resource names are supported by the full CustomResource schema too.
	// Availability of that provider type controls whether these names are allowed.
	if _, ok := resources[cfn.CustomResourceType]; ok {
		names = append(names, &ast.UnaryExpr{Op: token.MAT, X: str(cfn.CustomResourcePattern)})
	}
	f.Decls = append(f.Decls,
		def("ResourceType", union(names...)),
		def("Resource", and(sel("base", "ResourceAttributes"), obj(field("Type", id("#ResourceType"), false)))),
	)
	regionTemplate(f, module, extensions)
	return f
}

func regionTemplate(f *ast.File, module string, extensions bool) {
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
}
