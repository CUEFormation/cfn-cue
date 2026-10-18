package generate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPrimaryIdentifier(t *testing.T) {
	for _, tc := range []struct {
		schema string
		want   []string
	}{
		{`{"typeName":"AWS::Athena::WorkGroup","primaryIdentifier":["/properties/Name"]}`, []string{"Name"}},
		{`{"typeName":"AWS::Test::Pair","primaryIdentifier":["/properties/A","/properties/B"],"readOnlyProperties":["/properties/Arn"]}`,
			[]string{"A", "B"}},
		// A read-only part is assigned by CloudFormation, so nothing is compared.
		{`{"typeName":"AWS::EC2::VPC","primaryIdentifier":["/properties/VpcId"],"readOnlyProperties":["/properties/VpcId"]}`, nil},
		{`{"typeName":"AWS::Test::Mixed","primaryIdentifier":["/properties/A","/properties/Id"],"readOnlyProperties":["/properties/Id"]}`, nil},
		{`{"typeName":"AWS::Test::Nested","primaryIdentifier":["/properties/Config/Name"]}`, nil},
		{`{"typeName":"AWS::Test::None"}`, nil},
		{`{"typeName":"AWS::CodeBuild::Project","primaryIdentifier":["/properties/Id"],"readOnlyProperties":["/properties/Id"]}`, []string{"Name"}},
	} {
		var root map[string]any
		if err := json.Unmarshal([]byte(tc.schema), &root); err != nil {
			t.Fatal(err)
		}
		if got := PrimaryIdentifier(root); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", root["typeName"], got, tc.want)
		}
	}
}
