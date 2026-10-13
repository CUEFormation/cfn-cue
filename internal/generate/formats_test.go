package generate

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cuelang.org/go/cue"
)

func TestCloudFormationIdentifierFormats(t *testing.T) {
	// The malformed VPC/security-group/AMI literals reproduce the direct
	// format disagreements in the pinned cfn-lint corpus. IAM policy JSON
	// Resource validation is a separate semantic rule, not a Role.Arn format.
	cases := []struct {
		format string
		good   []string
		bad    []string
	}{
		{"AWS::EC2::VPC.Id", []string{"vpc-1234abcd", "vpc-1234567890ABCDEF0"}, []string{"vpc-1234567", "vpc-123456", "vpc-123456789", "vpc-1234567g", "prefix-vpc-12345678"}},
		{"AWS::EC2::SecurityGroup.Id", []string{"sg-1234abcd", "sg-1234567890ABCDEF0"}, []string{"sg-test", "sg-1234567", "sg-123456789", "sg-1234567g"}},
		{"AWS::EC2::Image.Id", []string{"ami-1234abcd", "ami-1234567890abcdef0", "ami-zzzzzzzz"}, []string{"ami-123456", "ami-1234567", "ami-123456789", "ami-1234ABCD", "resolve:ssm:/image"}},
		{"AWS::IAM::Role.Arn", []string{"arn:aws:iam::123456789012:role/MyRole", "arn:aws-us-gov:iam::123456789012:role/path/MyRole", "arn:aws:iam::١٢٣٤٥٦٧٨٩٠١٢:role/MyRole"}, []string{"SomeOptionalParameter", "arn:aws:iam::123:role/MyRole", "arn:aws:iam:us-east-1:123456789012:role/MyRole", "arn:aws:iam::123456789012:policy/MyRole", "arn:aws:iam::①①①①①①①①①①①①:role/MyRole"}},
		{"AWS::KMS::Key.Arn", []string{"arn:aws:kms:us-east-1:123456789012:key/1234", "arn:aws-cn:kms:cn-north-1:123456789012:alias/MyKey"}, []string{"alias/MyKey", "arn:aws:kms::123456789012:key/1234", "arn:aws:kms:us-east-1:123:key/1234", "arn:aws:kms:us-east-1:123456789012:key/"}},
		{"AWS::SNS::Topic.Arn", []string{"arn:aws:sns:us-east-1:123456789012:MyTopic", "arn:aws-cn:sns:cn-north-1:123456789012:MyTopic.fifo", "arn:aws:sns:us-east-1:１２３４５６７８９０１２:MyTopic"}, []string{"MyTopic", "arn:aws:sns:us-east-1:123456789012:", "arn:aws:sns:us-east-1:123:MyTopic"}},
		{"AWS::ACM::Certificate.Arn", []string{"arn:aws:acm:us-east-1:123456789012:certificate/1234", "arn:aws-us-gov:acm:us-gov-west-1:123456789012:certificate/1234"}, []string{"1234", "arn:aws:acm:us-east-1:123456789012:certificate/", "arn:aws:acm:us-east-1:123:certificate/1234"}},
		{"AWS::Lambda::Function.Arn", []string{"arn:aws:lambda:us-east-1:123456789012:function:MyFunction", "arn:aws:lambda:us-east-1:123456789012:function:MyFunction:live"}, []string{"MyFunction", "arn:aws:lambda:us-east-1:123456789012:function:", "arn:aws:lambda:us-east-1:123:function:MyFunction"}},
	}
	for _, tt := range cases {
		t.Run(tt.format, func(t *testing.T) {
			c := &converter{resource: "AWS::Test::Thing"}
			m := map[string]any{}
			c.normalizeFormat(m, "#/properties/Identifier/format", tt.format)
			if len(c.diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %v", c.diagnostics)
			}
			a := m["_extra"].([]any)
			re := regexp.MustCompile(a[0].(map[string]any)["pattern"].(string))
			for _, value := range tt.good {
				if !re.MatchString(value) || !re.MatchString(value+"\n") {
					t.Errorf("rejected valid identifier %q or its cfn-lint-compatible final newline", value)
				}
			}
			for _, value := range tt.bad {
				if re.MatchString(value) {
					t.Errorf("accepted malformed identifier %q", value)
				}
			}
		})
	}
}

func TestCloudFormationFormatDiagnostics(t *testing.T) {
	c := &converter{resource: "AWS::Test::Thing"}
	for _, value := range []any{"new-format", "AWS::SQS::Queue.Arn", 12} {
		m := map[string]any{}
		c.normalizeFormat(m, "#/properties/Identifier/format", value)
		if len(m) != 0 {
			t.Fatalf("unknown format gained a constraint: %v", m)
		}
	}
	if len(c.diagnostics) != 3 {
		t.Fatalf("diagnostics: %v", c.diagnostics)
	}
	for _, d := range c.diagnostics {
		if d.Keyword != "format" || !strings.HasPrefix(d.Detail, "format annotation not enforced:") {
			t.Fatalf("unexpected diagnostic: %v", d)
		}
	}
	for _, value := range []string{"date-time", "date", "uri", "uri-reference", "regex"} {
		m := map[string]any{}
		c.normalizeFormat(m, "#/properties/Identifier/format", value)
		if m["format"] != value {
			t.Fatalf("lost standard format %s", value)
		}
	}
}

