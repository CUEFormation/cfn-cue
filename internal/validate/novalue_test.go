package validate

import (
	"os"
	"strings"
	"testing"
)

// noValueTemplate declares parameters and conditions shared by the cases.
// Env takes only prod or dev; Free takes any value.
func noValueTemplate(resources string) string {
	return `{"Parameters":{"Env":{"Type":"String","AllowedValues":["prod","dev"]},"Free":{"Type":"String"}},
"Conditions":{
 "IsProd":{"Fn::Equals":[{"Ref":"Env"},"prod"]},
 "IsDev":{"Fn::Equals":["dev",{"Ref":"Env"}]},
 "IsStaging":{"Fn::Equals":[{"Ref":"Env"},"staging"]},
 "NotProd":{"Fn::Not":[{"Condition":"IsProd"}]},
 "ProdOrDev":{"Fn::Or":[{"Condition":"IsProd"},{"Condition":"IsDev"}]},
 "FreeA":{"Fn::Equals":[{"Ref":"Free"},"a"]},
 "FreeB":{"Fn::Equals":[{"Ref":"Free"},"b"]},
 "Always":{"Fn::Equals":["x","x"]},
 "Never":{"Fn::Equals":["x","y"]}},
"Resources":` + resources + `}`
}

const noValue = `{"Ref":"AWS::NoValue"}`

func thing(properties string) string {
	return `{"Thing":{"Type":"AWS::Test::Thing","Properties":` + properties + `}}`
}

