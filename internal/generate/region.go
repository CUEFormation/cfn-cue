package generate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cueformation/cfn-cue/internal/bundle"
	"github.com/cueformation/cfn-cue/internal/cfn"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/mod/module"
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
		addImport(f, alias, bundle.ResourceImport(module, resources[typ]))
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

// typeSegment keeps each part of a per-type package path a valid CUE
// identifier. A leading capital also keeps the package name distinct from cf
// and types, which CUE would otherwise merge from parent directories.
var typeSegment = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// checkTypePaths rejects types without a per-type package path, paths that
// CUE's module system rejects, such as Windows device names, and paths that
// differ only in case from each other or from a generated root directory,
// which collide on case-insensitive file systems.
func checkTypePaths(types []string) error {
	folded := map[string]string{}
	for _, dir := range bundle.TopLevelDirs {
		folded[dir] = dir
	}
	for _, typ := range types {
		parts := strings.Split(typ, "::")
		if len(parts) != 3 {
			return fmt.Errorf("resource type %q does not have three segments", typ)
		}
		for i, part := range parts {
			if !typeSegment.MatchString(part) {
				return fmt.Errorf("resource type %q has segment %q, which is not a capitalized CUE identifier", typ, part)
			}
			prefix := strings.Join(parts[:i+1], "::")
			key := strings.ToLower(prefix)
			if previous, ok := folded[key]; ok && previous != prefix {
				return fmt.Errorf("resource types %q and %q produce package paths that differ only in case", previous, prefix)
			}
			folded[key] = prefix
		}
		if err := module.CheckImportPath(bundle.TypeDir(typ)); err != nil {
			return fmt.Errorf("resource type %q: %w", typ, err)
		}
	}
	return nil
}

// TypePackage re-exports one shared resource schema's definitions under a
// stable path, so authors can import a type without its hash.
func TypePackage(module, typ, hash string, definitions []string) *ast.File {
	f := file(bundle.TypePackage(typ))
	addImport(f, "schema", bundle.ResourceImport(module, hash))
	for _, name := range definitions {
		f.Decls = append(f.Decls, &ast.Field{Label: ast.NewIdent(name), Value: ast.NewSel(id("schema"), name)})
	}
	return f
}

// definitions lists a package's top-level definitions in source order.
func definitions(f *ast.File) []string {
	names := []string{}
	for _, d := range f.Decls {
		if field, ok := d.(*ast.Field); ok {
			if label, ok := field.Label.(*ast.Ident); ok && strings.HasPrefix(label.Name, "#") {
				names = append(names, label.Name)
			}
		}
	}
	return names
}
