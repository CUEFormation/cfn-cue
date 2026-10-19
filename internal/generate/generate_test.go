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
		`{Rules:{Check:{Assertions:[{Assert:{"Fn::EachMemberEquals":[{"Fn::ValueOf":["Subnets","VpcId"]},{Ref:"VpcId"}]}}]}},Resources:{Thing:{Type:"AWS::Test::Thing",Properties:{Name:"one"}}}}`,
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
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name:"one",Status:"available",Node:{State:"readable"}}`, true)
	validate(t, literal, `{Name:{Ref:"P"}}`, false)
	// Provider definitions are package-level definitions other packages can
	// use. Read-only removal applies to property paths, so #Node keeps State.
	node := build(t, out, "./resources/htest").LookupPath(cue.MakePath(cue.Def("Node")))
	validate(t, node, `{Label:"a",Children:[{Label:{Ref:"P"}}]}`, true)
	validate(t, node, `{Label:"a",Other:"x"}`, false)
}

// TestDefinitionNames checks that provider definitions named like the
// generated #Properties and #Resource stay nested instead of replacing them.
func TestDefinitionNames(t *testing.T) {
	root := `{"typeName":"AWS::Test::Named","type":"object","additionalProperties":false,
		"properties":{"Tag":{"$ref":"#/definitions/Tag"},"Target":{"$ref":"#/definitions/Resource"},"Settings":{"$ref":"#/definitions/Properties"}},
		"definitions":{
			"Tag":{"type":"object","additionalProperties":false,"properties":{"Key":{"type":"string"}},"required":["Key"]},
			"Resource":{"type":"object","additionalProperties":false,"properties":{"Arn":{"type":"string"}},"required":["Arn"]},
			"Properties":{"type":"object","additionalProperties":false,"properties":{"Mode":{"enum":["a","b"]}}}}}`
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(&Source{Label: "names", SHA256: "names", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::Test::Named":"named"}`),
		"resources/named.json":     []byte(root),
	}}, Options{Out: out, Regions: []string{"us-east-1"}}); e != nil {
		t.Fatal(e)
	}
	pkg := build(t, out, "./resources/hnamed")
	validate(t, pkg.LookupPath(cue.MakePath(cue.Def("Tag"))), `{Key:"k"}`, true)
	resource := pkg.LookupPath(cue.MakePath(cue.Def("Resource")))
	validate(t, resource, `{Type:"AWS::Test::Named",Properties:{Tag:{Key:"k"},Target:{Arn:"a"},Settings:{Mode:"a"}}}`, true)
	validate(t, resource, `{Type:"AWS::Test::Named",Properties:{Target:{}}}`, false)
	validate(t, resource, `{Type:"AWS::Test::Named",Properties:{Settings:{Mode:"c"}}}`, false)
}

// TestPrunedDefinitions checks that resources packages drop provider
// definitions the template input schema no longer reaches, transitively, while
// literal packages keep every definition. Percent-encoded references and,
// in TestRootReferenceKept, references from the root schema keep their targets.
// A field rule inside a pruned definition is dropped; one in a kept
// definition still applies.
func TestPrunedDefinitions(t *testing.T) {
	root := `{"typeName":"AWS::Test::Pruned","type":"object","additionalProperties":false,
		"properties":{"Id":{"$ref":"#/definitions/AccountId"},"Wrapper":{"$ref":"#/definitions/Wrapper"},"Odd":{"$ref":"#/definitions/odd~1name"},"Spaced":{"$ref":"#/definitions%2Fodd%20space"}},
		"readOnlyProperties":["/properties/Id"],
		"definitions":{
			"AccountId":{"allOf":[{"$ref":"#/definitions/Digits"}]},
			"Digits":{"type":"string","pattern":"^[0-9]+$"},
			"Wrapper":{"type":"object","additionalProperties":false,"properties":{"Inner":{"$ref":"#/definitions/Inner"},"Other":{"type":"string"}},"requiredXor":["Inner","Other"]},
			"Inner":{"type":"string"},
			"odd space":{"type":"string"},
			"odd/name":{"type":"string"},
			"odd/orphan":{"type":"string"},
			"Orphan":{"type":"object","properties":{"X":{"type":"string"},"Y":{"type":"string"}},"requiredXor":["X","Y"]}}}`
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(&Source{Label: "pruned", SHA256: "pruned", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::Test::Pruned":"pruned"}`),
		"resources/pruned.json":    []byte(root),
	}}, Options{Out: out, Regions: []string{"us-east-1"}}); e != nil {
		t.Fatal(e)
	}
	pkg := build(t, out, "./resources/hpruned")
	for name, want := range map[string]bool{"AccountId": false, "Digits": false, "Orphan": false, "Wrapper": true, "Inner": true} {
		if got := pkg.LookupPath(cue.MakePath(cue.Def(name))).Exists(); got != want {
			t.Errorf("resources #%s exists=%v want=%v", name, got, want)
		}
	}
	b, e := os.ReadFile(filepath.Join(out, "resources", "hpruned", "schema.cue"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "odd~1name") || strings.Contains(string(b), "odd~1orphan") {
		t.Errorf("nested definitions not pruned as expected:\n%s", b)
	}
	literal := build(t, out, "./literal/hpruned")
	for _, name := range []string{"AccountId", "Digits", "Orphan"} {
		if !literal.LookupPath(cue.MakePath(cue.Def(name))).Exists() {
			t.Errorf("literal #%s missing", name)
		}
	}
	resource := pkg.LookupPath(cue.MakePath(cue.Def("Resource")))
	validate(t, resource, `{Type:"AWS::Test::Pruned",Properties:{Wrapper:{Inner:"x"},Odd:"y",Spaced:"z"}}`, true)
	validate(t, resource, `{Type:"AWS::Test::Pruned",Properties:{Id:"123456789012"}}`, false)
	validate(t, resource, `{Type:"AWS::Test::Pruned",Properties:{Wrapper:{Inner:"x",Other:"y"}}}`, false)
}

