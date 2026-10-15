package generate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
		// The survey's KMS disagreements used "aws/sns" and "testing".
		{"AWS::KMS::Key.Id", []string{"1234abcd-12ab-34cd-56ef-1234567890ab", "mrk-1234abcd12ab34cd56ef1234567890ab", "alias/aws/sns", "alias/My_Key-1"}, []string{"aws/sns", "testing", "1234ABCD-12AB-34CD-56EF-1234567890AB", "mrk-1234", "alias/", "alias/my:key"}},
		{"AWS::KMS::Alias.AliasName", []string{"alias/MyKey", "alias/aws/sns", "alias/a:b"}, []string{"MyKey", "aws/sns", "alias/", "alias/my key"}},
		{"AWS::EC2::Subnet.Id", []string{"subnet-1234abcd", "subnet-1234567890ABCDEF0"}, []string{"subnet-123", "subnet-1234567g", "sub-1234abcd"}},
		{"AWS::EC2::SecurityGroup.Name", []string{"My Group", "web_sg-1 (prod) #2", "a:b/c@d"}, []string{"", "caf\u00e9", "tab\tname"}},
		{"AWS::S3::Bucket.Name", []string{"abc", "my-bucket", "my.bucket.1", "a--b", strings.Repeat("a", 63)}, []string{"ab", strings.Repeat("a", 64), "My-Bucket", "-bucket", "bucket-", ".bucket", "bucket.", "my..bucket", "my.-bucket", "my-.bucket", "my_bucket", ""}},
	}
	for _, tt := range cases {
		t.Run(tt.format, func(t *testing.T) {
			c := &converter{resource: "AWS::Test::Thing"}
			m := map[string]any{}
			c.normalizeFormat(m, "#/properties/Identifier/format", tt.format)
			if len(c.diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %v", c.diagnostics)
			}
			// A value must match every pattern added for its format.
			var patterns []*regexp.Regexp
			for _, x := range m["_extra"].([]any) {
				patterns = append(patterns, regexp.MustCompile(x.(map[string]any)["pattern"].(string)))
			}
			matches := func(value string) bool {
				for _, re := range patterns {
					if !re.MatchString(value) {
						return false
					}
				}
				return true
			}
			for _, value := range tt.good {
				if !matches(value) || !matches(value+"\n") {
					t.Errorf("rejected valid identifier %q or its cfn-lint-compatible final newline", value)
				}
			}
			for _, value := range tt.bad {
				if matches(value) {
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

func TestCloudFormationAMIException(t *testing.T) {
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
		// fn.#DynamicReference admits dynamic references beside the
		// literal constraints, so the format pattern itself rejects them.
		for _, value := range []string{"{{resolve:ssm:/image:12}}", "{{resolve:secretsmanager:secret-id:SecretString:image}}"} {
			if re.MatchString(value) {
				t.Errorf("format pattern accepted dynamic reference %q: %+v", value, tt)
			}
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
	template := propertiesSchema(t, out, "./resources/htest")
	literal := propertiesSchema(t, out, "./literal/htest")
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

func TestDynamicReferencesSkipLiteralConstraints(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	// AmazonMQ's Password pattern rejects the colons in every dynamic reference.
	properties["Password"] = map[string]any{"type": "string", "pattern": "^[^,:=]+$", "maxLength": 12}
	properties["Enabled"] = map[string]any{"type": "boolean"}
	properties["Codes"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []any{"a", "b"}}}
	// Schemas without a type fall back to ValueExpression. QuickSight's
	// ApprovalPolicy Actions items, for example, are enum-only.
	properties["Actions"] = map[string]any{"type": "array", "items": map[string]any{"enum": []any{"read", "write"}}}
	properties["Vpc"] = map[string]any{"format": "AWS::EC2::VPC.Id"}
	properties["Either"] = map[string]any{"anyOf": []any{map[string]any{"enum": []any{"a"}}, map[string]any{"pattern": "^b$"}}}
	properties["Shape"] = map[string]any{"type": "object", "properties": map[string]any{"A": map[string]any{"type": "string"}}, "additionalProperties": false}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	template := propertiesSchema(t, out, "./resources/htest")
	literal := propertiesSchema(t, out, "./literal/htest")
	for _, data := range []string{
		`{Name:"one",Password:"{{resolve:secretsmanager:MySecret:SecretString:password}}"}`,
		`{Name:"one",Password:"{{resolve:ssm-secure:/db/password:1}}"}`,
		`{Name:"{{resolve:ssm:/thing/name}}",Count:"{{resolve:ssm:/thing/count}}",Enabled:"{{resolve:ssm:/thing/enabled}}"}`,
		`{Name:"one",Codes:["a","{{resolve:ssm:/code}}"]}`,
		`{Name:"one",Password:"prefix-{{resolve:ssm:/password}}-suffix"}`,
		`{Name:"one",Password:"{{resolve:ssm:/password}}\n"}`,
		`{Name:"one",Actions:["read","{{resolve:ssm:/action}}"]}`,
		`{Name:"one",Vpc:"{{resolve:ssm:/vpc}}",Either:"{{resolve:ssm:/either}}"}`,
	} {
		validate(t, template, data, true)
		validate(t, literal, data, false)
	}
	for _, data := range []string{
		`{Name:"one",Password:"{{resolve:unknown:secret}}"}`,
		`{Name:"one",Password:"{{resolve:ssm:}}"}`,
		`{Name:"one",Password:"line\n{{resolve:ssm:/db/password}}"}`,
		`{Name:"one",Node:"{{resolve:ssm:/node}}"}`,
		`{Name:"one",Names:"{{resolve:ssm:/names}}"}`,
		`{Name:"one",Shape:"{{resolve:ssm:/shape}}"}`,
		`{Name:"one",Password:"{{resolve:ssm:/password}}\n\n"}`,
		`{Name:"one",Actions:["delete"]}`,
	} {
		validate(t, template, data, false)
	}
}

func TestEmptyBucketNameException(t *testing.T) {
	for _, tt := range []struct {
		resource, path string
		empty          bool
	}{
		{"AWS::S3::Bucket", "#/properties/BucketName/format", true},
		{"AWS::S3::Bucket", "#/properties/Other/format", false},
		{"AWS::Logs::Destination", "#/properties/BucketName/format", false},
	} {
		c := &converter{resource: tt.resource}
		m := map[string]any{}
		c.normalizeFormat(m, tt.path, "AWS::S3::Bucket.Name")
		for _, x := range m["_extra"].([]any) {
			if regexp.MustCompile(x.(map[string]any)["pattern"].(string)).MatchString("") != tt.empty {
				t.Errorf("empty bucket name handling: %+v", tt)
			}
		}
	}
}

func TestListIdentifierFormatsUnenforced(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	// The list formats annotate arrays, which formats do not constrain.
	// Item formats, where present, are enforced on their own.
	properties["SecurityGroupIds"] = map[string]any{"type": "array", "format": "AWS::EC2::SecurityGroup.Ids", "items": map[string]any{"type": "string", "format": "AWS::EC2::SecurityGroup.Id"}}
	// EC2 Instance and AutoScaling LaunchConfiguration use this shape.
	properties["SecurityGroups"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "anyOf": []any{
		map[string]any{"format": "AWS::EC2::SecurityGroup.Ids"},
		map[string]any{"format": "AWS::EC2::SecurityGroup.Names"},
	}}
	// SpotFleet's .Ids list holds GroupIdentifier objects, not strings.
	properties["GroupSet"] = map[string]any{"type": "array", "format": "AWS::EC2::SecurityGroup.Ids", "items": map[string]any{
		"type": "object", "properties": map[string]any{"GroupId": map[string]any{"type": "string"}}, "additionalProperties": false,
	}}
	properties["Subnets"] = map[string]any{"type": "array", "format": "AWS::EC2::Subnet.Ids", "items": map[string]any{"type": "string"}}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	diagnosed := map[string]bool{}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "format" {
			diagnosed[strings.TrimPrefix(d.Detail, "format annotation not enforced: ")] = true
		}
	}
	for _, format := range []string{"AWS::EC2::SecurityGroup.Ids", "AWS::EC2::SecurityGroup.Names", "AWS::EC2::Subnet.Ids"} {
		if !diagnosed[format] {
			t.Errorf("unenforced list format %s is not reported: %v", format, manifest.Diagnostics)
		}
	}
	template := propertiesSchema(t, out, "./resources/htest")
	literal := propertiesSchema(t, out, "./literal/htest")
	for _, data := range []string{
		`{Name:"one",SecurityGroupIds:["sg-1234abcd","sg-1234567890abcdef0"]}`,
		`{Name:"one",SecurityGroups:["sg-1234abcd","default","My Group"]}`,
		`{Name:"one",GroupSet:[{GroupId:"sg-1234abcd"}]}`,
		`{Name:"one",Subnets:["subnet-1234abcd","not-a-subnet-id"]}`,
	} {
		validate(t, literal, data, true)
		validate(t, template, data, true)
	}
	for _, data := range []string{
		`{Name:"one",SecurityGroupIds:["sg-1234abcd","sg-test"]}`, // item format
		`{Name:"one",GroupSet:["sg-1234abcd"]}`,                   // item type
	} {
		validate(t, literal, data, false)
		validate(t, template, data, false)
	}
	for _, data := range []string{
		`{Name:"one",SecurityGroupIds:{Ref:"SecurityGroupIdsParameter"}}`,
		`{Name:"one",SecurityGroupIds:[{"Fn::GetAtt":["SecurityGroup","GroupId"]}]}`,
		`{Name:"one",Subnets:{"Fn::Split":[",",{"Fn::ImportValue":"Subnets"}]}}`,
	} {
		validate(t, template, data, true)
		validate(t, literal, data, false)
	}
}

func TestNetworkFormats(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	properties["Cidr"] = map[string]any{"type": "string", "format": "ipv4-network"}
	properties["Ipv6Cidr"] = map[string]any{"type": "string", "format": "ipv6-network"}
	properties["Untyped"] = map[string]any{"format": "ipv4-network"}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range manifest.Diagnostics {
		if d.Keyword == "format" {
			t.Fatalf("network format still diagnosed: %v", d)
		}
	}
	template := propertiesSchema(t, out, "./resources/htest")
	literal := propertiesSchema(t, out, "./literal/htest")
	for _, data := range []string{
		`{Name:"one",Cidr:"10.0.0.0/16"}`,
		`{Name:"one",Cidr:"0.0.0.0/0"}`,
		`{Name:"one",Cidr:"10.0.0.1/32"}`,
		`{Name:"one",Cidr:"10.0.0.1"}`, // Python reads a bare address as /32.
		// Python also accepts a contiguous dotted netmask or hostmask.
		`{Name:"one",Cidr:"10.0.0.0/255.0.0.0"}`,
		`{Name:"one",Cidr:"10.0.0.0/0.255.255.255"}`,
		`{Name:"one",Cidr:"0.0.0.0/0.0.0.0"}`,
		`{Name:"one",Cidr:"10.0.0.1/255.255.255.255"}`,
		`{Name:"one",Cidr:"192.168.1.0/0.0.0.255"}`,
		`{Name:"one",Cidr:"172.16.0.0/255.240.0.0"}`,
		`{Name:"one",Cidr:"10.0.0.0/0.0.0.1"}`,
		`{Name:"one",Cidr:"192.168.1.128/255.255.255.128"}`,
		`{Name:"one",Ipv6Cidr:"2001:db8::/32"}`,
		`{Name:"one",Ipv6Cidr:"2001:DB8::/32"}`,
		`{Name:"one",Ipv6Cidr:"2001:0db8:0000::/48"}`,
		`{Name:"one",Ipv6Cidr:"2001:db8::1"}`,
		`{Name:"one",Untyped:12}`,
	} {
		validate(t, literal, data, true)
		validate(t, template, data, true)
	}
	for _, data := range []string{
		`{Name:"one",Cidr:"192.168.1.0/0"}`, // host bits set, as in the survey
		`{Name:"one",Cidr:"10.0.1.0/16"}`,
		`{Name:"one",Cidr:"10.0.0.0/33"}`,
		`{Name:"one",Cidr:"010.0.0.0/8"}`,
		`{Name:"one",Cidr:"10.0.0.1/255.0.0.0"}`,      // host bits set
		`{Name:"one",Cidr:"10.0.0.1/0.0.0.0"}`,        // 0.0.0.0 is a /0 netmask
		`{Name:"one",Cidr:"192.168.1.0/0.0.1.255"}`,   // host bits set for /23
		`{Name:"one",Cidr:"10.0.0.0/255.0.255.0"}`,    // not contiguous
		`{Name:"one",Cidr:"10.0.0.0/254.0.0.1"}`,      // neither netmask nor hostmask
		`{Name:"one",Cidr:"10.0.0.0/255.00.0.0"}`,     // leading zero in the mask
		`{Name:"one",Cidr:"10.0.0.0/255.0.0.256"}`,    // octet out of range
		`{Name:"one",Cidr:"10.0.0.256/255.0.0.0"}`,    // address out of range
		`{Name:"one",Cidr:"010.0.0.0/255.0.0.0"}`,     // leading zero in the address
		`{Name:"one",Cidr:"10.0.0.0/255.0.0.0\n"}`,    // no final newline, as in Python
		`{Name:"one",Untyped:"10.0.0.1/255.255.0.0"}`, // format alone still checks strings
		`{Name:"one",Cidr:"2001:db8::/32"}`,
		`{Name:"one",Cidr:"not-a-cidr"}`,
		`{Name:"one",Ipv6Cidr:"2001:db8::1/32"}`,
		`{Name:"one",Ipv6Cidr:"10.0.0.0/8"}`,
		`{Name:"one",Untyped:"10.0.1.0/16"}`,
	} {
		validate(t, literal, data, false)
		validate(t, template, data, false)
	}
	for _, data := range []string{
		`{Name:"one",Cidr:{Ref:"VpcCidr"}}`,
		`{Name:"one",Cidr:{"Fn::Select":[0,{"Fn::Cidr":["10.0.0.0/16",4,8]}]}}`,
		`{Name:"one",Cidr:"{{resolve:ssm:/vpc/cidr}}"}`,
		`{Name:"one",Cidr:{"Fn::Join":["/",[{Ref:"Address"},"255.255.0.0"]]}}`,
		`{Name:"one",Cidr:{"Fn::Sub":"${Address}/255.255.0.0"}}`,
	} {
		validate(t, template, data, true)
		validate(t, literal, data, false)
	}
}

func TestIPv4NetworkMasks(t *testing.T) {
	masks := ipv4NetworkMasks()
	for mask, prefix := range map[string]string{
		"0.0.0.0": "0", "128.0.0.0": "1", "255.0.0.0": "8", "255.255.255.254": "31",
		"255.255.255.255": "32", "127.255.255.255": "1", "0.255.255.255": "8", "0.0.0.1": "31",
	} {
		if !strings.Contains(masks, fmt.Sprintf("%q: %q\n", mask, prefix)) {
			t.Errorf("mask %s should map to /%s:\n%s", mask, prefix, masks)
		}
	}
	// 33 netmasks and 33 hostmasks, two of which are spelled as netmasks.
	if n := strings.Count(masks, ":"); n != 64 {
		t.Fatalf("got %d masks, want 64", n)
	}
}
