package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
)

func TestRegionalAliasCollisionPreservesOutput(t *testing.T) {
	source := fixture(t)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	previous := map[string][]byte{}
	for _, path := range []string{"manifest.json", "regions/us-east-1/region.cue"} {
		data, err := os.ReadFile(filepath.Join(out, path))
		if err != nil {
			t.Fatal(err)
		}
		previous[path] = data
	}
	source.files["providers/us-east-1.json"] = []byte(`{"AWS::Test_A::Thing":"first","AWS::Test::A_Thing":"second"}`)
	source.files["resources/first.json"] = []byte(strings.Replace(string(source.files["resources/test.json"]), "AWS::Test::Thing", "AWS::Test_A::Thing", 1))
	source.files["resources/second.json"] = []byte(strings.Replace(string(source.files["resources/test.json"]), "AWS::Test::Thing", "AWS::Test::A_Thing", 1))
	_, err := Generate(source, Options{Out: out})
	if err == nil {
		t.Fatal("generated colliding import aliases")
	}
	for _, detail := range []string{"us-east-1", "AWS::Test_A::Thing", "AWS::Test::A_Thing", "AWS_Test_A_Thing", "same import alias"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("collision error omits %q: %v", detail, err)
		}
	}
	for path, want := range previous {
		got, err := os.ReadFile(filepath.Join(out, path))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("failed generation changed %s: %v", path, err)
		}
	}
}

func TestRegionalTypeNames(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	full := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("Template")))
	names := build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template")))
	good := `{Resources: Thing: {Type: "AWS::Test::Thing", Properties: Name: {"Fn::Sub": "${Name}"}}, Outputs: Name: Value: {Ref: "Name"}}`
	validate(t, full, good, true)
	validate(t, names, good, true)
	badProperty := `{Resources: Thing: {Type: "AWS::Test::Thing", Properties: {Name: "bad", Unknown: 12}}}`
	validate(t, full, badProperty, false)
	validate(t, names, badProperty, true)
	// Property presence, shape, and intrinsic arguments are deliberately unchecked.
	validate(t, names, `{Resources: Thing: {Type: "AWS::Test::Thing"}}`, true)
	validate(t, names, `{Resources: Thing: {Type: "AWS::Test::Thing", Properties: 12}}`, true)
	for _, data := range []string{
		`{Resources: {}}`,
		`{Resources: Thing: {Type: "AWS::Other::Thing"}}`,
		`{Resources: Thing: {Type: "Custom::Thing"}}`,
		`{Resources: "bad-id": {Type: "AWS::Test::Thing"}}`,
		`{Resources: Thing: {Type: "AWS::Test::Thing", Bogus: true}}`,
		`{Resources: Thing: {Type: "AWS::Test::Thing"}, Outputs: Name: Value: {"Fn::Join": ["-"]}}`,
	} {
		validate(t, names, data, false)
	}
	// Prove isolation: the lightweight package still loads after every heavy
	// resource package has been removed.
	if err := os.RemoveAll(filepath.Join(out, "resources")); err != nil {
		t.Fatal(err)
	}
	validate(t, build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template"))), good, true)
}

func TestRegionalTypeNamesExtensions(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out, LanguageExtensions: true}); err != nil {
		t.Fatal(err)
	}
	schema := build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, schema, `{Resources: Thing: {Type: "AWS::Test::Thing"}}`, false)
	validate(t, schema, `{Transform: "AWS::LanguageExtensions", Resources: {"Fn::ForEach::Things": ["X", ["a", "b"], {"Thing${X}": {Type: "AWS::Test::Thing"}}]}}`, true)
	validate(t, schema, `{Transform: "AWS::LanguageExtensions", Resources: {"Fn::ForEach::Things": ["X", ["a"], {"Thing${X}": {Type: "AWS::Other::Thing"}}]}}`, false)
}

func TestRegionalTypeNamesCustomResources(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		out := filepath.Join(t.TempDir(), "generated")
		if _, err := Generate(fixture(t), Options{Out: out}); err != nil {
			t.Fatal(err)
		}
		mapping := map[string]string{"AWS::Test::Thing": "test"}
		if enabled {
			mapping["AWS::CloudFormation::CustomResource"] = "custom"
		}
		bytes, err := render(RegionTypes("github.com/cueformation/cfn-cue", mapping, false))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "regions/us-east-1/types/region.cue"), bytes, 0644); err != nil {
			t.Fatal(err)
		}
		schema := build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template")))
		validate(t, schema, `{Resources: Thing: {Type: "Custom::Thing"}}`, enabled)
		validate(t, schema, `{Resources: Thing: {Type: "Custom::"}}`, false)
		validate(t, schema, `{Resources: Thing: {Type: "Custom::bad!"}}`, false)
	}
}

func TestRegionalTypeNamesAvailability(t *testing.T) {
	source := fixture(t)
	source.files["providers/eusc-de-east-1.json"] = []byte(`{"AWS::Other::Thing":"other"}`)
	source.files["resources/other.json"] = []byte(strings.Replace(string(source.files["resources/test.json"]), "AWS::Test::Thing", "AWS::Other::Thing", 1))
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"us-east-1", "eusc-de-east-1"} {
		schema := build(t, out, "./regions/"+region+"/types").LookupPath(cue.MakePath(cue.Def("Template")))
		validate(t, schema, `{Resources: Thing: {Type: "AWS::Test::Thing"}}`, region == "us-east-1")
		validate(t, schema, `{Resources: Thing: {Type: "AWS::Other::Thing"}}`, region == "eusc-de-east-1")
	}
}

