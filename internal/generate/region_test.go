package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
)

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
