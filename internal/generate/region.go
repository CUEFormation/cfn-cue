package generate

import (
	"github.com/cueformation/cfn-cue/internal/cfn"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

func Region(module string, resources map[string]string, extensions bool) (*ast.File, error) {
	names := sortedStringKeys(resources)
	aliases, err := cfn.ResourceAliases(names)
	if err != nil {
		return nil, err
	}
	f := file("cf")
	addImport(f, "base", module+"/template:template")
	types := &ast.StructLit{}
	for _, typ := range names {
		alias := aliases[typ]
		addImport(f, alias, module+"/resources/h"+resources[typ]+":schema")
		types.Elts = append(types.Elts, &ast.Field{Label: ast.NewString(typ), Value: sel(alias, "Resource")})
	}
	f.Decls = append(f.Decls, def("ResourceTypes", types))
	_, custom := resources[cfn.CustomResourceType]
	regionTemplate(f, module, extensions, selectResource(custom))
	return f, nil
}

// selectResource checks each resource against #ResourceTypes[Type]. A union
// of every resource schema would try each type in the region for every
// resource: about 0.3 ms per type in CUE v0.18, or 0.5 s per resource in a
// complete region, so the full package defines no such union. CloudFormation requires Type to be a literal string, so
// the lookup is always concrete. Custom:: names select the CustomResource
// schema, whose Type pattern then checks the name.
func selectResource(custom bool) func(ast.Expr) *ast.Field {
	return func(label ast.Expr) *ast.Field {
		lookup := func(key ast.Expr) ast.Expr {
			return &ast.IndexExpr{X: id("#ResourceTypes"), Index: key}
		}
		r := &ast.StructLit{Elts: []ast.Decl{field("Type", id("string"), false)}}
		typ := ast.NewSel(id("R"), "Type")
		if custom {
			r.Elts = append(r.Elts,
				&ast.Comprehension{Clauses: []ast.Clause{&ast.IfClause{Condition: &ast.BinaryExpr{X: typ, Op: token.MAT, Y: str("^Custom::")}}},
					Value: &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: lookup(str(cfn.CustomResourceType))}}}},
				&ast.Comprehension{Clauses: []ast.Clause{&ast.IfClause{Condition: &ast.BinaryExpr{X: ast.NewSel(id("R"), "Type"), Op: token.NMAT, Y: str("^Custom::")}}},
					Value: &ast.StructLit{Elts: []ast.Decl{&ast.EmbedDecl{Expr: lookup(ast.NewSel(id("R"), "Type"))}}}})
		} else {
			r.Elts = append(r.Elts, &ast.EmbedDecl{Expr: lookup(typ)})
		}
		return &ast.Field{Label: ast.NewList(label), Alias: &ast.PostfixAlias{Label: ast.NewIdent("_"), Field: ast.NewIdent("R")}, Value: r}
	}
}

// matchResource checks resources against #Resource.
func matchResource(label ast.Expr) *ast.Field {
	return &ast.Field{Label: ast.NewList(label), Value: id("#Resource")}
}

// RegionTypes checks regional availability without importing resource property
// schemas. Keep it in a separate package so loading it never loads the full
// registry produced by Region.
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
	regionTemplate(f, module, extensions, matchResource)
	return f
}

func regionTemplate(f *ast.File, module string, extensions bool, resource func(ast.Expr) *ast.Field) {
	collection := &ast.StructLit{Elts: []ast.Decl{resource(sel("base", "LogicalID"))}}
	if extensions {
		addImport(f, "fn", module+"/fn:fn")
		f.Decls = append(f.Decls, def("ForEach", tuple(id("string"), union(list(id("string")), sel("fn", "Ref")), id("#Fragment"))))
		fragment := &ast.StructLit{Elts: []ast.Decl{resource(and(id("string"), &ast.UnaryExpr{Op: token.NMAT, X: str("^Fn::ForEach::")}))}}
		// Fragment keys may include ${Identifier} / &{Identifier} substitutions.
		fragment.Elts = append(fragment.Elts, &ast.Field{Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^Fn::ForEach::[A-Za-z0-9]+$")}), Value: id("#ForEach")})
		f.Decls = append(f.Decls, def("Fragment", fragment))
		collection.Elts = append(collection.Elts, &ast.Field{Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^Fn::ForEach::[A-Za-z0-9]+$")}), Value: id("#ForEach")})
	}
	f.Decls = append(f.Decls, def("Resources", collection), def("Template", and(sel("base", "Template"), obj(field("Resources", id("#Resources"), false)))))
}