func TestRegionalTypeNamesSAM(t *testing.T) {
	source := fixture(t)
	source.files["providers/sam.json"] = []byte(`{"AWS::Serverless::Function":"sam"}`)
	source.files["resources/sam.json"] = []byte(strings.Replace(string(source.files["resources/test.json"]), "AWS::Test::Thing", "AWS::Serverless::Function", 1))
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, IncludeSAM: true})
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.IncludeSAM {
		t.Fatal("manifest lost the SAM generator option")
	}
	schema := build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, schema, `{Transform: "AWS::Serverless-2016-10-31", Resources: Function: {Type: "AWS::Serverless::Function"}}`, true)
	// The same filter applies to both representations, even with SAM enabled.
	if _, err := Generate(source, Options{Out: out, IncludeSAM: true, Resources: []string{"AWS::Test::Thing"}}); err != nil {
		t.Fatal(err)
	}
	schema = build(t, out, "./regions/us-east-1/types").LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, schema, `{Resources: Function: {Type: "AWS::Serverless::Function"}}`, false)
}

// TestRegionalTypeSelection checks that the full region selects each
// resource's schema by Type, including Custom:: names and ForEach fragments.
func TestRegionalTypeSelection(t *testing.T) {
	for _, custom := range []bool{false, true} {
		s := fixture(t)
		providers := `{"AWS::Test::Thing":"test"}`
		if custom {
			providers = `{"AWS::Test::Thing":"test","AWS::CloudFormation::CustomResource":"custom"}`
			s.files["resources/custom.json"] = []byte(`{"typeName":"AWS::CloudFormation::CustomResource","type":"object",
				"properties":{"ServiceToken":{"type":"string"}},"patternProperties":{".*":{}},"required":["ServiceToken"]}`)
		}
		s.files["providers/us-east-1.json"] = []byte(providers)
		delete(s.files, "providers/eusc-de-east-1.json")
		for _, extensions := range []bool{false, true} {
			out := filepath.Join(t.TempDir(), "generated")
			if _, err := Generate(s, Options{Out: out, LanguageExtensions: extensions}); err != nil {
				t.Fatal(err)
			}
			region := build(t, out, "./regions/us-east-1")
			// A union of every resource schema costs about 0.5 s per resource
			// in a complete region; the full package must not define one.
			if region.LookupPath(cue.MakePath(cue.Def("Resource"))).Exists() {
				t.Fatal("full region package defines #Resource")
			}
			schema := region.LookupPath(cue.MakePath(cue.Def("Template")))
			prefix := ""
			if extensions {
				prefix = `Transform: "AWS::LanguageExtensions", `
			}
			check := func(resources string, good bool) {
				t.Helper()
				validate(t, schema, "{"+prefix+"Resources: "+resources+"}", good)
			}
			check(`{Thing: {Type: "AWS::Test::Thing", Properties: Name: "one"}}`, true)
			check(`{Thing: {Type: "AWS::Test::Thing", Properties: Name: "bad"}}`, false)
			check(`{Thing: {Type: "AWS::Test::Thing", Properties: Name: "one", Bogus: 1}}`, false)
			check(`{Thing: {Properties: Name: "one"}}`, false)
			check(`{Thing: {Type: 5}}`, false)
			check(`{Thing: {Type: {Ref: "T"}}}`, false)
			check(`{Thing: {Type: "AWS::Other::Thing"}}`, false)
			check(`{Thing: {Type: "Custom::Thing", Properties: ServiceToken: "arn:aws:lambda:us-east-1:123456789012:function:f"}}`, custom)
			check(`{Thing: {Type: "AWS::CloudFormation::CustomResource", Properties: ServiceToken: "token"}}`, custom)
			check(`{Thing: {Type: "Custom::bad!", Properties: ServiceToken: "token"}}`, false)
			check(`{Thing: {Type: "Custom::Thing", Properties: {}}}`, false)
			if extensions {
				check(`{"Fn::ForEach::Things": ["X", ["a", "b"], {"Thing${X}": {Type: "AWS::Test::Thing", Properties: Name: "one"}}]}`, true)
				check(`{"Fn::ForEach::Things": ["X", ["a"], {"Thing${X}": {Type: "AWS::Other::Thing"}}]}`, false)
				check(`{"Fn::ForEach::Things": ["X", ["a"], {"Thing${X}": {Type: "AWS::Test::Thing", Properties: Name: "bad"}}]}`, false)
			}
			// One error naming the type, not one Type conflict per resource type.
			e := schema.Unify(schema.Context().CompileString(`{` + prefix + `Resources: Thing: {Type: "AWS::Other::Thing"}}`)).Validate(cue.Concrete(true))
			if list := errors.Errors(e); len(list) != 1 || !strings.Contains(list[0].Error(), `undefined field: "AWS::Other::Thing"`) {
				t.Fatalf("unknown type error: %v", e)
			}
		}
	}
}
