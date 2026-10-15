package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

func Template(module string, extensions, sam bool) *ast.File {
	f := file("template")
	addImport(f, "fn", module+"/fn:fn")
	addImport(f, "structs", "struct")
	// CloudFormation logical IDs are alphanumeric and may start with a digit,
	// as in widdix's 5xxErrorRateTooHighAlarm.
	logical := &ast.UnaryExpr{Op: token.MAT, X: str("^[A-Za-z0-9]{1,255}$")}
	add := func(name string, x ast.Expr) { f.Decls = append(f.Decls, def(name, x)) }
	add("LogicalID", logical)
	// Fn::If can choose a whole UpdatePolicy or CreationPolicy, but other
	// intrinsics such as Ref cannot stand in for the object.
	branch := union(id("#Policy"), sel("fn", "NoValue"))
	condition := ast.NewCall(id("close"), &ast.StructLit{Elts: []ast.Decl{&ast.Field{
		Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^Fn::If$")}), Value: tuple(id("string"), branch, branch)}}})
	add("Policy", union(sel("fn", "Object"), and(condition, ast.NewCall(ast.NewSel(id("structs"), "MinFields"), ast.NewLit(token.INT, "1")))))
	// Condition names may also contain & and _, as in the VPC Quick Start's
	// NATGateway&3AZCondition. This matches cfn-lint's condition schema.
	add("ConditionName", &ast.UnaryExpr{Op: token.MAT, X: str("^[A-Za-z0-9&_]{1,255}$")})
	add("ResourceAttributes", obj(
		field("Type", id("string"), false), field("Properties", id("_"), true), field("Condition", id("string"), true),
		field("DependsOn", union(id("string"), list(id("string"))), true), field("Metadata", sel("fn", "Object"), true),
		field("DeletionPolicy", union(str("Delete"), str("Retain"), str("Snapshot"), str("RetainExceptOnCreate"), sel("fn", "StringExpression")), true),
		field("UpdateReplacePolicy", union(str("Delete"), str("Retain"), str("Snapshot"), sel("fn", "StringExpression")), true),
		field("CreationPolicy", id("#Policy"), true), field("UpdatePolicy", id("#Policy"), true), field("Version", union(id("string"), id("int")), true)))
	// This envelope is shared; per-region packages narrow Type and Properties.
	parameter := obj(field("Type", union(str("String"), str("Number"), str("List<Number>"), str("CommaDelimitedList"), &ast.UnaryExpr{Op: token.MAT, X: str("^(AWS::[A-Za-z0-9:<>,]+|List<AWS::[A-Za-z0-9:]+>)$")}), false))
	for _, name := range []string{"AllowedPattern", "ConstraintDescription", "Description"} {
		parameter.Elts = append(parameter.Elts, field(name, id("string"), true))
	}
	// YAML reads AllowedValues: [true, false] as booleans. CloudFormation
	// uses their string form, as it does for numbers.
	value := func() ast.Expr { return union(id("string"), id("number"), id("bool")) }
	parameter.Elts = append(parameter.Elts, field("Default", value(), true), field("AllowedValues", list(value()), true), field("NoEcho", id("bool"), true))
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
		// Mapping values may be YAML booleans, as in PerformanceInsights: true.
		field("Mappings", pattern(id("#LogicalID"), pattern(id("string"), pattern(id("string"), union(value(), list(value()))))), true),
		field("Conditions", pattern(id("#ConditionName"), sel("fn", "ConditionExpression")), true),
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
	// Other string fields, such as Description, keep their string type.
	addQuotedScalarCompatibility(f, "#Parameter")
	return f
}
