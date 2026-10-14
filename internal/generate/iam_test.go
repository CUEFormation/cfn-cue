package generate

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
)

func iamFixture() *Source {
	return &Source{Label: "iam-fixture", SHA256: "iam-fixture", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::IAM::Role":"role","AWS::Organizations::Policy":"org","AWS::KMS::Key":"kms"}`),
		"resources/role.json": []byte(`{"typeName":"AWS::IAM::Role","type":"object","additionalProperties":false,
			"properties":{"AssumeRolePolicyDocument":{"type":["object","string"]},"Policies":{"type":"array","items":{"$ref":"#/definitions/Policy"}}},
			"definitions":{"Policy":{"type":"object","additionalProperties":false,"properties":{"PolicyDocument":{"type":["object","string"]},"PolicyName":{"type":"string"}},"required":["PolicyDocument","PolicyName"]}},
			"required":["AssumeRolePolicyDocument"]}`),
		"resources/org.json": []byte(`{"typeName":"AWS::Organizations::Policy","type":"object","additionalProperties":false,
			"properties":{"Content":{"type":["object","string"]},"Name":{"type":"string"},"Type":{"type":"string","enum":["SERVICE_CONTROL_POLICY","RESOURCE_CONTROL_POLICY","TAG_POLICY"]}},
			"required":["Content","Name","Type"]}`),
		"resources/kms.json": []byte(`{"typeName":"AWS::KMS::Key","type":"object","additionalProperties":false,"properties":{"Description":{"type":"string"}}}`),
	}}
}

// TestIAMProfiles runs testdata/iam.cases against the generated iam package.
// A custom module path checks that the package's fn import is rebound.
func TestIAMProfiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(iamFixture(), Options{Out: out, Module: "example.com/cfn"}); e != nil {
		t.Fatal(e)
	}
	pkg := build(t, out, "./iam")
	f, e := os.Open("testdata/iam.cases")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || (parts[0] != "ok" && parts[0] != "bad") {
			t.Fatalf("testdata/iam.cases:%d: want ok|bad<TAB>#Definition<TAB>value", n)
		}
		def := pkg.LookupPath(cue.MakePath(cue.Def(strings.TrimPrefix(parts[1], "#"))))
		if !def.Exists() {
			t.Fatalf("testdata/iam.cases:%d: unknown definition %s", n, parts[1])
		}
		validate(t, def, parts[2], parts[0] == "ok")
	}
	if e = scanner.Err(); e != nil {
		t.Fatal(e)
	}
}

func TestIAMPlacement(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	m, e := Generate(iamFixture(), Options{Out: out})
	if e != nil {
		t.Fatal(e)
	}
	missing := false
	for _, d := range m.Diagnostics {
		missing = missing || d.Resource == "AWS::KMS::Key" && d.Keyword == "iamPolicy" && d.Path == "#/properties/KeyPolicy"
	}
	if !missing {
		t.Fatalf("missing placement diagnostic for AWS::KMS::Key: %v", m.Diagnostics)
	}
	types := build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("ResourceTypes")))
	role := types.LookupPath(cue.MakePath(cue.Str("AWS::IAM::Role")))
	org := types.LookupPath(cue.MakePath(cue.Str("AWS::Organizations::Policy")))
	trust := `{Statement:[{Effect:"Allow",Principal:{Service:"lambda.amazonaws.com"},Action:"sts:AssumeRole"}]}`
	validate(t, role, `{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:`+trust+`,Policies:[{PolicyName:"p",PolicyDocument:{Statement:[{Effect:"Allow",Action:"s3:GetObject",Resource:"*"}]}}]}}`, true)
	validate(t, role, `{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:{"Fn::Sub":"{}"}}}`, true)
	validate(t, role, `{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:{Statement:[{Effect:"Allow",Action:"sts:AssumeRole"}]}}}`, false)
	validate(t, role, `{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:`+trust+`,Policies:[{PolicyName:"p",PolicyDocument:{Statement:[{Effect:"Allow",Principal:"*",Action:"s3:GetObject",Resource:"*"}]}}]}}`, false)
	scp := `{Statement:[{Effect:"Deny",Action:"ec2:*",Resource:"*"}]}`
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:"SERVICE_CONTROL_POLICY",Content:`+scp+`}}`, true)
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:"RESOURCE_CONTROL_POLICY",Content:`+scp+`}}`, false)
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:"SERVICE_CONTROL_POLICY",Content:{Statement:[{Effect:"Deny",Principal:"*",Action:"ec2:*",Resource:"*"}]}}}`, false)
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:"TAG_POLICY",Content:{tags:{CostCenter:{tag_key:{"@@assign":"CostCenter"}}}}}}`, true)
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:{Ref:"PolicyType"},Content:{anything:true}}}`, true)
	validate(t, org, `{Type:"AWS::Organizations::Policy",Properties:{Name:"n",Type:"TAG_POLICY"}}`, false)
	literal := build(t, out, "./literal/hrole").LookupPath(cue.MakePath(cue.Def("Properties")))
	validate(t, literal, `{AssumeRolePolicyDocument:{Statement:[{Effect:"Allow",Action:"sts:AssumeRole"}]}}`, true)

	out = filepath.Join(t.TempDir(), "provider")
	m, e = Generate(iamFixture(), Options{Out: out, NoIAMPolicies: true})
	if e != nil {
		t.Fatal(e)
	}
	if m.IAMPolicies || len(m.Diagnostics) != 0 {
		t.Fatalf("NoIAMPolicies manifest: %v %v", m.IAMPolicies, m.Diagnostics)
	}
	role = build(t, out, "./regions/us-east-1").LookupPath(cue.MakePath(cue.Def("ResourceTypes"), cue.Str("AWS::IAM::Role")))
	validate(t, role, `{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:{Statement:[{Effect:"Allow",Action:"sts:AssumeRole"}]}}}`, true)
}
