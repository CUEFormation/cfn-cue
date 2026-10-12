package generate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
)

func fixture(t *testing.T) *Source {
	t.Helper()
	root := map[string]any{
		"typeName": "AWS::Test::Thing", "type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"Name":    map[string]any{"type": "string", "enum": []any{"one", "two"}},
			"Count":   map[string]any{"type": "integer", "minimum": 1, "maximum": 4},
			"Names":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"Node":    map[string]any{"$ref": "#/definitions/Node"},
			"Status":  map[string]any{"type": "string"},
			"Policy":  map[string]any{"type": []any{"object", "string"}, "minLength": 1},
			"Options": map[string]any{"type": "object", "properties": map[string]any{"A": map[string]any{"type": "string"}, "B": map[string]any{"type": "string"}}, "additionalProperties": false, "requiredXor": []any{"A", "B"}},
		},
		"required": []any{"Name"}, "readOnlyProperties": []any{"/properties/Status", "/properties/Node/State"},
		"definitions": map[string]any{"Node": map[string]any{"type": "object", "properties": map[string]any{"Label": map[string]any{"type": "string"}, "State": map[string]any{"type": "string"}, "Children": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/definitions/Node"}}}, "additionalProperties": false}},
	}
	b, e := json.Marshal(root)
	if e != nil {
		t.Fatal(e)
	}
	s := &Source{Label: "fixture", SHA256: "fixture", files: map[string][]byte{
		"providers/us-east-1.json":      []byte(`{"AWS::Test::Thing":"test"}`),
		"providers/eusc-de-east-1.json": []byte(`{"AWS::Test::Thing":"test"}`),
		"resources/test.json":           b,
	}}
	return s
}
func build(t *testing.T, dir, pkg string) cue.Value {
	t.Helper()
	instances := load.Instances([]string{pkg}, &load.Config{Dir: dir})
	if len(instances) != 1 {
		t.Fatalf("instances: %d", len(instances))
	}
	if instances[0].Err != nil {
		t.Fatal(instances[0].Err)
	}
	v := cuecontext.New().BuildInstance(instances[0])
	if e := v.Err(); e != nil {
		t.Fatal(e)
	}
	return v
}
func validate(t *testing.T, schema cue.Value, data string, good bool) {
	t.Helper()
	t.Logf("validate %s", data)
	v := schema.Context().CompileString(data)
	if e := v.Err(); e != nil {
		t.Fatalf("invalid test input: %s\n%v", data, e)
	}
	e := schema.Unify(v).Validate(cue.Concrete(true))
	if (e == nil) != good {
		t.Fatalf("valid=%v want=%v: %s\n%v", e == nil, good, data, e)
	}
}
func TestRegionalGenerationAndValidation(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	m, e := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Regions) != 1 {
		t.Fatal("region selection broadened")
	}
	region := build(t, out, "./regions/us-east-1")
	t.Log("region built")
	schema := region.LookupPath(cue.MakePath(cue.Def("Template")))
	for _, data := range []string{
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Count:2}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Join":["-",[{"Fn::Sub":"${P}"},{Ref:"P"}]]},Count:{Ref:"N"},Names:{"Fn::Split":[",",{Ref:"List"}]}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Node:{Label:{"Fn::Sub":"a"},Children:[{Label:"b",Children:[{Label:"c"}]}]}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Options:{A:"x"}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Policy:{Version:"2012-10-17",Statement:[{Action:"sts:AssumeRole",Condition:{StringEquals:{ExternalId:{Ref:"P"}}}}]}}}}}`,
		`{Conditions:{Yes:{"Fn::And":[{"Fn::Equals":[{Ref:"P"},"a"]},{Condition:"Other"}]}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::If":["Yes",{"Fn::Sub":"x"},{Ref:"AWS::NoValue"}]}}}}}`,
		`{Rules:{Check:{Assertions:[{Assert:{"Fn::EachMemberIn":[{"Fn::ValueOfAll":["AWS::EC2::Subnet::Id","VpcId"]},{"Fn::RefAll":"AWS::EC2::VPC::Id"}]}}]}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Resources:{}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Count:2}}}}`,
		`{Resources:{Thing:{Type:"AWS::Other::Thing",Properties:{Name:"one"}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"bad"}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Count:5}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Status:"bad"}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Node:{State:"readonly"}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Join":["-",["x"]],Ref:"P"}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Split":[",","x"]}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::If":["Yes",[],"x"]}}}}}`,
		`{Conditions:{Yes:{"Fn::And":[true]}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Options:{A:"x",B:"y"}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Policy:[]}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one",Policy:{Statement:[{"Fn::Bogus":"x"}]}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Length":["a"]}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Bogus":"x"}}}}}`,
		`{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::Sub":["name",{P:{"Fn::Join":["-",["a"]],Ref:"P"}}]}}}}}`,
	} {
		validate(t, schema, data, false)
	}
	literal := build(t, out, "./literal/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
	validate(t, literal, `{Name:"one",Status:"available",Node:{State:"readable"}}`, true)
	validate(t, literal, `{Name:{Ref:"P"}}`, false)
}
func TestExtensions(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	_, e := Generate(fixture(t), Options{Out: out, LanguageExtensions: true})
	if e != nil {
		t.Fatal(e)
	}
	s := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, s, `{Transform:"AWS::LanguageExtensions",Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:{"Fn::ToJsonString":{a:{Ref:"P"}}},Count:{"Fn::Length":["a","b"]}}}}}`, true)
	validate(t, s, `{Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`, false)
	validate(t, s, `{Transform:"AWS::LanguageExtensions",Resources:{"Fn::ForEach::Things":["X",["a","b"],{"Thing${X}":{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}]}}`, true)
}
func TestFailureLeavesOutputIntact(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	s := fixture(t)
	_, e := Generate(s, Options{Out: out})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	s.files["resources/test.json"] = []byte(`{"typeName":"AWS::Wrong::Thing"}`)
	if _, e = Generate(s, Options{Out: out}); e == nil {
		t.Fatal("missing type mismatch error")
	}
	after, _ := os.ReadFile(filepath.Join(out, "manifest.json"))
	if string(before) != string(after) {
		t.Fatal("failed generation modified output")
	}
}
func TestDiagnosticsAndStrict(t *testing.T) {
	s := fixture(t)
	var root map[string]any
	_ = json.Unmarshal(s.files["resources/test.json"], &root)
	root["maxUniqueItems"] = 2
	s.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	m, e := Generate(s, Options{Out: out, Strict: true})
	if e == nil || m == nil || len(m.Diagnostics) == 0 {
		t.Fatalf("strict result: %v %v", m, e)
	}
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("strict failure published output")
	}
}
func TestSourceAndInputErrors(t *testing.T) {
	s := fixture(t)
	if _, e := Generate(s, Options{Out: filepath.Join(t.TempDir(), "out"), Regions: []string{"missing"}}); e == nil {
		t.Fatal("missing region accepted")
	}
	s.files["providers/us-east-1.json"] = []byte(`{"AWS::Test::Thing":"../test"}`)
	if _, e := Generate(s, Options{Out: filepath.Join(t.TempDir(), "out")}); e == nil {
		t.Fatal("unsafe hash accepted")
	}
	if _, e := OpenSource(context.Background(), "missing.zip"); e == nil || !strings.Contains(e.Error(), "missing.zip") {
		t.Fatal(e)
	}
}

func TestIntrinsicBuild(t *testing.T) {
	b, e := render(Intrinsics(false))
	if e != nil {
		t.Fatal(e)
	}
	v := cuecontext.New().CompileBytes(b)
	if e = v.Err(); e != nil {
		t.Fatal(e)
	}
}
