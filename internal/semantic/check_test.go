package semantic

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var attributes = map[string][]string{
	"AWS::S3::Bucket":                     {"Arn", "DomainName"},
	"AWS::CloudFormation::Stack":          {"Outputs.*"},
	"AWS::CloudFormation::CustomResource": {"*"},
	"AWS::SQS::Queue":                     {"Arn", "QueueName", "QueueUrl"},
}

func lookup(typ string) ([]string, bool) {
	if strings.HasPrefix(typ, "Custom::") {
		typ = "AWS::CloudFormation::CustomResource"
	}
	names, ok := attributes[typ]
	return names, ok
}

func check(t *testing.T, data string) []Diagnostic {
	t.Helper()
	var template map[string]any
	if err := json.Unmarshal([]byte(data), &template); err != nil {
		t.Fatal(err)
	}
	return Check(template, Options{Attributes: lookup})
}

// summary lists each diagnostic as severity:rule:path.
func summary(ds []Diagnostic) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Severity+":"+d.Rule+":"+d.Path)
	}
	return out
}

func TestReferences(t *testing.T) {
	cases := []struct {
		name, data string
		want       []string
	}{
		{"valid", `{
			"Parameters": {"Name": {"Type": "String"}},
			"Conditions": {"Named": {"Fn::Not": [{"Fn::Equals": [{"Ref": "Name"}, ""]}]}},
			"Mappings": {"Env": {"prod": {"Size": "2"}}},
			"Resources": {
				"Bucket": {"Type": "AWS::S3::Bucket", "Properties": {"BucketName": {"Fn::Sub": "${Name}-${AWS::Region}-${!Literal}"}}},
				"Queue": {"Type": "AWS::SQS::Queue", "DependsOn": ["Bucket"], "Properties": {
					"QueueName": {"Fn::Sub": ["${Prefix}-${Bucket.Arn}", {"Prefix": {"Ref": "Name"}}]},
					"DelaySeconds": {"Fn::FindInMap": ["Env", "prod", "Size"]},
					"MaximumMessageSize": {"Fn::FindInMap": ["Env", {"Ref": "Name"}, "Size"]},
					"Tags": [{"Key": "x", "Value": {"Fn::If": ["Named", {"Fn::GetAtt": "Bucket.DomainName"}, {"Ref": "AWS::NoValue"}]}}]}},
				"Stack": {"Type": "AWS::CloudFormation::Stack", "Properties": {"TemplateURL": "https://example.com/t"}},
				"Custom": {"Type": "Custom::Thing", "Properties": {"ServiceToken": {"Fn::GetAtt": ["Stack", "Outputs.Token"]}}},
				"Unknown": {"Type": "AWS::Unlisted::Thing"}
			},
			"Outputs": {
				"Any": {"Value": {"Fn::GetAtt": ["Custom", "Anything.At.All"]}},
				"Computed": {"Value": {"Fn::GetAtt": ["Bucket", {"Ref": "Name"}]}},
				"Unlisted": {"Value": {"Fn::GetAtt": ["Unknown", "Whatever"]}}
			}
		}`, []string{}},
		{"ref-undefined", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Ref": "Missing"}}}}}`,
			[]string{"error:ref-undefined:/Resources/Q/Properties/QueueName/Ref"}},
		{"ref-resource-in-condition", `{"Conditions": {"C": {"Fn::Equals": [{"Ref": "Q"}, "x"]}}, "Resources": {"Q": {"Type": "AWS::SQS::Queue"}}}`,
			[]string{"error:ref-context:/Conditions/C/Fn::Equals/0/Ref"}},
		{"getatt-missing-resource", `{"Parameters": {"P": {"Type": "String"}}, "Resources": {"Q": {"Type": "AWS::SQS::Queue"}}, "Outputs": {"A": {"Value": {"Fn::GetAtt": ["P", "Arn"]}}, "B": {"Value": {"Fn::GetAtt": "Nope.Arn"}}}}`,
			[]string{"error:getatt-undefined:/Outputs/A/Value/Fn::GetAtt", "error:getatt-undefined:/Outputs/B/Value/Fn::GetAtt"}},
		{"getatt-attribute", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue"}}, "Outputs": {"A": {"Value": {"Fn::GetAtt": ["Q", "Url"]}}, "B": {"Value": {"Fn::Sub": "${Q.Name}"}}}}`,
			[]string{"error:getatt-attribute:/Outputs/A/Value/Fn::GetAtt", "error:getatt-attribute:/Outputs/B/Value/Fn::Sub"}},
		{"stack-output-prefix", `{"Resources": {"S": {"Type": "AWS::CloudFormation::Stack"}}, "Outputs": {"A": {"Value": {"Fn::GetAtt": ["S", "Arn"]}}}}`,
			[]string{"error:getatt-attribute:/Outputs/A/Value/Fn::GetAtt"}},
		{"sub-undefined", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::Sub": ["${Bound}-${ Unbound }", {"Bound": "x"}]}}}}}`,
			[]string{"error:sub-undefined:/Resources/Q/Properties/QueueName/Fn::Sub"}},
		{"sub-variable-value", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::Sub": ["${X}", {"X": {"Ref": "Missing"}}]}}}}}`,
			[]string{"error:ref-undefined:/Resources/Q/Properties/QueueName/Fn::Sub/1/X/Ref"}},
		{"dependson", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "DependsOn": ["Missing"]}}}`,
			[]string{"error:dependson-undefined:/Resources/Q/DependsOn/0"}},
		{"condition-undefined", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "Condition": "Missing", "Properties": {"QueueName": {"Fn::If": ["Other", "a", "b"]}}}}}`,
			[]string{"error:condition-undefined:/Resources/Q/Condition", "error:condition-undefined:/Resources/Q/Properties/QueueName/Fn::If/0"}},
		{"condition-cycle", `{"Conditions": {"A": {"Fn::Not": [{"Condition": "B"}]}, "B": {"Fn::Not": [{"Condition": "A"}]}}, "Resources": {"Q": {"Type": "AWS::SQS::Queue"}}}`,
			[]string{"error:condition-cycle:/Conditions/A"}},
		{"policy-condition-key", `{"Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {"Policy": {"Condition": "NotAConditionName"}}}}}`,
			[]string{}},
		{"mapping", `{"Mappings": {"M": {"a": {"b": "c"}}}, "Resources": {"Q": {"Type": "AWS::SQS::Queue", "Properties": {
			"QueueName": {"Fn::FindInMap": ["Missing", "a", "b"]},
			"DelaySeconds": {"Fn::FindInMap": ["M", "x", "b"]},
			"MaximumMessageSize": {"Fn::FindInMap": ["M", "a", "y"]},
			"MessageRetentionPeriod": {"Fn::FindInMap": ["M", "a", "y", {"DefaultValue": "1"}]}}}}}`,
			[]string{"error:mapping-key:/Resources/Q/Properties/DelaySeconds/Fn::FindInMap",
				"error:mapping-key:/Resources/Q/Properties/MaximumMessageSize/Fn::FindInMap",
				"error:mapping-undefined:/Resources/Q/Properties/QueueName/Fn::FindInMap"}},
		{"duplicate", `{"Parameters": {"Q": {"Type": "String"}}, "Resources": {"Q": {"Type": "AWS::SQS::Queue"}}}`,
			[]string{"error:duplicate-logical-id:/Resources/Q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary(check(t, tc.data)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestCycles(t *testing.T) {
	const conditions = `"Parameters": {"Env": {"Type": "String"}},
		"Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}, "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]},
			"Always": {"Fn::Equals": ["a", "a"]}, "NotProd": {"Fn::Not": [{"Condition": "Prod"}]}},`
	cases := []struct {
		name, data string
		want       []string
	}{
		{"self", `{"Resources": {"A": {"Type": "AWS::SQS::Queue", "DependsOn": "A"}}}`,
			[]string{"error:dependency-cycle:/Resources/A"}},
		{"unconditional", `{"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::GetAtt": ["B", "QueueName"]}}},
			"B": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::Sub": "${C}"}}},
			"C": {"Type": "AWS::SQS::Queue", "DependsOn": "A"}}}`,
			[]string{"error:dependency-cycle:/Resources/A"}},
		{"acyclic", `{"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "DependsOn": ["B", "C"]},
			"B": {"Type": "AWS::SQS::Queue", "DependsOn": "C"},
			"C": {"Type": "AWS::SQS::Queue"}}}`, []string{}},
		{"conditional", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, "a"]}}},
			"B": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Ref": "A"}}}}}`,
			[]string{"warning:dependency-cycle:/Resources/A"}},
		{"contradictory-branches", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, "a"]}}},
			"B": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", "b", {"Ref": "A"}]}}}}}`,
			[]string{}},
		{"exclusive-values", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Condition": "Prod", "DependsOn": "B"},
			"B": {"Type": "AWS::SQS::Queue", "Condition": "Dev", "DependsOn": "A"}}}`,
			// No cycle, but each DependsOn names a resource that is absent
			// whenever its dependent exists. That still depends on Env.
			[]string{"warning:conditional-target:/Resources/A/DependsOn", "warning:conditional-target:/Resources/B/DependsOn"}},
		{"negated-condition", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Condition": "Prod", "DependsOn": "B"},
			"B": {"Type": "AWS::SQS::Queue", "Condition": "NotProd", "DependsOn": "A"}}}`,
			[]string{"warning:conditional-target:/Resources/A/DependsOn", "warning:conditional-target:/Resources/B/DependsOn"}},
		{"constant-condition", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Condition": "Always", "DependsOn": "B"},
			"B": {"Type": "AWS::SQS::Queue", "DependsOn": "A"}}}`,
			[]string{"error:dependency-cycle:/Resources/A"}},
		{"both-branches", `{` + conditions + `"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, {"Fn::Sub": "${B}"}]}}},
			"B": {"Type": "AWS::SQS::Queue", "DependsOn": "A"}}}`,
			[]string{"error:dependency-cycle:/Resources/A"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := check(t, tc.data)
			if got := summary(ds); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestConditionalTargets(t *testing.T) {
	ds := check(t, `{"Parameters": {"Env": {"Type": "String"}},
		"Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}, "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]},
			"ProdOrDev": {"Fn::Or": [{"Condition": "Prod"}, {"Condition": "Dev"}]}, "Never": {"Fn::Equals": ["a", "b"]}},
		"Resources": {
			"Target": {"Type": "AWS::SQS::Queue", "Condition": "Prod"},
			"Wide": {"Type": "AWS::SQS::Queue", "Condition": "ProdOrDev"},
			"Absent": {"Type": "AWS::SQS::Queue", "Condition": "Never"},
			"Impossible": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Ref": "Absent"}}},
			"Same": {"Type": "AWS::SQS::Queue", "Condition": "Prod", "DependsOn": "Target", "Properties": {"QueueName": {"Ref": "Wide"}}},
			"Guarded": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", {"Fn::GetAtt": ["Target", "QueueName"]}, {"Ref": "AWS::NoValue"}]}}},
			"Unguarded": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::Sub": "${Target}"}}},
			"Opposite": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", "x", {"Ref": "Target"}]}}}},
		"Outputs": {"Target": {"Condition": "Prod", "Value": {"Ref": "Target"}}, "Unconditional": {"Value": {"Ref": "Wide"}}}}`)
	want := []string{
		"warning:conditional-target:/Resources/Opposite/Properties/QueueName/Fn::If/2/Ref",
		"error:conditional-target:/Resources/Impossible/Properties/QueueName/Ref",
		"warning:conditional-target:/Resources/Unguarded/Properties/QueueName/Fn::Sub",
		"warning:conditional-target:/Outputs/Unconditional/Value/Ref",
	}
	got := summary(ds)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestConditionalCycleGuards(t *testing.T) {
	ds := check(t, `{"Parameters": {"Env": {"Type": "String"}},
		"Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}},
		"Resources": {
			"A": {"Type": "AWS::SQS::Queue", "Properties": {"QueueName": {"Fn::If": ["Prod", {"Ref": "B"}, "a"]}}},
			"B": {"Type": "AWS::SQS::Queue", "DependsOn": "A"}}}`)
	if len(ds) != 1 || !reflect.DeepEqual(ds[0].Conditions, []Guard{{"Prod", true}}) || ds[0].Message != "circular dependency when its conditions hold: A -> B -> A" {
		t.Fatalf("%+v", ds)
	}
}

func TestClassify(t *testing.T) {
	var template map[string]any
	_ = json.Unmarshal([]byte(`{"Conditions": {
		"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]},
		"Dev": {"Fn::Equals": ["dev", {"Ref": "Env"}]},
		"Same": {"Fn::Equals": [{"Ref": "Env"}, {"Ref": "Env"}]},
		"Literal": {"Fn::Equals": ["1", 1]},
		"Either": {"Fn::Or": [{"Condition": "Prod"}, {"Fn::Not": [{"Condition": "Prod"}]}]},
		"Region": {"Fn::Equals": [{"Ref": "AWS::Region"}, "us-east-1"]}}}`), &template)
	c := Analyze(template).Conditions
	_ = json.Unmarshal([]byte(`{"Parameters": {"Env": {"Type": "String", "AllowedValues": ["prod", "dev"]}, "Size": {"Type": "Number", "AllowedValues": [1, 2]}},
		"Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}, "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]},
			"Test": {"Fn::Equals": [{"Ref": "Env"}, "test"]}, "One": {"Fn::Equals": [{"Ref": "Size"}, "1"]}}}`), &template)
	allowed := Analyze(template).Conditions
	for _, tc := range []struct {
		guards []Guard
		want   Truth
	}{
		{[]Guard{{"Prod", false}, {"Dev", false}}, Never},
		{[]Guard{{"Prod", false}}, Sometimes},
		{[]Guard{{"Test", true}}, Never},
		{[]Guard{{"One", true}}, Sometimes},
	} {
		if got := allowed.Classify(tc.guards); got != tc.want {
			t.Errorf("allowed values %v: got %s, want %s", tc.guards, got, tc.want)
		}
	}
	for _, tc := range []struct {
		guards []Guard
		want   Truth
	}{
		{nil, Always},
		{[]Guard{{"Prod", true}}, Sometimes},
		{[]Guard{{"Prod", true}, {"Prod", false}}, Never},
		{[]Guard{{"Prod", true}, {"Dev", true}}, Never},
		{[]Guard{{"Prod", false}, {"Dev", false}}, Sometimes},
		{[]Guard{{"Same", true}}, Always},
		{[]Guard{{"Literal", true}}, Always},
		{[]Guard{{"Either", true}}, Always},
		// The validation region is not substituted for AWS::Region.
		{[]Guard{{"Region", true}}, Sometimes},
		{[]Guard{{"Missing", true}}, Sometimes},
	} {
		if got := c.Classify(tc.guards); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.guards, got, tc.want)
		}
	}
}

func TestReferenceGuards(t *testing.T) {
	var template map[string]any
	_ = json.Unmarshal([]byte(`{"Resources": {"A": {"Type": "AWS::SQS::Queue", "Condition": "C",
		"Properties": {"QueueName": {"Fn::If": ["D", {"Fn::If": ["E", "x", {"Ref": "P"}]}, "y"]}}}}}`), &template)
	for _, r := range Analyze(template).References {
		if r.Kind == KindRef {
			want := []Guard{{"C", true}, {"D", true}, {"E", false}}
			if !reflect.DeepEqual(r.Guards, want) || Pointer(r.Path) != "/Resources/A/Properties/QueueName/Fn::If/1/Fn::If/2/Ref" {
				t.Fatalf("%+v", r)
			}
			return
		}
	}
	t.Fatal("Ref not found")
}

func TestSubVariables(t *testing.T) {
	got := SubVariables("${A}-${ B.C }-${!D}-${ !E}-$${F}-${}")
	if want := []string{"A", "B.C", "F"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}
