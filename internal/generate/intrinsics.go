package generate

import (
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
	"fmt"
	"regexp"
	"strings"
)

// Intrinsics constructs a finite graph of named, structurally guarded CUE
// definitions. References are never recursively expanded during generation.
func Intrinsics(extensions bool) *ast.File {
	f := file("fn")
	addImport(f, "structs", "struct")
	addImport(f, "list", "list")
	addImport(f, "cfnScalarStrings", "strings")
	addImport(f, "cfnScalarStrconv", "strconv")
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
	classLiterals := map[string]ast.Expr{}
	for _, name := range []string{"String", "Number", "Boolean", "List", "Object"} {
		literal := map[string]ast.Expr{"String": id("string"), "Number": id("number"), "Boolean": id("bool"), "List": r("List"), "Object": r("Object")}[name]
		// Coercion belongs to typed CloudFormation value branches. Keep
		// generic JSON scalars and function argument shapes unchanged.
		if name == "Number" {
			literal = union(literal, quotedNumber(id("number"), false))
		} else if name == "Boolean" {
			literal = booleanCompatibility(literal)
		}
		classLiterals[name] = literal
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
	// A union of return classes needs one function table, not a disjunction
	// of tables with different optional Fn::If fields. Shared functions such
	// as Ref would otherwise match several distinct, incomplete alternatives.
	classNames := []string{"String", "Number", "Boolean", "List", "Object"}
	classFunctions := [][]string{stringNames, numberNames, boolNames, listNames, objectNames}
	for mask := 1; mask < 1<<len(classNames); mask++ {
		if mask&(mask-1) == 0 {
			continue // single-class definitions already exist above
		}
		name := ""
		var values []ast.Expr
		var names []string
		for i, class := range classNames {
			if mask&(1<<i) == 0 {
				continue
			}
			name += class
			values = append(values, classLiterals[class])
			for _, function := range classFunctions[i] {
				if !strings.HasPrefix(function, "If") && !includes(names, function) {
					names = append(names, function)
				}
			}
		}
		branch := union(append(values, r(name+"Expression"))...)
		fn("If"+name, "Fn::If", tuple(id("string"), branch, branch))
		add(name+"Expression", refs(append(names, "If"+name)...))
	}
	all := []string{"Ref", "GetAtt", "FindInMap", "Select", "Transform", "Base64", "ImportValue", "Join", "Sub", "GetStackOutput", "If", "Cidr", "GetAZs", "Split"}
	if extensions {
		all = append(all, "Length", "ToJsonString")
	}
	add("ValueExpression", refs(all...))
	names := map[string]string{}
	for name := range signatures {
		names[name] = name
	}
	// Keys are pattern constraints, which apply only to keys that are
	// present. As optional fields, every argument schema was evaluated each
	// time a function struct appeared in a disjunction, which is once per
	// property: about 25 ms per unset property in CUE v0.18. Regex labels
	// keep format.Simplify from turning ["Ref"] back into Ref?.
	keyed := func(fields []ast.Decl) ast.Expr {
		return and(ast.NewCall(id("close"), &ast.StructLit{Elts: fields}),
			ast.NewCall(ast.NewSel(ast.NewIdent("structs"), "MinFields"), ast.NewLit(token.INT, "1")),
			ast.NewCall(ast.NewSel(ast.NewIdent("structs"), "MaxFields"), ast.NewLit(token.INT, "1")))
	}
	key := func(k string, args ast.Expr) ast.Decl {
		return &ast.Field{Label: ast.NewList(&ast.UnaryExpr{Op: token.MAT, X: str("^" + regexp.QuoteMeta(k) + "$")}), Value: args}
	}
	functionFields := []ast.Decl{}
	for _, name := range sortedStringKeys(names) {
		v := signatures[name]
		if name == "NoValue" || (strings.HasPrefix(name, "If") && name != "If") {
			continue
		}
		args := v.args
		if name == "If" {
			args = id("_")
		}
		functionFields = append(functionFields, key(v.key, args))
	}
	add("Function", keyed(functionFields))
	restriction := func(ns []string) ast.Expr {
		fields := []ast.Decl{}
		for _, name := range ns {
			fields = append(fields, key(signatures[name].key, signatures[name].args))
		}
		return keyed(fields)
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