func TestCloudFormationAMIAndDynamicReferenceExceptions(t *testing.T) {
	for _, tt := range []struct {
		resource string
		path     string
		template bool
		ssmAMI   bool
	}{
		{"AWS::EC2::LaunchTemplate", "#/definitions/LaunchTemplateData/properties/ImageId/format", true, true},
		{"AWS::EC2::LaunchTemplate", "#/definitions/Other/properties/ImageId/format", true, false},
		{"AWS::EC2::Instance", "#/properties/ImageId/format", true, false},
		{"AWS::EC2::Instance", "#/properties/ImageId/format", false, false},
	} {
		c := &converter{resource: tt.resource, template: tt.template}
		m := map[string]any{}
		c.normalizeFormat(m, tt.path, "AWS::EC2::Image.Id")
		pattern := m["_extra"].([]any)[0].(map[string]any)["pattern"].(string)
		re := regexp.MustCompile(pattern)
		if re.MatchString("resolve:ssm:/image:12") != tt.ssmAMI {
			t.Fatalf("LaunchTemplate exception mismatch: %+v", tt)
		}
		for _, value := range []string{"{{resolve:ssm:/image:12}}", "{{resolve:secretsmanager:secret-id:SecretString:image}}"} {
			if re.MatchString(value) != tt.template {
				t.Errorf("dynamic reference handling %q: %+v", value, tt)
			}
		}
		if re.MatchString("{{resolve:unknown:/image}}") {
			t.Errorf("accepted unsupported resolver: %+v", tt)
		}
	}
}

func TestGeneratedCloudFormationFormatConstraints(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	properties["VpcId"] = map[string]any{"type": "string", "format": "AWS::EC2::VPC.Id", "pattern": "^vpc-aaaaaaaa$"}
	properties["SecurityGroupIds"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "AWS::EC2::SecurityGroup.Id"}}
	properties["ImageId"] = map[string]any{"type": "string", "format": "AWS::EC2::Image.Id"}
	properties["RoleArn"] = map[string]any{"type": "string", "format": "AWS::IAM::Role.Arn"}
	properties["UnionIdentifier"] = map[string]any{"type": "string", "anyOf": []any{
		map[string]any{"format": "AWS::EC2::VPC.Id"},
		map[string]any{"format": "AWS::EC2::SecurityGroup.Id"},
	}}
	// A format alone restricts only strings; it must not silently add a type.
	properties["Untyped"] = map[string]any{"format": "AWS::EC2::VPC.Id"}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "format" {
			t.Fatalf("supported formats still diagnosed: %v", d)
		}
	}
	template := build(t, out, "./resources/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
	literal := build(t, out, "./literal/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
	for _, data := range []string{
		`{Name:"one",VpcId:"vpc-aaaaaaaa",SecurityGroupIds:["sg-1234abcd"],ImageId:"ami-1234abcd",RoleArn:"arn:aws:iam::123456789012:role/MyRole",Untyped:12}`,
		`{Name:"one",Untyped:false}`,
		`{Name:"one",RoleArn:"arn:aws:iam::١٢٣٤٥٦٧٨٩٠١٢:role/MyRole",UnionIdentifier:"vpc-1234abcd"}`,
		`{Name:"one",UnionIdentifier:"sg-1234abcd"}`,
	} {
		validate(t, literal, data, true)
		validate(t, template, data, true)
	}
	for _, data := range []string{
		`{Name:"one",VpcId:"vpc-1234567"}`,
		`{Name:"one",VpcId:"vpc-bbbbbbbb"}`, // existing pattern still enforced
		`{Name:"one",SecurityGroupIds:["sg-test"]}`,
		`{Name:"one",ImageId:"ami-123456"}`,
		`{Name:"one",RoleArn:"SomeOptionalParameter"}`,
		`{Name:"one",Untyped:"vpc-bad"}`,
		`{Name:"one",RoleArn:"arn:aws:iam::①①①①①①①①①①①①:role/MyRole"}`,
		`{Name:"one",UnionIdentifier:"ami-1234abcd"}`,
	} {
		validate(t, literal, data, false)
		validate(t, template, data, false)
	}
	for _, data := range []string{
		`{Name:"one",RoleArn:{Ref:"RoleParameter"}}`,
		`{Name:"one",ImageId:{"Fn::GetAtt":["ImageResource","ImageId"]}}`,
		`{Name:"one",RoleArn:"{{resolve:ssm:/role-arn}}"}`,
	} {
		validate(t, template, data, true)
		validate(t, literal, data, false)
	}
}
