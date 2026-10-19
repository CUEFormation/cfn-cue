package semantic

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cueformation/cfn-cue/internal/generate"
)

// TestSidPlacements checks that sidPlacements lists exactly the identity,
// trust and KMS key policies the generator places.
func TestSidPlacements(t *testing.T) {
	unique := map[string]bool{"IdentityPolicyValue": true, "TrustPolicyValue": true, "KMSKeyPolicyValue": true}
	// Schema pointers as property paths. Policy is the inline policy list item.
	paths := map[string]string{"#/definitions/Policy/properties/PolicyDocument": "Policies/*/PolicyDocument"}
	want := map[string][]string{}
	for typ, pointers := range generate.IAMPolicies() {
		for pointer, profile := range pointers {
			if !unique[profile] {
				continue
			}
			path, ok := paths[pointer]
			if !ok {
				path, ok = strings.CutPrefix(pointer, "#/properties/")
				ok = ok && !strings.Contains(path, "/")
			}
			if !ok {
				t.Fatalf("%s: no property path for %s", typ, pointer)
			}
			want[typ] = append(want[typ], path)
		}
		sort.Strings(want[typ])
	}
	got := map[string][]string{}
	for typ, p := range sidPlacements {
		got[typ] = append([]string(nil), p...)
		sort.Strings(got[typ])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sidPlacements = %v, want %v", got, want)
	}
}

// complexCondition returns a template with a ManagedPolicy and a condition
// Complex over more comparisons than Classify enumerates.
func complexCondition(statements string) string { return conditionOver(maxAtoms+1, statements) }

// conditionOver returns a template with a ManagedPolicy and a condition
// Complex that conjoins n independent comparisons.
func conditionOver(n int, statements string) string {
	var comparisons []string
	for i := range n {
		comparisons = append(comparisons, fmt.Sprintf(`{"Fn::Equals": [{"Ref": "P%d"}, "x"]}`, i))
	}
	parameters := make([]string, n)
	for i := range parameters {
		parameters[i] = fmt.Sprintf(`"P%d": {"Type": "String"}`, i)
	}
	return `{"Parameters": {` + strings.Join(parameters, ",") + `},
		"Conditions": {"Complex": {"Fn::And": [{"Fn::And": [` + strings.Join(comparisons[:8], ",") + `]}, {"Fn::And": [` + strings.Join(comparisons[8:], ",") + `]}]}},
		"Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Properties": {"PolicyDocument": {"Statement": ` + statements + `}}}}}`
}

