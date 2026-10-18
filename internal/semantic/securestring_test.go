package semantic

import (
	"reflect"
	"strings"
	"testing"
)

// The expectations match cfn-lint v1.57.2's E1027 for the same templates.
func TestSecureStrings(t *testing.T) {
	const secure = "{{resolve:ssm-secure:Secret:1}}"
	resource := func(typ, properties string) string {
		return `{"Resources": {"R": {"Type": "` + typ + `", "Properties": ` + properties + `}}}`
	}
	cases := []struct {
		name, data string
		want       []string
	}{
		{"allowed", resource("AWS::RDS::DBInstance", `{"MasterUserPassword": "`+secure+`"}`), []string{}},
		{"allowed-if", `{"Conditions": {"C": {"Fn::Equals": ["a", "b"]}}, "Resources": {"R": {"Type": "AWS::RDS::DBCluster",
			"Properties": {"MasterUserPassword": {"Fn::If": ["C", "` + secure + `", "{{resolve:ssm:Plain:1}}"]}}}}}`, []string{}},
		{"allowed-list-item", resource("AWS::OpsWorks::Stack", `{"RdsDbInstances": [{"DbPassword": "`+secure+`", "DbUser": "`+secure+`"}],
			"CustomCookbooksSource": {"Password": "`+secure+`", "Username": "`+secure+`"}}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/CustomCookbooksSource/Username",
				"error:ssm-secure-placement:/Resources/R/Properties/RdsDbInstances/0/DbUser"}},
		{"amplify", resource("AWS::Amplify::App", `{"OauthToken": "`+secure+`"}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/OauthToken"}},
		{"if-branch", `{"Conditions": {"C": {"Fn::Equals": ["a", "b"]}}, "Resources": {"R": {"Type": "AWS::Amplify::App",
			"Properties": {"OauthToken": {"Fn::If": ["C", "x", "` + secure + `"]}}}}}`,
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/OauthToken/Fn::If/2"}},
		{"nested-list", resource("AWS::SNS::Topic", `{"Tags": [{"Key": "k", "Value": "`+secure+`"}]}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/Tags/0/Value"}},
		{"policy-document", resource("AWS::IAM::Policy", `{"PolicyDocument": {"Statement": [{"Resource": "`+secure+`"}]}}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/PolicyDocument/Statement/0/Resource"}},
		{"custom-resource", resource("Custom::Thing", `{"ServiceToken": "arn", "Secret": "`+secure+`"}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/Secret"}},
		{"functions-not-checked", resource("AWS::Amplify::App", `{"OauthToken": {"Fn::Sub": "`+secure+`-${AWS::Region}"},
			"AccessToken": {"Fn::Join": ["", ["`+secure+`"]]}}`), []string{}},
		{"other-references", resource("AWS::Amplify::App", `{"OauthToken": "{{resolve:ssm:Plain:1}}", "AccessToken": "{{resolve:secretsmanager:S}}"}`),
			[]string{}},
		{"last-reference", resource("AWS::Amplify::App", `{"OauthToken": "{{resolve:ssm:Plain:1}}-`+secure+`",
			"AccessToken": "`+secure+`-{{resolve:ssm:Plain:1}}"}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/OauthToken"}},
		{"multi-line", resource("AWS::Amplify::App", `{"OauthToken": "x\n`+secure+`", "AccessToken": "`+secure+`\n"}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/AccessToken"}},
		{"other-sections", `{"Description": "` + secure + `",
			"Parameters": {"P": {"Type": "String", "Default": "` + secure + `"}},
			"Mappings": {"M": {"a": {"b": "` + secure + `"}}},
			"Resources": {"R": {"Type": "AWS::RDS::DBInstance", "Metadata": {"MasterUserPassword": "` + secure + `"}}},
			"Outputs": {"O": {"Value": "` + secure + `"}}}`,
			[]string{"error:ssm-secure-placement:/Description",
				"error:ssm-secure-placement:/Mappings/M/a/b",
				"error:ssm-secure-placement:/Outputs/O/Value",
				"error:ssm-secure-placement:/Parameters/P/Default",
				"error:ssm-secure-placement:/Resources/R/Metadata/MasterUserPassword"}},
		{"no-type", `{"Resources": {"R": {"Properties": {"OauthToken": "` + secure + `"}}}}`, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary(check(t, tc.data)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestSecureStringMessage(t *testing.T) {
	ds := check(t, `{"Resources": {"AmplifyApp": {"Type": "AWS::Amplify::App",
		"Properties": {"OauthToken": "{{resolve:ssm-secure:SecureSecretString:1}}"}}}}`)
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics", len(ds))
	}
	want := "dynamic reference {{resolve:ssm-secure:SecureSecretString:1}} is in AWS::Amplify::App property OauthToken"
	if !strings.HasPrefix(ds[0].Message, want) {
		t.Fatalf("message %q", ds[0].Message)
	}
}
