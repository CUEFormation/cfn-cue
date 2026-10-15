package generate

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLooseValues(t *testing.T) {
	for _, tt := range []struct {
		in, want []any
	}{
		{[]any{0.0}, []any{0.0, "0"}},
		{[]any{"5.7"}, []any{"5.7", 5.7}},
		{[]any{"10"}, []any{"10", 10.0}},
		{[]any{"05"}, []any{"05"}},
		// Python's str(8.0) is "8.0", but JSON decodes 8.0 to the same
		// value as 8, so the float spelling is not added.
		{[]any{"8.0"}, []any{"8.0"}},
		{[]any{true}, []any{true, "true", "True", "TRUE"}},
		{[]any{"False"}, []any{"False", false, "false", "FALSE"}},
		{[]any{-1.0, "-1"}, []any{-1.0, "-1"}},
	} {
		if got := looseValues(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("looseValues(%v) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestPrepareExtensionRewrites(t *testing.T) {
	decode := func(s string) any {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Markers are checked separately; strip them to compare the shape.
	var strip func(any) any
	strip = func(v any) any {
		switch n := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, x := range n {
				if k != literalMarker {
					out[k] = strip(x)
				}
			}
			return out
		case []any:
			out := make([]any, len(n))
			for i, x := range n {
				out[i] = strip(x)
			}
			return out
		}
		return v
	}
	for _, tt := range []struct {
		in        string
		lowercase bool
		want      string
	}{
		// not over required properties becomes false property schemas.
		{`{"then":{"not":{"anyOf":[{"required":["A"]},{"required":["B"]}],"properties":{"A":true,"B":true}}}}`, false,
			`{"then":{"properties":{"A":false,"B":false}}}`},
		{`{"not":{"required":["A"]}}`, false, `{"properties":{"A":false}}`},
		// "absent or not matching" becomes a not on the property schema.
		{`{"contains":{"not":{"properties":{"E":{"enum":[false]}},"required":["E"]}}}`, false,
			`{"contains":{"properties":{"E":{"not":{"enum":[false,"false","False","FALSE"]}}}}}`},
		// anyOf in a condition is negated twice so matchN checks required.
		{`{"if":{"anyOf":[{"required":["A"]},{"required":["B"]}]}}`, false,
			`{"if":{"allOf":[{"not":{"allOf":[{"not":{"required":["A"]}},{"not":{"required":["B"]}}]}}]}}`},
		{`{"then":{"anyOf":[{"required":["A"]}]}}`, false, `{"then":{"anyOf":[{"required":["A"]}]}}`},
		// RE2 has no lookahead; "does not start with" is a negated prefix.
		{`{"if":{"properties":{"Engine":{"pattern":"^(?!aurora).*$","type":"string"}}}}`, false,
			`{"if":{"properties":{"Engine":{"not":{"pattern":"^aurora","type":"string"},"type":"string"}}}}`},
		{`{"if":{"properties":{"Engine":{"const":"aurora-mysql"}}}}`, true,
			`{"if":{"properties":{"Engine":{"pattern":"(?i)^(?:aurora-mysql)$","type":"string"}}}}`},
		{`{"if":{"properties":{"Engine":{"pattern":"(aurora|aurora-mysql)"}}}}`, true,
			`{"if":{"properties":{"Engine":{"pattern":"(?i)(aurora|aurora-mysql)"}}}}`},
		{`{"additionalProperties":true,"properties":{"A":true}}`, false, `{"properties":{"A":{}}}`},
	} {
		got := prepareExtension(decode(tt.in), false, tt.lowercase, "")
		if !reflect.DeepEqual(strip(got), decode(tt.want)) {
			b, _ := json.Marshal(strip(got))
			t.Errorf("prepareExtension(%s)\n got %s\nwant %s", tt.in, b, tt.want)
		}
	}
	// Conditions are marked so intrinsics cannot satisfy them; then is not.
	got := prepareExtension(decode(`{"if":{"properties":{"A":{"const":"x"}}},"then":{"properties":{"B":{"const":"y"}}}}`), false, false, "").(map[string]any)
	condition := got["if"].(map[string]any)
	if condition[literalMarker] != true || condition["properties"].(map[string]any)["A"].(map[string]any)[literalMarker] != true {
		t.Errorf("condition not marked: %v", condition)
	}
	if then := got["then"].(map[string]any); then["properties"].(map[string]any)["B"].(map[string]any)[literalMarker] != nil {
		t.Errorf("then marked: %v", then)
	}
}

func TestExtensionFilesAreListed(t *testing.T) {
	listed := map[string]bool{}
	for _, ext := range cfnLintExtensions {
		listed[ext.File] = true
		if !strings.HasPrefix(ext.Rule, "E") {
			t.Errorf("%s: only error-level cfn-lint rules are applied", ext.Rule)
		}
	}
	err := fs.WalkDir(extensionFiles, "cfnlint/extensions", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if name := strings.TrimPrefix(path, "cfnlint/extensions/"); !listed[name] {
			t.Errorf("vendored %s is not applied", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name := range listed {
		if _, err := extensionFiles.ReadFile("cfnlint/extensions/" + name); err != nil {
			t.Errorf("listed %s is not vendored", name)
		}
	}
}

// TestExtensionsOnFixture applies real cfn-lint extension files to the test
// resource. The cases are the shapes that broke CUE evaluation: not over
// required properties, a nested if in then, anyOf in a condition, a
// lookahead pattern, and a rule on list items.
func TestExtensionsOnFixture(t *testing.T) {
	saved := cfnLintExtensions
	defer func() { cfnLintExtensions = saved }()
	thing := func(paths ...string) map[string][]string { return map[string][]string{"AWS::Test::Thing": paths} }
	cfnLintExtensions = []extension{
		{Rule: "E3045", File: "aws_s3_bucket/ownershipcontrols.json", Paths: thing("")},
		{Rule: "E3638", File: "aws_dynamodb_table/billingmode_exclusive.json", Paths: thing("")},
		{Rule: "E3681", File: "aws_elasticloadbalancingv2_targetgroup/targettype_restrictions.json", Paths: thing("")},
		{Rule: "E3682", File: "aws_rds_dbinstance/aurora_exclusive.json", Paths: thing(""), LowercaseEngine: true},
		{Rule: "E3719", File: "aws_rds_dbinstance/backupretentionperiod_maximum.json", Paths: thing("")},
		{Rule: "E3044", File: "aws_ecs_service/fargate.json", Paths: thing("")},
		{Rule: "E3723", File: "aws_apigateway_stage/method_settings_resource_path.json", Paths: thing("Settings/*")},
	}
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	str := map[string]any{"type": "string"}
	for _, name := range []string{"AccessControl", "BillingMode", "TargetType", "Protocol", "VpcId", "Engine", "DBClusterIdentifier", "MasterUserPassword", "LaunchType", "SchedulingStrategy"} {
		properties[name] = str
	}
	properties["Port"] = map[string]any{"type": "integer"}
	properties["BackupRetentionPeriod"] = map[string]any{"type": "integer"}
	properties["OwnershipControls"] = map[string]any{"type": "object", "properties": map[string]any{"Rules": map[string]any{"type": "array"}}, "additionalProperties": false}
	throughput := map[string]any{"type": "integer"}
	properties["ProvisionedThroughput"] = map[string]any{"type": "object", "properties": map[string]any{"ReadCapacityUnits": throughput, "WriteCapacityUnits": throughput}, "additionalProperties": false}
	properties["Settings"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"ResourcePath": str, "MetricsEnabled": map[string]any{"type": "boolean"}}, "additionalProperties": false}}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "cfnLintExtension" {
			t.Errorf("extension not applied: %v", d)
		}
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Name:"one"}`,
		`{Name:"one",AccessControl:"Private"}`,
		`{Name:"one",AccessControl:{Ref:"Acl"}}`, // an intrinsic never satisfies a condition
		`{Name:"one",AccessControl:"PublicRead",OwnershipControls:{Rules:[{}]}}`,
		`{Name:"one",BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:"0",WriteCapacityUnits:0}}`,
		`{Name:"one",TargetType:"lambda"}`,
		`{Name:"one",TargetType:"ip",Port:80,Protocol:"HTTP",VpcId:"vpc-1"}`,
		`{Name:"one",Engine:"mysql",BackupRetentionPeriod:7,MasterUserPassword:"x"}`,
		`{Name:"one",Engine:"Aurora-MySQL"}`,
		`{Name:"one",SchedulingStrategy:"DAEMON"}`,
		`{Name:"one",LaunchType:"FARGATE",SchedulingStrategy:"REPLICA"}`,
		`{Name:"one",Settings:[{ResourcePath:"x"},{ResourcePath:"/x",MetricsEnabled:true}]}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Name:"one",AccessControl:"PublicRead"}`,
		`{Name:"one",BillingMode:"PAY_PER_REQUEST",ProvisionedThroughput:{ReadCapacityUnits:5,WriteCapacityUnits:5}}`,
		`{Name:"one",TargetType:"lambda",Port:80}`,
		`{Name:"one",TargetType:"ip"}`,
		`{Name:"one",Engine:"Aurora-MySQL",MasterUserPassword:"x"}`,
		`{Name:"one",DBClusterIdentifier:"c",BackupRetentionPeriod:7}`,
		`{Name:"one",Engine:"mysql",BackupRetentionPeriod:36}`,
		`{Name:"one",LaunchType:"FARGATE",SchedulingStrategy:"DAEMON"}`,
		`{Name:"one",Settings:[{ResourcePath:"x",MetricsEnabled:true}]}`,
	} {
		validate(t, schema, data, false)
	}
}
