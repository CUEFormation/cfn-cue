package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cueformation/cfn-cue/internal/generate"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{
		"providers/us-east-1.json": `{"AWS::Test::Thing":"thing","AWS::Test::Nested":"nested","AWS::CloudFormation::CustomResource":"custom"}`,
		"resources/nested.json":    `{"typeName":"AWS::Test::Nested","type":"object","additionalProperties":false,"definitions":{"Setting":{"type":"object","additionalProperties":false,"properties":{"Key":{"type":"string"},"Value":{"type":"string"}},"required":["Key"]}},"properties":{"Settings":{"type":"array","minItems":1,"items":{"$ref":"#/definitions/Setting"}},"Primary":{"type":"string"},"Secondary":{"type":"string"}},"anyOf":[{"required":["Primary"]},{"required":["Secondary"]}]}`,
		"resources/custom.json":    `{"typeName":"AWS::CloudFormation::CustomResource","type":"object","properties":{"ServiceToken":{"type":"string"},"ServiceTimeout":{"type":"integer","minimum":1,"maximum":3600}},"required":["ServiceToken"],"patternProperties":{".*":{"type":["string","integer","number","array","object"]}},"additionalProperties":false}`,
		"resources/thing.json":     `{"typeName":"AWS::Test::Thing","type":"object","additionalProperties":false,"properties":{"Name":{"type":"string","enum":["one","two"]},"Count":{"type":"integer","minimum":1},"Names":{"type":"array","items":{"type":"string"}},"Status":{"type":"string"}},"required":["Name"],"readOnlyProperties":["/properties/Status"],"primaryIdentifier":["/properties/Name"]}`,
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
		{"numeric-string", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Count":"2"}}}}`, "valid"},
		{"numeric-string-range", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Count":"0"}}}}`, "invalid"},
		{"numeric-string-malformed", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one","Count":"two"}}}}`, "invalid"},
		{"wrong-result", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Split":[",","x"]}}}}}`, "invalid"},
		{"arity", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Sub":["x"]}}}}}`, "invalid"},
		{"two-functions", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::Sub":"x","Ref":"P"}}}}}`, "invalid"},
		{"if-branch", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["Yes",[],"one"]}}}}}`, "invalid"},
		{"empty", `{"Resources":{}}`, "invalid"},
		{"logical-id", `{"Resources":{"Bad-Name":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "invalid"},
		{"logical-id-digit", `{"Resources":{"5xxAlarm":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "valid"},
		{"if-properties", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Condition":"Yes","Properties":{"Fn::If":["Yes",{"Name":"one"},{"Ref":"AWS::NoValue"}]}}}}`, "valid"},
		{"if-properties-removed", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Fn::If":["Yes",{"Name":"one"},{"Ref":"AWS::NoValue"}]}}}}`, "invalid"},
		{"if-properties-branch", `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Fn::If":["Yes",{"Name":"bad"},{"Ref":"AWS::NoValue"}]}}}}`, "invalid"},
		{"template-field", `{"Unexpected":true,"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "invalid"},
		{"unknown-type", `{"Resources":{"Thing":{"Type":"AWS::Absent::Thing"}}}`, "unsupported"},
		{"sam", `{"Transform":"AWS::Serverless-2016-10-31","Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "unsupported"},
		{"extensions", `{"Transform":"AWS::LanguageExtensions","Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`, "unsupported"},
		{"type-shape", `{"Resources":{"Thing":{"Type":["AWS::Test::Thing"]}}}`, "invalid"},
		{"transform-null", `{"Transform":null,"Resources":{"Thing":{"Type":"AWS::Test::Thing"}}}`, "invalid"},
		{"transform-list-null", `{"Transform":["AWS::LanguageExtensions",null],"Resources":{"Thing":{"Type":"AWS::Test::Thing"}}}`, "invalid"},
		{"transform-number", `{"Transform":2,"Resources":{"Thing":{"Type":"AWS::Test::Thing"}}}`, "invalid"},
		{"transform-object", `{"Transform":{},"Resources":{"Thing":{"Type":"AWS::Test::Thing"}}}`, "invalid"},
		{"foreach", `{"Resources":{"Fn::ForEach::Things":["X",["one"],{}]}}`, "unsupported"},
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

func TestCustomResourceValidation(t *testing.T) {
	dir := fixture(t)
	for _, tc := range []struct{ data, status string }{
		{`{"Resources":{"Thing":{"Type":"Custom::Thing","Properties":{"ServiceToken":"arn:aws:lambda:us-east-1:123456789012:function:Provider","Extra":{"Ref":"Parameter"}}}}}`, "valid"},
		{`{"Resources":{"Thing":{"Type":"Custom::Thing","Properties":{"ServiceToken":{"Ref":"Provider"},"ServiceTimeout":"60"}}}}`, "valid"},
		{`{"Resources":{"Thing":{"Type":"Custom::Thing"}}}`, "invalid"},
		{`{"Resources":{"Thing":{"Type":"Custom::Thing","Properties":{"ServiceToken":[false]}}}}`, "invalid"},
		{`{"Resources":{"Thing":{"Type":"Custom::Thing","Properties":{"ServiceToken":"provider","ServiceTimeout":"3601"}}}}`, "invalid"},
		{`{"Resources":{"Thing":{"Type":"Custom::bad!","Properties":{"ServiceToken":"provider"}}}}`, "invalid"},
		{`{"Resources":{"Thing":{"Type":"Custom::","Properties":{"ServiceToken":"provider"}}}}`, "invalid"},
	} {
		for _, resource := range []string{"", "Thing"} {
			result := Check([]byte(tc.data), Options{Schemas: dir, Region: "us-east-1", Resource: resource})
			if result.Status != tc.status {
				t.Fatalf("resource=%q: %s want %s: %s", resource, result.Status, tc.status, result.Message)
			}
		}
	}
	data := []byte(`{"Resources":{"A":{"Type":"Custom::Alpha","Properties":{"ServiceToken":"provider"}},"B":{"Type":"Custom::Beta","Properties":{"ServiceToken":"provider"}},"C":{"Type":"AWS::CloudFormation::CustomResource","Properties":{"ServiceToken":"provider"}}}}`)
	result := Check(data, Options{Schemas: dir, Region: "us-east-1"})
	if result.Status != "valid" || !reflect.DeepEqual(result.ResourceTypes, []string{"AWS::CloudFormation::CustomResource", "Custom::Alpha", "Custom::Beta"}) {
		t.Fatal(result)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	bytes, _ := os.ReadFile(manifestPath)
	var manifest generate.Manifest
	if err := json.Unmarshal(bytes, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest.Regions["us-east-1"], "AWS::CloudFormation::CustomResource")
	bytes, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	if result := Check(data, Options{Schemas: dir, Region: "us-east-1"}); result.Status != "unsupported" {
		t.Fatal(result)
	}
}

func TestManyConcreteResources(t *testing.T) {
	dir := fixture(t)
	resources := map[string]any{}
	for i := 0; i < 50; i++ {
		name := "Thing" + strings.Repeat("A", i)
		resources[name] = map[string]any{"Type": "AWS::Test::Thing", "Properties": map[string]any{
			"Name": map[string]any{"Fn::If": []any{"Create", "one", "two"}}, "Count": map[string]any{"Fn::If": []any{"Create", "2", map[string]any{"Ref": "AWS::NoValue"}}}}}
	}
	data, _ := json.Marshal(map[string]any{"Resources": resources})
	if result := Check(data, Options{Schemas: dir, Region: "us-east-1"}); result.Status != "valid" {
		t.Fatal(result)
	}
	resources["Invalid"] = map[string]any{"Type": "AWS::Test::Thing", "Properties": map[string]any{"Count": 2}}
	data, _ = json.Marshal(map[string]any{"Resources": resources})
	if result := Check(data, Options{Schemas: dir, Region: "us-east-1"}); result.Status != "invalid" {
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

func TestResourceAliasCollision(t *testing.T) {
	dir := fixture(t)
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest generate.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Regions["us-east-1"] = map[string]string{"AWS::Test_A::Thing": "first", "AWS::Test::A_Thing": "second"}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	result := Check([]byte(`{"Resources":{"First":{"Type":"AWS::Test_A::Thing"},"Second":{"Type":"AWS::Test::A_Thing"}}}`), Options{Schemas: dir, Region: "us-east-1"})
	if result.Status != "error" || result.Phase != "input" {
		t.Fatalf("want collision detected before loading schemas: %+v", result)
	}
	for _, detail := range []string{"AWS::Test_A::Thing", "AWS::Test::A_Thing", "AWS_Test_A_Thing", "same import alias"} {
		if !strings.Contains(result.Message, detail) {
			t.Fatalf("collision error omits %q: %s", detail, result.Message)
		}
	}
}

// TestMixedFailures covers templates whose resources fail in different ways.
// Go randomizes map iteration per range statement and per process, so each case
// runs repeatedly in this process and in fresh processes.
func TestMixedFailures(t *testing.T) {
	if os.Getenv("CFN_CUE_VALIDATE_CHILD") != "" {
		result := Check([]byte(os.Getenv("CFN_CUE_VALIDATE_DATA")), Options{Schemas: os.Getenv("CFN_CUE_VALIDATE_CHILD"), Region: "us-east-1"})
		os.Stdout.WriteString("\n" + result.Status + "\n" + result.Message)
		os.Exit(0)
	}
	dir := fixture(t)
	absent := "is absent from us-east-1"
	for _, tc := range []struct {
		name, data, status string
		message            []string
	}{
		// SAM's tests/translator/output/unsupported_resources.json.
		{"missing-type-and-unknown", `{"Resources":{"Missing":{"Properties":{}},"Unknown":{"Type":"AWS::Other::ABCD","Properties":{}}}}`, "invalid",
			[]string{"resource Missing must have a string Type", "not checked: resource Unknown: AWS::Other::ABCD " + absent}},
		{"schema-invalid-and-unknown", `{"Resources":{"Bad":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad"}},"Good":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"Unknown":{"Type":"AWS::Other::ABCD"}}}`, "invalid",
			[]string{"Resources.Bad.Properties.Name", "not checked: resource Unknown: AWS::Other::ABCD " + absent}},
		{"invalid-envelope-on-unknown", `{"Resources":{"Good":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"Unknown":{"Type":"AWS::Other::ABCD","DeletionPolicy":"Never"}}}`, "invalid",
			[]string{"Resources.Unknown.DeletionPolicy", "not checked: resource Unknown"}},
		{"valid-and-unknown", `{"Resources":{"Good":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"Zed":{"Type":"AWS::Other::Zed"},"Alpha":{"Type":"AWS::Other::Alpha"}}}`, "unsupported",
			[]string{"resource Alpha: AWS::Other::Alpha " + absent + "\nresource Zed: AWS::Other::Zed " + absent}},
		{"invalid-ids-and-types", `{"Resources":{"B-1":{"Type":"AWS::Test::Thing"},"A-1":{"Type":"AWS::Test::Thing"},"C":{"Type":"Custom::bad!"},"D":{"Type":"AWS::Other::D"}}}`, "invalid",
			[]string{"invalid resource logical ID: A-1\ninvalid resource logical ID: B-1\nresource C has an invalid custom resource Type\nnot checked: resource D"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := Check([]byte(tc.data), Options{Schemas: dir, Region: "us-east-1"})
			if first.Status != tc.status {
				t.Fatalf("%s want %s: %s", first.Status, tc.status, first.Message)
			}
			for _, part := range tc.message {
				if !strings.Contains(first.Message, part) {
					t.Fatalf("message omits %q:\n%s", part, first.Message)
				}
			}
			for i := 0; i < 20; i++ {
				if result := Check([]byte(tc.data), Options{Schemas: dir, Region: "us-east-1"}); result.Status != first.Status || result.Message != first.Message {
					t.Fatalf("run %d changed: %s: %s", i, result.Status, result.Message)
				}
			}
			want := "\n" + first.Status + "\n" + first.Message
			for i := 0; i < 3; i++ {
				cmd := exec.Command(os.Args[0], "-test.run=^TestMixedFailures$")
				cmd.Env = append(os.Environ(), "CFN_CUE_VALIDATE_CHILD="+dir, "CFN_CUE_VALIDATE_DATA="+tc.data)
				out, err := cmd.Output()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(string(out), want) {
					t.Fatalf("process %d changed:\n%s", i, out)
				}
			}
		})
	}
}

func TestSemanticChecks(t *testing.T) {
	dir := fixture(t)
	const (
		undefinedRef = `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Ref":"Missing"}}}}}`
		badEnum      = `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad"}}}}`
		bothBad      = `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad","Names":[{"Ref":"Missing"}]}}}}`
		attribute    = `{"Resources":{"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"Other":{"Type":"AWS::Test::Thing","Properties":{"Name":"two","Names":[{"Fn::GetAtt":["Thing","%s"]}]}}}}`
		duplicate    = `{"Resources":{"A":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}},"B":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`
		largeNumbers = `{"Resources":{"A":{"Type":"AWS::Test::Thing","Properties":{"Name":9007199254740992}},"B":{"Type":"AWS::Test::Thing","Properties":{"Name":9007199254740993}}}}`
		conditional  = `{"Parameters":{"Env":{"Type":"String"}},"Conditions":{"Prod":{"Fn::Equals":[{"Ref":"Env"},"prod"]}},"Resources":{"A":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["Prod",{"Ref":"B"},"one"]}}},"B":{"Type":"AWS::Test::Thing","DependsOn":"A","Properties":{"Name":"two"}}}}`
	)
	for _, tc := range []struct{ data, checks, status, phase string }{
		// Structural checks alone keep their existing results.
		{undefinedRef, "", "valid", "evaluate"},
		{undefinedRef, ChecksStructural, "valid", "evaluate"},
		{undefinedRef, ChecksSemantic, "invalid", "semantic"},
		{undefinedRef, ChecksAll, "invalid", "semantic"},
		// Semantic checks alone do not evaluate CUE.
		{badEnum, ChecksSemantic, "valid", "semantic"},
		{badEnum, ChecksAll, "invalid", "evaluate"},
		{bothBad, ChecksAll, "invalid", "evaluate"},
		{fmt.Sprintf(attribute, "Status"), ChecksAll, "valid", "evaluate"},
		{fmt.Sprintf(attribute, "Missing"), ChecksAll, "invalid", "semantic"},
		{conditional, ChecksAll, "valid", "evaluate"},
		// The manifest's primary identifier reaches the semantic pass.
		{duplicate, ChecksStructural, "valid", "evaluate"},
		{duplicate, ChecksAll, "invalid", "semantic"},
		// Numbers keep every digit, so these names differ.
		{largeNumbers, ChecksSemantic, "valid", "semantic"},
		{undefinedRef, "everything", "error", "input"},
	} {
		result := Check([]byte(tc.data), Options{Schemas: dir, Region: "us-east-1", Checks: tc.checks})
		if result.Status != tc.status || result.Phase != tc.phase {
			t.Fatalf("%s %s: got %s/%s: %s", tc.checks, tc.data, result.Status, result.Phase, result.Message)
		}
	}
	result := Check([]byte(bothBad), Options{Schemas: dir, Region: "us-east-1", Checks: ChecksAll})
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Rule != "ref-undefined" || !strings.Contains(result.Message, "Name") {
		t.Fatalf("structural failure should keep semantic diagnostics: %+v", result)
	}
	result = Check([]byte(conditional), Options{Schemas: dir, Region: "us-east-1", Checks: ChecksAll})
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Severity != "warning" {
		t.Fatalf("conditional cycle should be a warning: %+v", result)
	}
	if result := Check([]byte(undefinedRef), Options{Schemas: dir, Region: "us-east-1", Resource: "Thing", Checks: ChecksAll}); result.Status != "error" {
		t.Fatalf("semantic checks need the whole template: %+v", result)
	}
	// A manifest from an earlier generator has no attribute names; they are
	// unknown, so GetAtt names are not checked.
	path := filepath.Join(dir, "manifest.json")
	data, _ := os.ReadFile(path)
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, info := range manifest["schemas"].(map[string]any) {
		delete(info.(map[string]any), "attributes")
	}
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if result := Check([]byte(fmt.Sprintf(attribute, "Missing")), Options{Schemas: dir, Region: "us-east-1", Checks: ChecksSemantic}); result.Status != "valid" {
		t.Fatalf("unknown attribute names must not be rejected: %+v", result)
	}
}

// TestSemanticWithMixedFailures checks that semantic results follow the same
// precedence as other results: error, then invalid, then unsupported.
func TestSemanticWithMixedFailures(t *testing.T) {
	dir := fixture(t)
	absent := `"Absent":{"Type":"AWS::Absent::Thing"}`
	for _, tc := range []struct {
		name, data, checks, status, phase string
		message                           []string
	}{
		{"semantic-over-unsupported", `{"Resources":{` + absent + `,"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Ref":"Missing"}}}}}`,
			ChecksAll, "invalid", "semantic", []string{"Ref target Missing", "not checked: resource Absent"}},
		{"semantic-only-over-unsupported", `{"Resources":{` + absent + `,"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Ref":"Missing"}}}}}`,
			ChecksSemantic, "invalid", "semantic", []string{"Ref target Missing", "not checked: resource Absent"}},
		{"all-unsupported-semantic", `{"Resources":{` + absent + `,"Other":{"Type":"AWS::Absent::Thing","DependsOn":"Missing"}}}`,
			ChecksAll, "invalid", "semantic", []string{"DependsOn target Missing", "not checked: resource Absent", "not checked: resource Other"}},
		{"unsupported-without-semantic-errors", `{"Resources":{` + absent + `,"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"one"}}}}`,
			ChecksAll, "unsupported", "evaluate", []string{"resource Absent"}},
		{"schema-and-semantic", `{"Resources":{` + absent + `,"Thing":{"Type":"AWS::Test::Thing","Properties":{"Name":"bad","Names":[{"Ref":"Missing"}]}}}}`,
			ChecksAll, "invalid", "evaluate", []string{"Name", "Ref target Missing", "not checked: resource Absent"}},
		{"removal-and-semantic", `{"Parameters":{"Env":{"Type":"String"}},"Conditions":{"IsProd":{"Fn::Equals":[{"Ref":"Env"},"prod"]}},"Resources":{` + absent + `,"Thing":{"Type":"AWS::Test::Thing","DependsOn":"Missing","Properties":{"Name":{"Fn::If":["IsProd","one",{"Ref":"AWS::NoValue"}]}}}}}`,
			ChecksAll, "invalid", "evaluate", []string{"removes Properties.Name when condition IsProd is false", "DependsOn target Missing", "not checked: resource Absent"}},
		{"input-and-semantic", `{"Resources":{"Bad-Name":{"Type":"AWS::Test::Thing"},"Thing":{"Type":"AWS::Test::Thing","DependsOn":"Missing","Properties":{"Name":"one"}}}}`,
			ChecksAll, "invalid", "input", []string{"invalid resource logical ID: Bad-Name", "DependsOn target Missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first string
			for i := 0; i < 5; i++ {
				result := Check([]byte(tc.data), Options{Schemas: dir, Region: "us-east-1", Checks: tc.checks})
				if result.Status != tc.status || result.Phase != tc.phase {
					t.Fatalf("got %s/%s, want %s/%s: %s", result.Status, result.Phase, tc.status, tc.phase, result.Message)
				}
				for _, part := range tc.message {
					if !strings.Contains(result.Message, part) {
						t.Fatalf("message omits %q: %s", part, result.Message)
					}
				}
				if i == 0 {
					first = result.Message
				} else if result.Message != first {
					t.Fatalf("message changed between runs:\n%s\n---\n%s", first, result.Message)
				}
			}
		})
	}
}