func TestNoValueRequiredProperties(t *testing.T) {
	dir := fixture(t)
	cases := []struct {
		name, resources, status string
		message                 []string
	}{
		{"second-branch", thing(`{"Name":{"Fn::If":["IsProd","one",` + noValue + `]}}`), "invalid",
			[]string{"Resources.Thing: AWS::NoValue removes Properties.Name when condition IsProd is false", "Properties.Name: field is required but not present"}},
		{"first-branch", thing(`{"Name":{"Fn::If":["IsProd",` + noValue + `,"one"]}}`), "invalid",
			[]string{"removes Properties.Name when condition IsProd is true"}},
		{"both-branches", thing(`{"Name":{"Fn::If":["IsProd",` + noValue + `,` + noValue + `]}}`), "invalid",
			[]string{"removes Properties.Name when condition IsProd is true, or when condition IsProd is false"}},
		{"unconditional", thing(`{"Name":` + noValue + `}`), "invalid",
			[]string{"removes Properties.Name:\n"}},
		{"mixed-branches", thing(`{"Name":{"Fn::If":["FreeA",` + noValue + `,{"Fn::If":["FreeB","one",` + noValue + `]}]}}`), "invalid",
			[]string{"removes Properties.Name when condition FreeA is true, or when condition FreeA is false and condition FreeB is false"}},
		{"no-branch", thing(`{"Name":{"Fn::If":["IsProd","one","two"]}}`), "valid", nil},
		{"optional", thing(`{"Name":"one","Count":{"Fn::If":["IsProd",2,` + noValue + `]},"Names":` + noValue + `}`), "valid", nil},

		// Nested conditionals combine the choices on the path to AWS::NoValue.
		{"nested-reachable", thing(`{"Name":{"Fn::If":["FreeA","one",{"Fn::If":["FreeB","two",` + noValue + `]}]}}`), "invalid",
			[]string{"removes Properties.Name when condition FreeA is false and condition FreeB is false"}},
		{"nested-allowed-values", thing(`{"Name":{"Fn::If":["IsProd","one",{"Fn::If":["IsDev","two",` + noValue + `]}]}}`), "valid", nil},
		{"nested-same-unreachable", thing(`{"Name":{"Fn::If":["IsProd",{"Fn::If":["IsProd","one",` + noValue + `]},"two"]}}`), "valid", nil},
		{"nested-same-reachable", thing(`{"Name":{"Fn::If":["IsProd","one",{"Fn::If":["IsProd","two",` + noValue + `]}]}}`), "invalid",
			[]string{"removes Properties.Name when condition IsProd is false"}},
		{"not", thing(`{"Name":{"Fn::If":["IsProd","one",{"Fn::If":["NotProd","two",` + noValue + `]}]}}`), "valid", nil},
		{"or-exhaustive", thing(`{"Name":{"Fn::If":["ProdOrDev","one",` + noValue + `]}}`), "valid", nil},
		{"not-allowed-value", thing(`{"Name":{"Fn::If":["IsStaging",` + noValue + `,"one"]}}`), "valid", nil},
		{"constant-true", thing(`{"Name":{"Fn::If":["Always","one",` + noValue + `]}}`), "valid", nil},
		{"constant-true-removes", thing(`{"Name":{"Fn::If":["Always",` + noValue + `,"one"]}}`), "invalid",
			[]string{"removes Properties.Name when condition Always is true"}},

		// An undeclared condition is unknown: neither branch is excluded.
		{"undeclared", thing(`{"Name":{"Fn::If":["Missing","one",` + noValue + `]}}`), "invalid",
			[]string{"removes Properties.Name when condition Missing is false"}},

		// A resource Condition holds wherever the resource exists.
		{"resource-condition", `{"Thing":{"Type":"AWS::Test::Thing","Condition":"IsProd","Properties":{"Name":{"Fn::If":["IsProd","one",` + noValue + `]}}}}`, "valid", nil},
		{"resource-condition-removes", `{"Thing":{"Type":"AWS::Test::Thing","Condition":"IsProd","Properties":{"Name":{"Fn::If":["IsProd",` + noValue + `,"one"]}}}}`, "invalid",
			[]string{"removes Properties.Name when condition IsProd is true"}},
		{"resource-condition-related", `{"Thing":{"Type":"AWS::Test::Thing","Condition":"IsDev","Properties":{"Name":{"Fn::If":["IsProd",` + noValue + `,"one"]}}}}`, "valid", nil},
		{"resource-never-created", `{"Thing":{"Type":"AWS::Test::Thing","Condition":"Never","Properties":{"Name":` + noValue + `}}}`, "valid", nil},

		// Fn::If around the whole Properties object.
		{"properties-removed", thing(`{"Fn::If":["IsProd",{"Name":"one"},` + noValue + `]}`), "invalid",
			[]string{"removes Properties when condition IsProd is false", "Resources.Thing.Properties: field is required but not present"}},
		{"properties-branch", thing(`{"Fn::If":["IsProd",{"Name":{"Fn::If":["IsProd","one",` + noValue + `]}},{"Name":"two"}]}`), "valid", nil},
		{"properties-branch-removes", thing(`{"Fn::If":["IsProd",{"Name":"one"},{"Name":{"Fn::If":["IsDev","two",` + noValue + `]}}]}`), "valid", nil},
		{"properties-branch-free", thing(`{"Fn::If":["FreeA",{"Name":"one"},{"Name":{"Fn::If":["FreeB","two",` + noValue + `]}}]}`), "invalid",
			[]string{"removes Properties.Fn::If[2].Name when condition FreeA is false and condition FreeB is false"}},

		// Nested objects, list entries and presence groups.
		{"nested-required", `{"N":{"Type":"AWS::Test::Nested","Properties":{"Primary":"p","Settings":[{"Value":"v","Key":{"Fn::If":["IsProd","k",` + noValue + `]}}]}}}`, "invalid",
			[]string{"Resources.N: AWS::NoValue removes Properties.Settings[0].Key when condition IsProd is false", "Settings.0.Key: field is required but not present"}},
		{"list-entry", `{"N":{"Type":"AWS::Test::Nested","Properties":{"Primary":"p","Settings":[{"Fn::If":["IsProd",{"Key":"k"},` + noValue + `]},{"Key":"j"}]}}}`, "valid", nil},
		// Only required properties are in scope. A removal that leaves a
		// list too short, as AWS::NoValue can, is not reported.
		{"list-too-short", `{"N":{"Type":"AWS::Test::Nested","Properties":{"Primary":"p","Settings":[{"Fn::If":["IsProd",{"Key":"k"},` + noValue + `]}]}}}`, "valid", nil},
		{"group-alternate", `{"N":{"Type":"AWS::Test::Nested","Properties":{"Primary":{"Fn::If":["IsProd","a",` + noValue + `]},"Secondary":{"Fn::If":["IsProd",` + noValue + `,"b"]}}}}`, "valid", nil},
		{"group-both-removed", `{"N":{"Type":"AWS::Test::Nested","Properties":{"Primary":{"Fn::If":["IsProd","a",` + noValue + `]},"Secondary":{"Fn::If":["IsProd","b",` + noValue + `]}}}}`, "invalid",
			[]string{"removes Properties.Primary, Properties.Secondary when condition IsProd is false"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(noValueTemplate(test.resources))
			resource := "Thing"
			if !strings.Contains(test.resources, `"Thing"`) {
				resource = "N"
			}
			for _, only := range []string{"", resource} {
				result := Check(data, Options{Schemas: dir, Region: "us-east-1", Resource: only})
				if result.Status != test.status {
					t.Fatalf("resource=%q: %s, want %s: %s", only, result.Status, test.status, result.Message)
				}
				for _, want := range test.message {
					if !strings.Contains(result.Message, want) {
						t.Errorf("resource=%q: message omits %q:\n%s", only, want, result.Message)
					}
				}
			}
		})
	}
}

// A template that already fails reports the schema error, not a variant.
func TestNoValueAfterSchemaError(t *testing.T) {
	dir := fixture(t)
	data := []byte(noValueTemplate(thing(`{"Name":{"Fn::If":["IsProd",[],` + noValue + `]}}`)))
	result := Check(data, Options{Schemas: dir, Region: "us-east-1"})
	if result.Status != "invalid" || strings.Contains(result.Message, "AWS::NoValue removes") {
		t.Fatalf("%s: %s", result.Status, result.Message)
	}
}