// TestReadOnlyRuleDefinitions covers the two pinned schemas whose template
// input orphans a definition carrying a field rule (issue #12). GroundStation's
// EndpointDetails, field rule 2 of 4, is reached only by a read-only property.
// Events::Connection's AuthParameters, field rule 0, is inlined into its
// property when a read-only path below it is removed. Generation must succeed,
// the orphaned rule must be dropped with its definition, and the rules either
// side of it must still apply.
func TestReadOnlyRuleDefinitions(t *testing.T) {
	source, e := OpenSource(context.Background(), "../../snapshots/schemas-2026-10-12.zip")
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(t.TempDir(), "generated")
	if _, e = Generate(source, Options{Out: out, Regions: []string{"us-east-1"}, Resources: []string{"AWS::GroundStation::DataflowEndpointGroupV2", "AWS::Events::Connection"}}); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		pkg, def string
		exists   bool
	}{
		{"./resources/ha3fc2798aeb3e8b6", "EndpointDetails", false},
		{"./resources/ha3fc2798aeb3e8b6", "CreateEndpointDetails", true},
		{"./resources/ha3fc2798aeb3e8b6", "DownlinkDataflowDetails", true},
		{"./resources/ha3fc2798aeb3e8b6", "UplinkDataflowDetails", true},
		{"./literal/ha3fc2798aeb3e8b6", "EndpointDetails", true},
		{"./resources/hd0df85968cafb831", "AuthParameters", false},
		{"./literal/hd0df85968cafb831", "AuthParameters", true},
	} {
		def := build(t, out, c.pkg).LookupPath(cue.MakePath(cue.Def(c.def)))
		if def.Exists() != c.exists {
			t.Errorf("%s #%s exists=%v want=%v", c.pkg, c.def, def.Exists(), c.exists)
			continue
		}
		if c.exists {
			// Every field is optional, so only the at-least-one rule rejects {}.
			validate(t, def, `{}`, false)
		}
	}
	properties := propertiesSchema(t, out, "./resources/hd0df85968cafb831")
	validate(t, properties, `{AuthorizationType:"API_KEY",AuthParameters:{ApiKeyAuthParameters:{ApiKeyName:"k",ApiKeyValue:"v"},ConnectivityParameters:{Ref:"Config"}}}`, true)
	validate(t, properties, `{AuthorizationType:"API_KEY",AuthParameters:{ConnectivityParameters:{Ref:"Config"}}}`, false)
}

// TestRootReferenceKept checks that a definition referenced by the root
// schema's own $ref survives pruning. Read-only removal inlines a root $ref, so
// this fixture has no read-only properties.
func TestRootReferenceKept(t *testing.T) {
	root := `{"typeName":"AWS::Test::RootRef","type":"object","$ref":"#/definitions/Shape",
		"definitions":{"Shape":{"type":"object","properties":{"Name":{"type":"string"}},"required":["Name"]}}}`
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(&Source{Label: "rootref", SHA256: "rootref", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::Test::RootRef":"rootref"}`),
		"resources/rootref.json":   []byte(root),
	}}, Options{Out: out, Regions: []string{"us-east-1"}}); e != nil {
		t.Fatal(e)
	}
	if !build(t, out, "./resources/hrootref").LookupPath(cue.MakePath(cue.Def("Shape"))).Exists() {
		t.Error("resources #Shape pruned")
	}
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

// propertiesSchema returns a generated package's properties schema: #Properties in
// literal packages, or the Properties inlined in #Resource. CUE does not select
// optional or required fields, so the field is made regular first.
func propertiesSchema(t *testing.T, dir, pkg string) cue.Value {
	t.Helper()
	v := build(t, dir, pkg)
	if p := v.LookupPath(cue.MakePath(cue.Def("Properties"))); p.Exists() {
		return p
	}
	resource := v.LookupPath(cue.MakePath(cue.Def("Resource")))
	return resource.Unify(v.Context().CompileString("{Properties: _}")).LookupPath(cue.MakePath(cue.Str("Properties")))
}
