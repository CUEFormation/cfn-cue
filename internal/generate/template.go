package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

func Template(module string, extensions, sam bool) *ast.File {
	f := file("template")
	addImport(f, "fn", module+"/fn:fn")
	addImport(f, "structs", "struct")
	logical := &ast.UnaryExpr{Op: token.MAT, X: str("^[A-Za-z][A-Za-z0-9]{0,254}$")}
	add := func(name string, x ast.Expr) { f.Decls = append(f.Decls, def(name, x)) }
	add("LogicalID", logical)
	add("ResourceAttributes", obj(
		field("Type", id("string"), false), field("Properties", id("_"), true), field("Condition", id("string"), true),
		field("DependsOn", union(id("string"), list(id("string"))), true), field("Metadata", sel("fn", "Object"), true),
		field("DeletionPolicy", union(str("Delete"), str("Retain"), str("Snapshot"), str("RetainExceptOnCreate"), sel("fn", "StringExpression")), true),
		field("UpdateReplacePolicy", union(str("Delete"), str("Retain"), str("Snapshot"), sel("fn", "StringExpression")), true),
		field("CreationPolicy", sel("fn", "Object"), true), field("UpdatePolicy", sel("fn", "Object"), true), field("Version", union(id("string"), id("int")), true)))
	// This envelope is shared; per-region packages narrow Type and Properties.
	parameter := obj(field("Type", union(str("String"), str("Number"), str("List<Number>"), str("CommaDelimitedList"), &ast.UnaryExpr{Op: token.MAT, X: str("^(AWS::[A-Za-z0-9:<>,]+|List<AWS::[A-Za-z0-9:]+>)$")}), false))
	for _, name := range []string{"AllowedPattern", "ConstraintDescription", "Description"} {
		parameter.Elts = append(parameter.Elts, field(name, id("string"), true))
	}
	parameter.Elts = append(parameter.Elts, field("Default", union(id("string"), id("number")), true), field("AllowedValues", list(union(id("string"), id("number"))), true), field("NoEcho", id("bool"), true))
	for _, name := range []string{"MaxLength", "MinLength"} {
		parameter.Elts = append(parameter.Elts, field(name, and(id("int"), &ast.UnaryExpr{Op: token.GEQ, X: ast.NewLit(token.INT, "0")}), true))
	}
	for _, name := range []string{"MaxValue", "MinValue"} {
		parameter.Elts = append(parameter.Elts, field(name, id("number"), true))
	}
	add("Parameter", parameter)
	add("Output", obj(field("Value", sel("fn", "Value"), false), field("Description", id("string"), true), field("Condition", id("string"), true), field("Export", obj(field("Name", sel("fn", "String"), false)), true)))
	add("Rule", obj(field("RuleCondition", sel("fn", "ConditionExpression"), true), field("Assertions", list(obj(field("Assert", sel("fn", "ConditionExpression"), false), field("AssertDescription", id("string"), true))), false)))
	transform := union(id("string"), list(id("string")))
	if extensions {
		addImport(f, "list", "list")
		transform = union(str("AWS::LanguageExtensions"), and(list(id("string")), ast.NewCall(ast.NewSel(ast.NewIdent("list"), "Contains"), str("AWS::LanguageExtensions"))))
	}
	template := obj(
		field("AWSTemplateFormatVersion", str("2010-09-09"), true), field("Description", id("string"), true), field("Metadata", sel("fn", "Object"), true),
		field("Parameters", pattern(id("#LogicalID"), id("#Parameter")), true),
		field("Mappings", pattern(id("#LogicalID"), pattern(id("string"), pattern(id("string"), union(id("string"), id("number"), list(id("string")))))), true),
		field("Conditions", pattern(id("#LogicalID"), sel("fn", "ConditionExpression")), true),
		field("Rules", pattern(id("#LogicalID"), id("#Rule")), true),
		field("Resources", and(&ast.StructLit{Elts: []ast.Decl{&ast.Ellipsis{}}}, ast.NewCall(ast.NewSel(ast.NewIdent("structs"), "MinFields"), ast.NewLit(token.INT, "1"))), false),
		field("Outputs", pattern(id("#LogicalID"), id("#Output")), true), field("Transform", transform, !extensions))
	if sam {
		template.Elts = append(template.Elts, field("Globals", sel("fn", "Object"), true))
	}
	add("Template", template)
	// Parameter bounds have the same quoted scalar compatibility as resource
	// properties. Select only this definition so unrelated template fields and
	// the existing string alternatives in Default/AllowedValues stay intact.
	addScalarCompatibility(f, "#Parameter")
	return f
}
