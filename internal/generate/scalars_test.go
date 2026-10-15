package generate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/parser"
)

func TestScalarCompatibilityAST(t *testing.T) {
	source := `package schema
 import "strings"
 #Port: int & >=-1 & <=65535
 #Ratio: number & >0 & <2
 #IntegerEnum: int & (2|4)
 #BooleanEnum: bool & true
 #Properties: {
  Size?: =~"^[0-9]+$" & strings.MaxRunes(3)
  Flag?: =~"^(true|false)$"
  Protocol?: "tcp" | "-1" | "true" | "1.5" | "01"
  Const?: "SIMPLE" & string
  Mixed?: =~"^[a-z]+$" | int & >=0
  StringOrInt?: string | int & >=0
  Enabled?: bool
  Count?: int & >=1 & <=4
  Port?: #Port
  Ratio?: #Ratio
  NumericUnion?: (int|number) & >=0 & <=2
  StringOrObject?: string|{Count?:int & >=1 & <=4}
  Enum?: #IntegerEnum
  BooleanEnum?: #BooleanEnum
  Nested?: {Ports?: [...#Port], Map?: {[string]: #Port}}
  Name?: string
 }
 `
	f, err := parser.ParseFile("scalar.cue", source)
	if err != nil {
		t.Fatal(err)
	}
	addScalarCompatibility(f)
	generated, err := render(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cuecontext.New()
	v := ctx.CompileBytes(generated)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	properties := v.LookupPath(cue.MakePath(cue.Def("Properties")))
	cases := []struct {
		name, data string
		valid      bool
	}{
		{"native", `{Enabled:true,Count:2,Port:22,Ratio:0.5}`, true},
		{"quoted", `{Enabled:"true",Count:"2",Port:"22",Ratio:".5"}`, true},
		{"case", `{Enabled:"FALSE"}`, true},
		{"leading-zeros", `{Count:"002"}`, true},
		{"whitespace-and-sign", `{Count:" +2 ",Port:" -1 "}`, true},
		{"underscores", `{Port:"6_5_5_3_5",Ratio:"1_0e-1"}`, true},
		{"exponent", `{Ratio:"1e-2"}`, true},
		{"trailing-dot", `{Ratio:"1."}`, true},
		{"numeric-union", `{NumericUnion:"0.5"}`, true},
		{"numeric-union-bound", `{NumericUnion:"2.5"}`, false},
		{"string-or-object", `{StringOrObject:{Count:"2"}}`, true},
		{"string-or-object-bound", `{StringOrObject:{Count:"5"}}`, false},
		{"nested-reference-items", `{Nested:{Ports:["22","80"],Map:{one:"443"}}}`, true},
		{"string-from-number", `{Name:22,Size:"12"}`, true},
		{"string-from-bool", `{Name:true}`, true},
		{"string-form-pattern", `{Size:12}`, true},
		{"string-form-negative", `{Size:-1}`, false},
		{"string-form-fraction", `{Size:1.5}`, false},
		{"string-form-length", `{Size:1234}`, false},
		{"string-form-bool", `{Size:true}`, false},
		{"string-form-true", `{Flag:true}`, true},
		{"string-form-number", `{Flag:1}`, false},
		{"string-enum", `{Protocol:"-1"}`, true},
		{"string-enum-number", `{Protocol:-1}`, true},
		{"string-enum-bool", `{Protocol:true}`, true},
		{"string-enum-float", `{Protocol:1.5}`, true},
		{"string-enum-other-number", `{Protocol:1}`, false},
		{"string-enum-noncanonical", `{Protocol:"01"}`, true},
		{"string-const", `{Const:"SIMPLE"}`, true},
		{"string-or-int-negative", `{StringOrInt:-1}`, false},
		{"string-or-int-bool", `{StringOrInt:false}`, true},
		{"string-or-int-quoted", `{StringOrInt:"-1"}`, true},
		{"numeric-sibling", `{Mixed:2}`, true},
		{"numeric-sibling-bound", `{Mixed:-2}`, false},
		{"numeric-sibling-quoted", `{Mixed:"2"}`, true},
		{"string-null", `{Name:null}`, false},
		{"string-list", `{Name:[1]}`, false},
		{"malformed-bool", `{Enabled:"yes"}`, false},
		{"mixed-bool-case", `{Enabled:"tRuE"}`, false},
		{"bool-shortcuts", `{Enabled:"1"}`, false},
		{"spaced-bool", `{Enabled:" true "}`, false},
		{"int-as-bool", `{Enabled:1}`, false},
		{"malformed-int", `{Count:"two"}`, false},
		{"fraction-as-int", `{Count:"2.0"}`, false},
		{"exponent-as-int", `{Count:"2e0"}`, false},
		{"empty-int", `{Count:""}`, false},
		{"int-min", `{Count:"0"}`, false},
		{"int-max", `{Count:"5"}`, false},
		{"port-max", `{Port:"65536"}`, false},
		{"bool-as-int", `{Count:true}`, false},
		{"hex", `{Port:"0x16"}`, false},
		{"bad-underscores", `{Port:"2__2"}`, false},
		{"malformed-number", `{Ratio:"one"}`, false},
		{"number-min", `{Ratio:"0"}`, false},
		{"number-max", `{Ratio:"2.0"}`, false},
		{"hex-number", `{Ratio:"0x1p0"}`, false},
		{"cue-suffix", `{Ratio:"1K"}`, false},
		{"nonfinite", `{Ratio:"NaN"}`, false},
		{"nested-bound", `{Nested:{Ports:["22","65536"]}}`, false},
		{"native-enum", `{Enum:2,BooleanEnum:true}`, true},
		{"enum-rejects-wrong-native", `{Enum:3}`, false},
		{"enum-no-coercion", `{Enum:"2"}`, false},
		{"boolean-enum-no-coercion", `{BooleanEnum:"true"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { validate(t, properties, tc.data, tc.valid) })
	}
	// Conversion is a validation check, never a change to template data.
	input := `{Port:" 0022 ",Enabled:"TRUE"}`
	validated := properties.Unify(ctx.CompileString(input))
	var got map[string]any
	b, err := validated.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["Port"] != " 0022 " || got["Enabled"] != "TRUE" {
		t.Fatalf("original scalars changed: %s", b)
	}
}

func TestScalarCompatibilityPreservesMatchN(t *testing.T) {
	source := `#Properties: { Count?: int & matchN(1,[2,4]), Excluded?: int & matchN(0,[2]) }`
	f, err := parser.ParseFile("scalar.cue", source)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := render(f)
	addScalarCompatibility(f)
	after, _ := render(f)
	if string(before) != string(after) {
		t.Fatalf("matchN predicates were changed:\n%s", after)
	}
	ctx := cuecontext.New()
	schema := ctx.CompileBytes(after).LookupPath(cue.MakePath(cue.Def("Properties")))
	validate(t, schema, `{Count:2,Excluded:3}`, true)
	validate(t, schema, `{Count:"2"}`, false)
	validate(t, schema, `{Excluded:2}`, false)
}

func TestGeneratedScalarCompatibilityAndStrictLiterals(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	props := root["properties"].(map[string]any)
	props["Enabled"] = map[string]any{"type": "boolean"}
	for _, name := range []string{"self", "len", "string", "cfnScalarStrings", "cfnScalarStrconv"} {
		props[name] = map[string]any{"type": "string"}
	}
	props["Ratio"] = map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 2}
	props["NumberEnum"] = map[string]any{"type": "integer", "enum": []any{2, 4}}
	root["definitions"].(map[string]any)["EnumPort"] = map[string]any{"type": "integer", "enum": []any{2, 4}}
	props["RefEnum"] = map[string]any{"type": "integer", "$ref": "#/definitions/EnumPort"}
	props["ChoiceEnum"] = map[string]any{"type": "integer", "oneOf": []any{map[string]any{"enum": []any{2}}, map[string]any{"const": 4}}}
	root["definitions"].(map[string]any)["Port"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 65535}
	props["Ports"] = map[string]any{"type": "array", "items": map[string]any{"$ref": "#/definitions/Port"}}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	template := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Count:"2",Enabled:"True",Ratio:"1.5",Ports:["22","443"]}}}}`, true)
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Count:"2",self:"original",len:"length",string:"type",cfnScalarStrings:"strings",cfnScalarStrconv:"strconv"}}}}`, true)
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Count:"5"}}}}`, false)
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Ports:["65536"]}}}}`, false)
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",NumberEnum:"2"}}}}`, false)
	validate(t, template, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",RefEnum:2,ChoiceEnum:4}}}}`, true)
	for _, data := range []string{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",RefEnum:"2"}}}}`, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",ChoiceEnum:"2"}}}}`, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",ChoiceEnum:3}}}}`} {
		validate(t, template, data, false)
	}
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name:"one",Count:2,Enabled:true,Ratio:1.5,Ports:[22]}`, true)
	for _, data := range []string{`{Name:"one",Count:"2"}`, `{Name:"one",Enabled:"true"}`, `{Name:"one",Ratio:"1.5"}`, `{Name:"one",Ports:["22"]}`} {
		validate(t, literal, data, false)
	}
}

func TestSelectiveScalarDefinitions(t *testing.T) {
	f, err := parser.ParseFile("scalar.cue", `#NumberValue: number
 #BooleanValue: bool
 #StrictArgument: int
 #GenericScalar: string|number|bool
 `)
	if err != nil {
		t.Fatal(err)
	}
	addScalarCompatibility(f, "#NumberValue", "#BooleanValue")
	b, err := render(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "#StrictArgument: int") {
		t.Fatalf("strict argument changed:\n%s", b)
	}
	v := cuecontext.New().CompileBytes(b)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, data string
		good       bool
	}{
		{"NumberValue", `"2"`, true}, {"BooleanValue", `"false"`, true},
		{"NumberValue", `"hello"`, false}, {"StrictArgument", `"2"`, false},
	} {
		validate(t, v.LookupPath(cue.MakePath(cue.Def(tc.name))), tc.data, tc.good)
	}
}

func TestScalarCompatibilityInsideRequiredXorObjects(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	ports := map[string]any{
		"FromPort":      map[string]any{"type": "integer", "minimum": -1},
		"ToPort":        map[string]any{"type": "integer", "minimum": -1},
		"CidrIp":        map[string]any{"type": "string"},
		"SourceGroupId": map[string]any{"type": "string"},
	}
	props := root["properties"].(map[string]any)
	for name, schema := range ports {
		props[name] = schema
	}
	root["requiredXor"] = []any{"CidrIp", "SourceGroupId"}
	props["Rules"] = map[string]any{"type": "array", "items": map[string]any{
		"type": "object", "properties": ports, "additionalProperties": false,
		"requiredXor": []any{"CidrIp", "SourceGroupId"},
	}}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	template := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	cases := []struct {
		data  string
		valid bool
	}{
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",FromPort:"22",ToPort:"22",Rules:[{SourceGroupId:"sg-example",FromPort:"443",ToPort:"443"}]}}}}`, true},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",FromPort:"bad"}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",FromPort:"-2"}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",Rules:[{SourceGroupId:"sg-example",FromPort:"bad"}]}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",FromPort:"22"}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",SourceGroupId:"sg-example",FromPort:"22"}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",Rules:[{FromPort:"22"}]}}}}`, false},
		{`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",CidrIp:"0.0.0.0/0",Rules:[{CidrIp:"0.0.0.0/0",SourceGroupId:"sg-example",FromPort:"22"}]}}}}`, false},
	}
	for _, tc := range cases {
		validate(t, template, tc.data, tc.valid)
	}
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name:"one",CidrIp:"0.0.0.0/0",FromPort:22,Rules:[{SourceGroupId:"sg-example",FromPort:443}]}`, true)
	validate(t, literal, `{Name:"one",CidrIp:"0.0.0.0/0",FromPort:"22"}`, false)
}

func TestScalarCompatibilityAvoidsShadowedHelpers(t *testing.T) {
	f, err := parser.ParseFile("scalar.cue", `package schema
 #Properties: {
  cfnScalarStrings?: __string
  cfnScalarStrconv?: __string
  self?: __string
  len?: __string
  string?: __string
  Count?: int & >=1 & <=4
 }`)
	if err != nil {
		t.Fatal(err)
	}
	addScalarCompatibility(f)
	if err := astutil.Sanitize(f); err != nil {
		t.Fatal(err)
	}
	b, err := render(f)
	if err != nil {
		t.Fatal(err)
	}
	v := cuecontext.New().CompileBytes(b)
	if err := v.Err(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	schema := v.LookupPath(cue.MakePath(cue.Def("Properties")))
	validate(t, schema, `{Count:"2",self:"original",len:"length",string:"type",cfnScalarStrings:"strings",cfnScalarStrconv:"strconv"}`, true)
	validate(t, schema, `{Count:"5"}`, false)
}

func TestTemplateParameterQuotedScalars(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	packageValue := build(t, out, "./template")
	parameter := packageValue.LookupPath(cue.MakePath(cue.Def("Parameter")))
	cases := []struct {
		data  string
		valid bool
	}{
		{`{Type:"String",MinLength:"18",MaxLength:"64",NoEcho:"True"}`, true},
		{`{Type:"String",MinLength:"0",MaxLength:"0"}`, true},
		{`{Type:"String",MinLength:18,MaxLength:64,NoEcho:true}`, true},
		{`{Type:"Number",MinValue:"-3.5",MaxValue:"2.5e3",Default:"2",AllowedValues:["2",2],NoEcho:"FALSE"}`, true},
		{`{Type:"Number",MinValue:-3.5,MaxValue:2500,Default:2,AllowedValues:[2,3]}`, true},
		{`{Type:"String",Default:"original",AllowedValues:["original",18]}`, true},
		{`{Type:"String",MinLength:"-1"}`, false},
		{`{Type:"String",MaxLength:"-1"}`, false},
		{`{Type:"String",MinLength:"18.5"}`, false},
		{`{Type:"String",MaxLength:"6.4e1"}`, false},
		{`{Type:"String",MinLength:"eighteen"}`, false},
		{`{Type:"String",MaxLength:true}`, false},
		{`{Type:"Number",MinValue:"small"}`, false},
		{`{Type:"Number",MaxValue:"large"}`, false},
		{`{Type:"Number",MinValue:true}`, false},
		{`{Type:"Number",MaxValue:"NaN"}`, false},
		{`{Type:"String",NoEcho:"tRuE"}`, false},
		{`{Type:"String",NoEcho:"yes"}`, false},
		{`{Type:"String",NoEcho:"1"}`, false},
		{`{Type:"String",Description:18}`, false},
		{`{Type:"String",AllowedValues:[true,false],Default:true}`, true},
		{`{Type:"String",AllowedValues:[[true]]}`, false},
	}
	for _, tc := range cases {
		validate(t, parameter, tc.data, tc.valid)
	}
	for _, echo := range []string{"true", "True", "TRUE", "false", "False", "FALSE"} {
		validate(t, parameter, `{Type:"String",NoEcho:"`+echo+`"}`, true)
	}
	schema := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, schema, `{Parameters:{Text:{Type:"String",MinLength:"18",MaxLength:"64",NoEcho:"TRUE"},Amount:{Type:"Number",MinValue:"-3.5",MaxValue:"2.5e3"}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, true)
	validate(t, schema, `{Parameters:{Text:{Type:"String",MinLength:"-1"}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, false)
}

func TestTemplateNames(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	resources := `Resources:{Thing:{Type:"AWS::Test::Thing",Condition:"2Create",Properties:{Name:"one"}}}`
	validate(t, schema, `{Conditions:{"2Create":true},`+resources+`}`, true)
	validate(t, schema, `{Conditions:{"NAT&3AZ_Condition":true},`+resources+`}`, true)
	validate(t, schema, `{Conditions:{"2-Create":true},`+resources+`}`, false)
	validate(t, schema, `{Parameters:{"2Name":{Type:"String"}},`+resources+`}`, true)
	validate(t, schema, `{Parameters:{"Name&":{Type:"String"}},`+resources+`}`, false)
	validate(t, schema, `{Resources:{"5xxAlarm":{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, true)
	validate(t, schema, `{Resources:{"Thing_1":{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, false)
}

func TestConditionalTemplateValues(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	thing := func(attributes string) string {
		return `{Resources:{Thing:{Type:"AWS::Test::Thing",` + attributes + `}}}`
	}
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{Name:"one",Count:4},{Ref:"AWS::NoValue"}]}`), true)
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{Name:"one"},{Name:"two"}]}`), true)
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{Name:"one",Count:5},{Name:"two"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{Name:"one"},{Name:"two"}],Count:1}`), false)
	validate(t, schema, thing(`Properties:{}`), false)
	// Nested Fn::If keeps the exact properties schema at every literal leaf.
	validate(t, schema, thing(`Properties:{"Fn::If":["Big",{"Fn::If":["Small",{Name:"one"},{Name:"two",Count:4}]},{Ref:"AWS::NoValue"}]}`), true)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{Ref:"AWS::NoValue"},{"Fn::If":["B",{"Fn::If":["C",{Name:"one"},{Ref:"AWS::NoValue"}]},{Name:"two"}]}]}`), true)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{"Fn::If":["C",{Name:"one",Count:5},{Name:"two"}]},{Name:"two"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{Name:"three"},{Name:"two"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{Name:"one",Bogus:1},{Name:"two"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{Name:"one"},{Name:"two"}],Name:"one"},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":[{Ref:"B"},{Name:"one"},{Name:"two"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::Sub":"x"},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{Ref:"P"},{Name:"one"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{"Fn::If":["A",{"Fn::If":["B",{},{Name:"one"}]},{Name:"one"}]}`), false)
	validate(t, schema, thing(`Properties:{Ref:"AWS::NoValue"}`), false)
	validate(t, schema, thing(`Properties:{Name:"one"},UpdatePolicy:{"Fn::If":["Big",{AutoScalingReplacingUpdate:{WillReplace:true}},{Ref:"AWS::NoValue"}]}`), true)
	validate(t, schema, thing(`Properties:{Name:"one"},CreationPolicy:{"Fn::If":["Big",{ResourceSignal:{Count:1}},{Ref:"AWS::NoValue"}]}`), true)
	validate(t, schema, thing(`Properties:{Name:"one"},UpdatePolicy:{"Fn::If":["Big",{"Fn::If":["Small",{},{Ref:"AWS::NoValue"}]},{}]}`), true)
	validate(t, schema, thing(`Properties:{Name:"one"},UpdatePolicy:{Ref:"Policy"}`), false)
	validate(t, schema, thing(`Properties:{Name:"one"},UpdatePolicy:{"Fn::If":["Big",{Ref:"Policy"},{}]}`), false)
	validate(t, schema, thing(`Properties:{Name:"one"},CreationPolicy:{"Fn::FindInMap":["A","B","C"]}`), false)
	mapping := `Mappings:{Engine:{mysql:{PerformanceInsights:true,Ports:[3306,true,"x"]}}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}`
	validate(t, schema, `{`+mapping+`}`, true)
	validate(t, schema, `{Mappings:{Engine:{mysql:{Nested:{A:"b"}}}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, false)
}