func TestDuplicateSids(t *testing.T) {
	s := func(sid string) string {
		return `{"Sid": "` + sid + `", "Effect": "Allow", "Action": "s3:*", "Resource": "*"}`
	}
	role := func(statements string) string {
		return `{"Parameters": {"Env": {"Type": "String", "AllowedValues": ["prod", "dev"]}},
			"Conditions": {"Prod": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}, "Dev": {"Fn::Equals": [{"Ref": "Env"}, "dev"]},
				"NotProd": {"Fn::Not": [{"Condition": "Prod"}]}, "Other": {"Fn::Equals": [{"Ref": "AWS::Region"}, "us-east-1"]}},
			"Resources": {"R": {"Type": "AWS::IAM::Role", "Properties": {"AssumeRolePolicyDocument": {"Statement": []},
				"Policies": [{"PolicyName": "p", "PolicyDocument": {"Statement": ` + statements + `}}]}}}}`
	}
	nv := `{"Ref": "AWS::NoValue"}`
	p := "/Resources/R/Properties/Policies/0/PolicyDocument/Statement"
	cases := []struct {
		name, data string
		want       []string
	}{
		{"distinct", role(`[` + s("A") + `,` + s("B") + `]`), []string{}},
		{"duplicate", role(`[` + s("A") + `,` + s("A") + `]`), []string{"error:duplicate-sid:" + p + "/1/Sid"}},
		{"empty", role(`[` + s("") + `,` + s("") + `]`), []string{"error:duplicate-sid:" + p + "/1/Sid"}},
		{"single", role(s("A")), []string{}},
		{"intrinsic", role(`[{"Sid": {"Fn::Sub": "A"}, "Effect": "Allow"}, {"Sid": {"Fn::Sub": "A"}, "Effect": "Allow"}]`), []string{}},
		{"novalue", role(`[` + nv + `,` + s("A") + `]`), []string{}},
		{"same if", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + s("A") + `]}]`), []string{}},
		{"exclusive", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["Prod", ` + nv + `, ` + s("A") + `]}]`), []string{}},
		{"not", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["NotProd", ` + s("A") + `, ` + nv + `]}]`), []string{}},
		{"allowed values", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["Dev", ` + s("A") + `, ` + nv + `]}]`), []string{}},
		{"both", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}]`), []string{"warning:duplicate-sid:" + p + "/1/Fn::If/1/Sid"}},
		{"unrelated", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["Other", ` + s("A") + `, ` + nv + `]}]`), []string{"warning:duplicate-sid:" + p + "/1/Fn::If/1/Sid"}},
		{"unconditional", role(`[` + s("A") + `, {"Fn::If": ["Prod", ` + nv + `, ` + s("A") + `]}]`), []string{"warning:duplicate-sid:" + p + "/1/Fn::If/2/Sid"}},
		{"whole if", role(`{"Fn::If": ["Prod", [` + s("A") + `], [` + s("A") + `]]}`), []string{}},
		{"whole if dup", role(`{"Fn::If": ["Prod", [` + s("A") + `,` + s("A") + `], ` + nv + `]}`), []string{"warning:duplicate-sid:" + p + "/Fn::If/1/1/Sid"}},
		{"whole if one branch", role(`{"Fn::If": ["Prod", [` + s("A") + `,` + s("A") + `], [` + s("A") + `]]}`), []string{"warning:duplicate-sid:" + p + "/Fn::If/1/1/Sid"}},
		{"later unconditional", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, ` + s("A") + `, ` + s("A") + `]`),
			[]string{"warning:duplicate-sid:" + p + "/1/Sid", "error:duplicate-sid:" + p + "/2/Sid"}},
		{"whole if both lists", role(`{"Fn::If": ["Prod", [` + s("A") + `,` + s("A") + `], [` + s("A") + `,` + s("A") + `]]}`),
			[]string{"error:duplicate-sid:" + p + "/Fn::If/1/1/Sid", "error:duplicate-sid:" + p + "/Fn::If/2/1/Sid"}},
		{"covering earlier entries", role(`[{"Fn::If": ["Prod", ` + s("A") + `, ` + nv + `]}, {"Fn::If": ["Prod", ` + nv + `, ` + s("A") + `]}, ` + s("A") + `]`),
			[]string{"error:duplicate-sid:" + p + "/2/Sid"}},
		{"never branch", `{"Conditions": {"Never": {"Fn::Equals": ["x", "y"]}}, "Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Properties": {"PolicyDocument": {"Statement": [` + s("A") + `, {"Fn::If": ["Never", ` + s("A") + `, ` + s("A") + `]}]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Statement/1/Fn::If/2/Sid"}},
		{"both branches", role(`[` + s("A") + `, {"Fn::If": ["Prod", ` + s("A") + `, ` + s("A") + `]}]`), []string{"error:duplicate-sid:" + p + "/1/Fn::If/1/Sid"}},
		{"allowed values cover", role(`[` + s("A") + `, {"Fn::If": ["Prod", ` + s("A") + `, {"Fn::If": ["Dev", ` + s("A") + `, ` + nv + `]}]}]`),
			[]string{"error:duplicate-sid:" + p + "/1/Fn::If/1/Sid"}},
		{"json string", `{"Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Properties": {"PolicyDocument": "{\"Statement\": [{\"Sid\": \"A\"}, {\"Sid\": \"A\"}]}"}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Statement/1/Sid"}},
		{"conditional resource", `{"Conditions": {"C": {"Fn::Equals": [{"Ref": "AWS::Region"}, "x"]}}, "Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Condition": "C", "Properties": {"PolicyDocument": {"Statement": [` + s("A") + `,` + s("A") + `]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Statement/1/Sid"}},
		{"if policy", `{"Conditions": {"C": {"Fn::Equals": [{"Ref": "AWS::Region"}, "x"]}}, "Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Properties": {"PolicyDocument": {"Fn::If": ["C", {"Statement": [` + s("A") + `,` + s("A") + `]}, ` + nv + `]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Fn::If/1/Statement/1/Sid"}},
		{"implied by resource", `{"Conditions": {"C": {"Fn::Equals": [{"Ref": "AWS::Region"}, "x"]}}, "Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Condition": "C", "Properties": {"PolicyDocument": {"Statement": [` + s("A") + `, {"Fn::If": ["C", ` + s("A") + `, ` + nv + `]}]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Statement/1/Fn::If/1/Sid"}},
		{"implied by policy if", `{"Conditions": {"C": {"Fn::Equals": [{"Ref": "AWS::Region"}, "x"]}}, "Resources": {"P": {"Type": "AWS::IAM::ManagedPolicy", "Properties": {"PolicyDocument": {"Fn::If": ["C", {"Statement": [` + s("A") + `, {"Fn::If": ["C", ` + s("A") + `, ` + nv + `]}]}, ` + nv + `]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/P/Properties/PolicyDocument/Fn::If/1/Statement/1/Fn::If/1/Sid"}},
		{"unknown one entry", complexCondition(`[{"Fn::If": ["Complex", ` + s("A") + `, ` + s("A") + `]}]`), []string{}},
		{"many entries", conditionOver(12, `[`+strings.Repeat(`{"Fn::If": ["Complex", `+s("A")+`, `+nv+`]},`, 29)+`{"Fn::If": ["Complex", `+s("A")+`, `+nv+`]}]`),
			nil},
		{"unknown two entries", complexCondition(`[` + s("A") + `, {"Fn::If": ["Complex", ` + s("A") + `, ` + nv + `]}]`),
			[]string{"warning:duplicate-sid:/Resources/P/Properties/PolicyDocument/Statement/1/Fn::If/1/Sid"}},
		{"trust", `{"Resources": {"R": {"Type": "AWS::IAM::Role", "Properties": {"AssumeRolePolicyDocument": {"Statement": [` + s("A") + `,` + s("A") + `]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/R/Properties/AssumeRolePolicyDocument/Statement/1/Sid"}},
		{"kms", `{"Resources": {"K": {"Type": "AWS::KMS::Key", "Properties": {"KeyPolicy": {"Statement": [` + s("A") + `,` + s("A") + `]}}}}}`,
			[]string{"error:duplicate-sid:/Resources/K/Properties/KeyPolicy/Statement/1/Sid"}},
		{"bucket policy", `{"Resources": {"P": {"Type": "AWS::S3::BucketPolicy", "Properties": {"Bucket": "b", "PolicyDocument": {"Statement": [` + s("A") + `,` + s("A") + `]}}}}}`,
			[]string{}},
		{"scp", `{"Resources": {"P": {"Type": "AWS::Organizations::Policy", "Properties": {"Type": "SERVICE_CONTROL_POLICY", "Content": {"Statement": [` + s("A") + `,` + s("A") + `]}}}}}`,
			[]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := check(t, tc.data)
			got := []string{}
			for _, d := range ds {
				if d.Rule == "duplicate-sid" {
					got = append(got, d.Severity+":"+d.Rule+":"+d.Path)
					t.Log(d.Path + ": " + d.Message)
				}
			}
			if tc.want == nil {
				if len(got) != 29 {
					t.Fatalf("got %d diagnostics, want 29", len(got))
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
