package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"cuelang.org/go/cue"

	"github.com/cueformation/cfn-cue/internal/bundle"
)

// Run after generating a real bundle with CFN_CUE_INTEGRATION=1 go test
// ./internal/generate -run TestCurrentResources -timeout 3m.
func TestCurrentResources(t *testing.T) {
	if os.Getenv("CFN_CUE_INTEGRATION") != "1" {
		t.Skip("set CFN_CUE_INTEGRATION=1 to check the generated upstream bundle")
	}
	dir, e := filepath.Abs("../../generated")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m bundle.Manifest
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	t.Logf("checking %d regions and %d shared schemas", len(m.Regions), len(m.Schemas))
	if m.Format != bundle.ManifestFormat {
		t.Errorf("manifest format %d, want %d", m.Format, bundle.ManifestFormat)
	}
	index, e := bundle.ReadRegionIndex(dir, "us-east-1")
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(m.RegionTypes("us-east-1"), index.Resources) {
		t.Error("manifest types differ from the us-east-1 region index")
	}
	hash, _ := m.SchemaHash("AWS::RDS::DBInstance", "us-east-1")
	db, ok := m.Schemas[hash]
	if !ok {
		t.Fatal("manifest has no us-east-1 AWS::RDS::DBInstance schema")
	}
	if len(db.PrimaryIdentifier) == 0 || len(db.CreateOnlyProperties) == 0 || len(db.ConditionalCreateOnlyProperties) == 0 || len(db.WriteOnlyProperties) == 0 ||
		len(db.ReadOnlyProperties) == 0 || len(db.DeprecatedProperties) == 0 || len(db.PropertyTransform) == 0 || db.Tagging == nil {
		t.Errorf("AWS::RDS::DBInstance lifecycle incomplete: %+v", db.Lifecycle)
	}
	for _, name := range []string{"create", "read", "update", "delete", "list"} {
		if len(db.Handlers[name].Permissions) == 0 {
			t.Errorf("AWS::RDS::DBInstance has no %s handler permissions", name)
		}
	}
	if bytes.Contains(b, []byte(`"handlerSchema"`)) {
		t.Error("manifest contains handlerSchema")
	}
	source, e := OpenSource(context.Background(), "../../snapshots/schemas-2026-10-12.zip")
	if e != nil {
		t.Fatal(e)
	}
	checkDir := dir
	if os.Getenv("CFN_CUE_FULL_REGION") != "1" {
		checkDir = filepath.Join(t.TempDir(), "filtered")
		if _, e = Generate(source, Options{Out: checkDir, Regions: []string{"us-east-1"}, Resources: []string{"AWS::S3::Bucket", "AWS::IAM::Role"}}); e != nil {
			t.Fatal(e)
		}
	}
	v := build(t, checkDir, "./regions/us-east-1")
	s := v.LookupPath(cue.MakePath(cue.Def("Template")))
	validate(t, s, `{AWSTemplateFormatVersion:"2010-09-09",Parameters:{Name:{Type:"String"}},Resources:{Bucket:{Type:"AWS::S3::Bucket",Properties:{BucketName:{"Fn::Sub":"${Name}-bucket"},Tags:[{Key:"Name",Value:{Ref:"Name"}}]}},Role:{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:{Version:"2012-10-17",Statement:[{Effect:"Allow",Principal:{Service:"lambda.amazonaws.com"},Action:"sts:AssumeRole",Condition:{StringEquals:{"sts:ExternalId":"test"}}}]}}}},Outputs:{BucketArn:{Value:{"Fn::GetAtt":["Bucket","Arn"]}}}}`, true)
	validate(t, s, `{Resources:{Bucket:{Type:"AWS::S3::Bucket",Properties:{AccessControl:"DefinitelyNotAnACL"}}}}`, false)
	validate(t, s, `{Resources:{Bucket:{Type:"AWS::S3::Bucket",Properties:{Arn:"read-only"}}}}`, false)
	validate(t, s, `{Resources:{Role:{Type:"AWS::IAM::Role",Properties:{RoleName:"missing-assume-role-policy"}}}}`, false)
	validate(t, s, `{Resources:{Role:{Type:"AWS::IAM::Role",Properties:{AssumeRolePolicyDocument:{Statement:[{Effect:"Allow",Principal:{Service:"lambda.amazonaws.com"},Action:"sts:AssumeRole",Resource:"*"}]}}}}}`, false)
	validate(t, s, `{Resources:{Bucket:{Type:"AWS::DoesNotExist::Thing"}}}`, false)
	bucket := func(properties string) string {
		return `{Conditions:{C:true,D:true,E:true},Resources:{Bucket:{Type:"AWS::S3::Bucket",Properties:` + properties + `}}}`
	}
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{BucketName:"one-bucket"},{BucketName:"two-bucket"}]},{}]}`), true)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{"Fn::If":["E",{},{Ref:"AWS::NoValue"}]},{BucketName:"two-bucket"}]},{Ref:"AWS::NoValue"}]}`), true)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{AccessControl:"DefinitelyNotAnACL"},{}]},{}]}`), false)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{Arn:"read-only"},{}]},{}]}`), false)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{}]},{}]}`), false)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::If":["D",{},{}],BucketName:"x"},{}]}`), false)
	validate(t, s, bucket(`{"Fn::If":["C",{"Fn::Bogus":["D",{},{}]},{}]}`), false)
	// The per-type package re-exports the same schema without loading a region.
	perType := build(t, checkDir, "./AWS/S3/Bucket")
	r := perType.LookupPath(cue.MakePath(cue.Def("Resource")))
	validate(t, r, `{Type:"AWS::S3::Bucket",Properties:{BucketName:{"Fn::Sub":"${Name}-bucket"},Tags:[{Key:"Name",Value:{Ref:"Name"}}]}}`, true)
	validate(t, r, `{Type:"AWS::S3::Bucket",Properties:{AccessControl:"DefinitelyNotAnACL"}}`, false)
	validate(t, perType.LookupPath(cue.MakePath(cue.Def("Tag"))), `{Key:"Name",Value:"v"}`, true)
}