// TestIAMRoleNoValue checks the IAM Role cases compared with cfn-lint 1.57.2
// against ../../generated. Set CFN_CUE_INTEGRATION=1 to run.
func TestIAMRoleNoValue(t *testing.T) {
	if os.Getenv("CFN_CUE_INTEGRATION") != "1" {
		t.Skip("set CFN_CUE_INTEGRATION=1 to check the generated IAM Role schema")
	}
	trust := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	identity := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
	role := func(attributes, properties string) string {
		return `{"Role":{"Type":"AWS::IAM::Role"` + attributes + `,"Properties":` + properties + `}}`
	}
	document := func(value string) string { return `{"AssumeRolePolicyDocument":` + value + `}` }
	cases := []struct{ name, resources, status, message string }{
		{"literal", role("", document(trust)), "valid", ""},
		{"no-branch", role("", document(`{"Fn::If":["IsProd",`+trust+`,`+trust+`]}`)), "valid", ""},
		{"second-branch", role("", document(`{"Fn::If":["IsProd",`+trust+`,`+noValue+`]}`)), "invalid", "removes Properties.AssumeRolePolicyDocument when condition IsProd is false"},
		{"first-branch", role("", document(`{"Fn::If":["IsProd",`+noValue+`,`+trust+`]}`)), "invalid", "removes Properties.AssumeRolePolicyDocument when condition IsProd is true"},
		{"both-branches", role("", document(`{"Fn::If":["IsProd",`+noValue+`,`+noValue+`]}`)), "invalid", "removes Properties.AssumeRolePolicyDocument when condition IsProd is true, or when condition IsProd is false"},
		{"nested-allowed-values", role("", document(`{"Fn::If":["IsProd",`+trust+`,{"Fn::If":["IsDev",`+trust+`,`+noValue+`]}]}`)), "valid", ""},
		{"nested-reachable", role("", document(`{"Fn::If":["FreeA",`+trust+`,{"Fn::If":["FreeB",`+trust+`,`+noValue+`]}]}`)), "invalid", "when condition FreeA is false and condition FreeB is false"},
		{"resource-condition", role(`,"Condition":"IsProd"`, document(`{"Fn::If":["IsProd",`+trust+`,`+noValue+`]}`)), "valid", ""},
		{"resource-condition-removes", role(`,"Condition":"IsProd"`, document(`{"Fn::If":["IsProd",`+noValue+`,`+trust+`]}`)), "invalid", "when condition IsProd is true"},
		{"optional", role("", `{"AssumeRolePolicyDocument":`+trust+`,"RoleName":{"Fn::If":["IsProd","r",`+noValue+`]},"Path":`+noValue+`}`), "valid", ""},
		{"inline-policy", role("", `{"AssumeRolePolicyDocument":`+trust+`,"Policies":[{"PolicyName":"p","PolicyDocument":{"Fn::If":["IsProd",`+identity+`,`+noValue+`]}}]}`), "invalid", "removes Properties.Policies[0].PolicyDocument when condition IsProd is false"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result := Check([]byte(noValueTemplate(test.resources)), Options{Schemas: "../../generated", Region: "us-east-1"})
			if result.Status != test.status || !strings.Contains(result.Message, test.message) {
				t.Fatalf("%s, want %s with %q: %s", result.Status, test.status, test.message, result.Message)
			}
		})
	}
}

// Removal failures follow the same policy as schema failures: every failing
// resource is reported in logical-ID order, and unsupported resources are
// listed as not checked under an invalid status.
func TestNoValueDeterministic(t *testing.T) {
	dir := fixture(t)
	resources := `{"Zed":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["IsProd",` + noValue + `,"one"]}}},
"Absent":{"Type":"AWS::Absent::Thing"},
"Alpha":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["IsProd","one",` + noValue + `]}}}}`
	data := []byte(noValueTemplate(resources))
	first := Check(data, Options{Schemas: dir, Region: "us-east-1"})
	alpha := strings.Index(first.Message, "Resources.Alpha: AWS::NoValue removes Properties.Name when condition IsProd is false")
	zed := strings.Index(first.Message, "Resources.Zed: AWS::NoValue removes Properties.Name when condition IsProd is true")
	if first.Status != "invalid" || alpha < 0 || zed < alpha || !strings.HasSuffix(first.Message, "\nnot checked: resource Absent: AWS::Absent::Thing is absent from us-east-1") {
		t.Fatalf("%s: %s", first.Status, first.Message)
	}
	for i := 0; i < 5; i++ {
		if again := Check(data, Options{Schemas: dir, Region: "us-east-1"}); again.Status != first.Status || again.Message != first.Message {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, again.Message, first.Message)
		}
	}
	// Without removal failures, the unsupported resource decides the status.
	valid := []byte(noValueTemplate(`{"Absent":{"Type":"AWS::Absent::Thing"},"Alpha":{"Type":"AWS::Test::Thing","Properties":{"Name":{"Fn::If":["IsProd","one","two"]}}}}`))
	if result := Check(valid, Options{Schemas: dir, Region: "us-east-1"}); result.Status != "unsupported" {
		t.Fatalf("%s: %s", result.Status, result.Message)
	}
}
