package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
)

// TestTypePackages imports per-type packages from an authored package, as a
// template author would, and validates resources through them.
func TestTypePackages(t *testing.T) {
	s := fixture(t)
	s.files["providers/us-east-1.json"] = []byte(`{"AWS::Test::Thing":"test","AWS::CloudFormation::CustomResource":"custom"}`)
	s.files["resources/custom.json"] = []byte(`{"typeName":"AWS::CloudFormation::CustomResource","type":"object",
		"properties":{"ServiceToken":{"type":"string"}},"patternProperties":{".*":{}},"required":["ServiceToken"]}`)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(s, Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	author := `package example

import (
	thing "github.com/cueformation/cfn-cue/AWS/Test/Thing"
	"github.com/cueformation/cfn-cue/AWS/CloudFormation/CustomResource"
)

thingResource: thing.#Resource
node:          thing.#Node
custom:        CustomResource.#Resource
`
	if err := os.MkdirAll(filepath.Join(out, "example"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "example/example.cue"), []byte(author), 0644); err != nil {
		t.Fatal(err)
	}
	v := build(t, out, "./example")
	resource := v.LookupPath(cue.ParsePath("thingResource"))
	validate(t, resource, `{Type: "AWS::Test::Thing", Properties: {Name: "one", Node: Label: {Ref: "P"}}, DependsOn: "Other"}`, true)
	validate(t, resource, `{Type: "AWS::Test::Thing", Properties: Name: "bad"}`, false)
	validate(t, resource, `{Type: "AWS::Test::Thing", Properties: {Name: "one", Status: "read-only"}}`, false)
	validate(t, resource, `{Type: "AWS::Other::Thing", Properties: Name: "one"}`, false)
	// Provider definitions are re-exported with #Resource.
	validate(t, v.LookupPath(cue.ParsePath("node")), `{Label: "a", Children: [{Label: "b"}]}`, true)
	validate(t, v.LookupPath(cue.ParsePath("node")), `{Label: "a", Other: "x"}`, false)
	// Custom:: names use the CustomResource package, as in #Template.
	custom := v.LookupPath(cue.ParsePath("custom"))
	validate(t, custom, `{Type: "Custom::Thing", Properties: ServiceToken: "arn:aws:lambda:us-east-1:123456789012:function:f"}`, true)
	validate(t, custom, `{Type: "AWS::CloudFormation::CustomResource", Properties: ServiceToken: "token"}`, true)
	validate(t, custom, `{Type: "Custom::bad!", Properties: ServiceToken: "token"}`, false)
	validate(t, custom, `{Type: "Custom::Thing", Properties: {}}`, false)

	// Types available in any region get one package, outside regions/.
	if _, err := os.Stat(filepath.Join(out, "regions/us-east-1/AWS")); !os.IsNotExist(err) {
		t.Fatalf("per-type packages under a region: %v", err)
	}
}

func TestTypePackageSchemas(t *testing.T) {
	s := fixture(t)
	s.files["resources/other.json"] = s.files["resources/test.json"]
	s.files["providers/eusc-de-east-1.json"] = []byte(`{"AWS::Test::Thing":"other"}`)
	_, err := Generate(s, Options{Out: filepath.Join(t.TempDir(), "generated")})
	if err == nil || !strings.Contains(err.Error(), "one schema per type") {
		t.Fatalf("want one schema per type error, got %v", err)
	}
}

func TestTypePathChecks(t *testing.T) {
	if err := checkTypePaths([]string{"AWS::S3::Bucket", "AWS::S3::BucketPolicy", "Alexa::ASK::Skill", "AWS::Test_A::Thing"}); err != nil {
		t.Fatal(err)
	}
	for types, detail := range map[string]string{
		"AWS::SageMaker::Model,AWS::Sagemaker::Other": "differ only in case",
		"AWS::S3::Bucket,AWS::S3::BUCKET":             "differ only in case",
		"Iam::S3::Bucket":                             "differ only in case",
		"AWS::S3::bucket":                             "capitalized CUE identifier",
		"types::S3::Bucket":                           "capitalized CUE identifier",
		"AWS::S3::Bucket-2":                           "capitalized CUE identifier",
		"AWS::S3":                                     "three segments",
		"AWS::S3::Bucket::Extra":                      "three segments",
	} {
		if err := checkTypePaths(strings.Split(types, ",")); err == nil || !strings.Contains(err.Error(), detail) {
			t.Errorf("%s: want %q, got %v", types, detail, err)
		}
	}
}
