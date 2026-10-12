package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
	"fmt"
	"strings"
)

// Intrinsics constructs a finite graph of named, structurally guarded CUE
// definitions. References are never recursively expanded during generation.
func Intrinsics(extensions bool) *ast.File {
	f := file("fn")
	addImport(f, "structs", "struct")
	addImport(f, "list", "list")
	r := func(s string) ast.Expr { return id("#" + s) }
	type sig struct {
		key  string
		args ast.Expr
	}
	signatures := map[string]sig{}
	choices := [][]string{}
	refs := func(names ...string) ast.Expr {
		choices = append(choices, names)
		return r(fmt.Sprintf("Choice%d", len(choices)-1))
	}
	add := func(name string, x ast.Expr) { f.Decls = append(f.Decls, def(name, x)) }
	fn := func(name, key string, args ast.Expr) { signatures[name] = sig{key, args} }
	anyString := func(names ...string) ast.Expr { return union(id("string"), refs(names...)) }
	add("Scalar", union(id("string"), id("number"), id("bool"), ast.NewNull()))
	// Reserve intrinsic spellings so malformed functions cannot pass as an
	// arbitrary JSON object in unconstrained properties or nested branches.
	label := and(id("string"), &ast.UnaryExpr{Op: token.NEQ, X: str("Ref")}, &ast.UnaryExpr{Op: token.NMAT, X: str("^Fn::")})
	add("Object", pattern(label, r("Value")))
	add("List", list(r("Value")))
	add("Value", union(r("Scalar"), r("Object"), r("List"), r("ValueExpression")))
	fn("NoValue", "Ref", str("AWS::NoValue"))
	getAttName := id("string")
	refName := id("string")
	if extensions {
		getAttName = r("String")
		refName = anyString("Ref", "Base64", "FindInMap", "IfString", "Join", "Sub", "ToJsonString")
	}
	fn("Ref", "Ref", refName)
	fn("GetAtt", "Fn::GetAtt", union(&ast.UnaryExpr{Op: token.MAT, X: str("^[A-Za-z0-9]+\\..+$")}, tuple(getAttName, anyString("Ref"))))
	fn("Condition", "Condition", id("string"))
	condition := union(id("bool"), refs("Condition", "And", "Or", "Not", "Equals", "Contains", "EachMemberEquals", "EachMemberIn"))
	logicalBounds := and(ast.NewCall(ast.NewSel(ast.NewIdent("list"), "MinItems"), ast.NewLit(token.INT, "2")), ast.NewCall(ast.NewSel(ast.NewIdent("list"), "MaxItems"), ast.NewLit(token.INT, "10")))
	logicalArgs := and(list(condition), ast.NewCall(id("matchN"), ast.NewLit(token.INT, "1"), ast.NewList(logicalBounds)))
	fn("And", "Fn::And", logicalArgs)
	fn("Or", "Fn::Or", logicalArgs)
	fn("Not", "Fn::Not", tuple(condition))
	fn("Equals", "Fn::Equals", tuple(r("RuleValue"), r("RuleValue")))
	add("RuleValueExpression", refs("RefAll", "ValueOf", "ValueOfAll"))
	add("RuleValue", union(r("Value"), r("RuleValueExpression")))
	add("RuleList", union(r("ListValue"), refs("RefAll", "ValueOfAll")))
	fn("Contains", "Fn::Contains", tuple(r("RuleList"), r("RuleValue")))
	fn("EachMemberEquals", "Fn::EachMemberEquals", tuple(r("RuleList"), r("RuleValue")))
	fn("EachMemberIn", "Fn::EachMemberIn", tuple(r("RuleList"), r("RuleList")))
	fn("RefAll", "Fn::RefAll", id("string"))
	fn("ValueOf", "Fn::ValueOf", tuple(id("string"), id("string")))
	fn("ValueOfAll", "Fn::ValueOfAll", tuple(id("string"), id("string")))
	add("ConditionExpression", union(id("bool"), condition))
	fn("If", "Fn::If", tuple(id("string"), r("Value"), r("Value")))
	for _, name := range []string{"String", "Number", "Boolean", "List", "Object"} {
		literal := map[string]ast.Expr{"String": id("string"), "Number": id("number"), "Boolean": id("bool"), "List": r("List"), "Object": r("Object")}[name]
		add(name+"Value", union(literal, r(name+"Expression")))
		fn("If"+name, "Fn::If", tuple(id("string"), r(name+"Value"), r(name+"Value")))
	}
	add("String", r("StringValue"))
	add("Number", r("NumberValue"))
	fn("Base64", "Fn::Base64", r("String"))
	fn("GetAZs", "Fn::GetAZs", union(id("string"), r("Ref")))
	count := union(and(id("int"), &ast.UnaryExpr{Op: token.GEQ, X: ast.NewLit(token.INT, "1")}, &ast.UnaryExpr{Op: token.LEQ, X: ast.NewLit(token.INT, "256")}), r("NumberExpression"))
	bits := union(and(id("int"), &ast.UnaryExpr{Op: token.GEQ, X: ast.NewLit(token.INT, "0")}, &ast.UnaryExpr{Op: token.LEQ, X: ast.NewLit(token.INT, "128")}), r("NumberExpression"))
	fn("Cidr", "Fn::Cidr", tuple(r("String"), count, bits))
	fim := r("String")
	fn("FindInMap", "Fn::FindInMap", union(tuple(fim, fim, fim), tuple(fim, fim, fim, obj(field("DefaultValue", r("Value"), false)))))
	fn("ImportValue", "Fn::ImportValue", r("String"))
	joinMember := r("String")
	fn("Join", "Fn::Join", tuple(id("string"), union(list(joinMember), r("ListExpression"))))
	fn("Split", "Fn::Split", tuple(id("string"), r("String")))
	index := union(and(id("int"), &ast.UnaryExpr{Op: token.GEQ, X: ast.NewLit(token.INT, "0")}), &ast.UnaryExpr{Op: token.MAT, X: str("^[0-9]+$")}, r("NumberExpression"))
	fn("Select", "Fn::Select", tuple(index, r("ListValue")))
	fn("Sub", "Fn::Sub", union(id("string"), tuple(id("string"), pattern(&ast.UnaryExpr{Op: token.MAT, X: str("^[A-Za-z0-9_.:]+$")}, joinMember))))
	fn("GetStackOutput", "Fn::GetStackOutput", obj(field("StackName", r("String"), false), field("OutputName", r("String"), false), field("Region", r("String"), true), field("RoleArn", r("String"), true)))
	fn("Transform", "Fn::Transform", obj(field("Name", id("string"), false), field("Parameters", r("Object"), true)))
	dynamic := []string{"Ref", "GetAtt", "FindInMap", "Select", "Transform"}
	stringNames := append(append([]string{}, dynamic...), "Base64", "ImportValue", "Join", "Sub", "GetStackOutput", "IfString")
	numberNames := append(append([]string{}, dynamic...), "GetStackOutput", "IfNumber")
	boolNames := append(append([]string{}, dynamic...), "GetStackOutput", "IfBoolean")
	listNames := append(append([]string{}, dynamic...), "Cidr", "GetAZs", "Split", "IfList")
	objectNames := append(append([]string{}, dynamic...), "IfObject")
	if extensions {
		fn("Length", "Fn::Length", r("ListValue"))
		fn("ToJsonString", "Fn::ToJsonString", union(r("ObjectValue"), r("ListValue")))
		stringNames = append(stringNames, "ToJsonString")
		numberNames = append(numberNames, "Length")
	}
	add("StringExpression", refs(stringNames...))
	add("NumberExpression", refs(numberNames...))
	add("BooleanExpression", refs(boolNames...))
	add("ListExpression", refs(listNames...))
	add("ObjectExpression", refs(objectNames...))
	all := []string{"Ref", "GetAtt", "FindInMap", "Select", "Transform", "Base64", "ImportValue", "Join", "Sub", "GetStackOutput", "If", "Cidr", "GetAZs", "Split"}
	if extensions {
		all = append(all, "Length", "ToJsonString")
	}
	add("ValueExpression", refs(all...))
	functionFields := []*ast.Field{}
	names := map[string]string{}
	for name := range signatures {
		names[name] = name
	}
	for _, name := range sortedStringKeys(names) {
		v := signatures[name]
		if name == "NoValue" || (strings.HasPrefix(name, "If") && name != "If") {
			continue
		}
		args := v.args
		if name == "If" {
			args = id("_")
		}
		functionFields = append(functionFields, field(v.key, args, true))
	}
	add("Function", and(obj(functionFields...), ast.NewCall(ast.NewSel(ast.NewIdent("structs"), "MinFields"), ast.NewLit(token.INT, "1")), ast.NewCall(ast.NewSel(ast.NewIdent("structs"), "MaxFields"), ast.NewLit(token.INT, "1"))))
	restriction := func(ns []string) ast.Expr {
		fields := []*ast.Field{}
		for _, name := range ns {
			v := signatures[name]
			args := id("_")
			if name == "NoValue" || strings.HasPrefix(name, "If") {
				args = v.args
			}
			fields = append(fields, field(v.key, args, true))
		}
		return and(r("Function"), ast.NewCall(id("close"), obj(fields...)))
	}
	for _, name := range sortedStringKeys(names) {
		if name == "NoValue" {
			add(name, obj(field("Ref", str("AWS::NoValue"), false)))
		} else {
			add(name, restriction([]string{name}))
		}
	}
	for i, names := range choices {
		add(fmt.Sprintf("Choice%d", i), restriction(names))
	}
	return f
}
