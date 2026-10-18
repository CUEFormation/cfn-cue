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
		{"allowed", `{"Resources": {
			"MicrosoftAD": {"Type": "AWS::DirectoryService::MicrosoftAD", "Properties": {"Password": "` + secure + `"}},
			"SimpleAD": {"Type": "AWS::DirectoryService::SimpleAD", "Properties": {"Password": "` + secure + `"}},
			"Redis": {"Type": "AWS::ElastiCache::ReplicationGroup", "Properties": {"AuthToken": "` + secure + `"}},
			"User": {"Type": "AWS::IAM::User", "Properties": {"LoginProfile": {"Password": "` + secure + `"}}},
			"Firehose": {"Type": "AWS::KinesisFirehose::DeliveryStream", "Properties": {"RedshiftDestinationConfiguration": {"Password": "` + secure + `"}}},
			"App": {"Type": "AWS::OpsWorks::App", "Properties": {"AppSource": {"Password": "` + secure + `"}}},
			"Cluster": {"Type": "AWS::RDS::DBCluster", "Properties": {"MasterUserPassword": "` + secure + `"}},
			"Instance": {"Type": "AWS::RDS::DBInstance", "Properties": {"MasterUserPassword": "` + secure + `"}},
			"Redshift": {"Type": "AWS::Redshift::Cluster", "Properties": {"MasterUserPassword": "` + secure + `"}}}}`, []string{}},
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
		{"if-malformed", `{"Conditions": {"C": {"Fn::Equals": ["a", "b"]}}, "Resources": {"R": {"Type": "AWS::Amplify::App", "Properties": {
			"OauthToken": {"Fn::If": ["C", "` + secure + `"]},
			"AccessToken": {"Fn::If": ["Undefined", "x", "` + secure + `"]},
			"Name": {"Fn::If": ["` + secure + `", "x", "y"]},
			"Description": {"Fn::If": "` + secure + `"},
			"Repository": {"Fn::If": {"Key": "` + secure + `"}}}}}}`,
			[]string{"error:condition-undefined:/Resources/R/Properties/AccessToken/Fn::If/0",
				"error:ssm-secure-placement:/Resources/R/Properties/Description/Fn::If",
				"error:condition-undefined:/Resources/R/Properties/Name/Fn::If/0",
				"error:ssm-secure-placement:/Resources/R/Properties/Name/Fn::If/0"}},
		{"function-name-on-path", `{"Resources": {
			"Ref": {"Type": "AWS::S3::Bucket", "Properties": {"BucketName": "` + secure + `"}},
			"R": {"Type": "AWS::S3::Bucket", "Metadata": {"Fn::Sub": "` + secure + `", "Other": "x"}}}}`, []string{}},
		{"condition-function", `{"Conditions": {"C": {"Fn::Equals": ["` + secure + `", "x"]}}, "Resources": {"R": {"Type": "AWS::S3::Bucket"}}}`,
			[]string{"error:ssm-secure-placement:/Conditions/C/Fn::Equals/0"}},
		{"incomplete-reference", resource("AWS::Amplify::App", `{"OauthToken": "{{resolve:ssm-secure}}", "AccessToken": "{{resolve:ssm-secure:}}"}`),
			[]string{"error:ssm-secure-placement:/Resources/R/Properties/AccessToken"}},
		{"template-metadata", `{"Metadata": {"Note": "` + secure + `"}, "Resources": {"R": {"Type": "AWS::S3::Bucket"}}}`, []string{}},
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
