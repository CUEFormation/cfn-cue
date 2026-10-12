package validate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cueformation/cfn-cue/internal/generate"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{
		"providers/us-east-1.json": `{"AWS::Test::Thing":"thing"}`,
		"resources/thing.json":     `{"typeName":"AWS::Test::Thing","type":"object","additionalProperties":false,"properties":{"Name":{"type":"string","enum":["one","two"]},"Count":{"type":"integer","minimum":1},"Names":{"type":"array","items":{"type":"string"}},"Status":{"type":"string"}},"required":["Name"],"readOnlyProperties":["/properties/Status"]}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	source, err := generate.OpenSource(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := generate.Generate(source, generate.Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	// This deliberate regional error proves that concrete validation does not
	// accidentally evaluate the aggregate registry.
	if err := os.WriteFile(filepath.Join(out, "regions/us-east-1/region.cue"), []byte("package region\n#Template: _|_\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConcreteValidation(t *testing.T) {
	dir := fixture(t)
	cases := []struct{ name, data, status string }{
		{"literal", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Count":2}}}}`, "valid"},
		{"intrinsic", `{"Parameters":{"P":{"Type":"String"}},"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Join":["-",[{"Fn::Sub":"${P}"},{"Ref":"P"}]]}}}}}`, "valid"},
		{"missing", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Count":2}}}}`, "invalid"},
		{"enum", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad"}}}}`, "invalid"},
		{"unknown-property", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Extra":true}}}}`, "invalid"},
		{"readonly", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Status":"ready"}}}}`, "invalid"},
		{"numeric-string", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Count":"2"}}}}`, "invalid"},
		{"wrong-result", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Split":[",","x"]}}}}}`, "invalid"},
		{"arity", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Sub":["x"]}}}}}`, "invalid"},
		{"two-functions", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Sub":"x","Ref":"P"}}}}}`, "invalid"},
		{"if-branch", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["Yes",[],"one"]}}}}}`, "invalid"},
		{"empty", `{"Resources":{}}`, "invalid"},
		{"logical-id", `{"Resources":{"Bad-Name":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "invalid"},
		{"template-field", `{"Unexpected":true,"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "invalid"},
		{"unknown-type", `{"Resources":{"Thing":{"Type":"Custom::Thing"}}}`, "unsupported"},
		{"sam", `{"Transform":"AWS::Serverless-2016-10-31","Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "unsupported"},
		{"extensions", `{"Transform":"AWS::LanguageExtensions","Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "unsupported"},
		{"type-shape", `{"Resources":{"Thing":{"Type":["AWS::Test::Thing"]}}}`, "invalid"},
		{"null", `null`, "invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := Check([]byte(test.data), Options{Schemas: dir, Region: "us-east-1"})
			if result.Status != test.status {
				t.Fatalf("%s: want %s: %s", result.Status, test.status, result.Message)
			}
		})
	}
	if result := Check([]byte(cases[0].data), Options{Schemas: dir, Region: "missing"}); result.Status != "unsupported" {
		t.Fatal(result)
	}
}

func TestResourceIsolation(t *testing.T) {
	dir := fixture(t)
	data := []byte(`{"Resources":{"Good":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"Bad":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad"}}}}`)
	for _, resource := range []string{"", "Good", "Bad", "Missing"} {
		want := "invalid"
		if resource == "Good" {
			want = "valid"
		}
		result := Check(data, Options{Schemas: dir, Region: "us-east-1", Resource: resource})
		if result.Status != want {
			t.Fatalf("%s: %s: %s", resource, result.Status, result.Message)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "__cfn_cue_validate__.cue")); !os.IsNotExist(err) {
		t.Fatal("validation wrote to the generated bundle")
	}
}
