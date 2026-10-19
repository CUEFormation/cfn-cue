package generate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAttributes(t *testing.T) {
	for _, tc := range []struct {
		schema string
		want   []string
	}{
		{`{"typeName":"AWS::Test::Thing","properties":{"Name":{"type":"string"},"Arn":{"type":"string"},"Endpoint":{"type":"object"}},
			"readOnlyProperties":["/properties/Arn","/properties/Endpoint/Address"]}`, []string{"Arn", "Endpoint.Address"}},
		{`{"typeName":"AWS::Test::Empty","properties":{"Name":{"type":"string"}}}`, []string{}},
		{`{"typeName":"AWS::CloudFormation::Stack","readOnlyProperties":["/properties/Id"]}`, []string{"Outputs.*"}},
		{`{"typeName":"AWS::CloudFormation::CustomResource","readOnlyProperties":[]}`, []string{"*"}},
		{`{"typeName":"AWS::AppMesh::Mesh","readOnlyProperties":["/properties/Arn"]}`, []string{"Arn", "MeshName"}},
		{`{"typeName":"AWS::ServiceCatalog::CloudFormationProvisionedProduct","readOnlyProperties":["/properties/Outputs","/properties/RecordId"]}`,
			[]string{"Outputs.*", "RecordId"}},
		// Every property path is an attribute, except arrays of objects.
		{`{"typeName":"AWS::SQS::Queue","definitions":{"Tag":{"type":"object","properties":{"Key":{"type":"string"}}},
			"Redrive":{"type":"object","properties":{"Target":{"type":"string"}}}},
			"properties":{"QueueName":{"type":"string"},"Tags":{"type":"array","items":{"type":"object"}},
			"Ids":{"type":"array","items":{"type":"string"}},"Redrive":{"$ref":"#/definitions/Redrive"}},
			"readOnlyProperties":["/properties/Arn"]}`, []string{"Ids", "QueueName", "Redrive.Target"}},
	} {
		var root map[string]any
		if err := json.Unmarshal([]byte(tc.schema), &root); err != nil {
			t.Fatal(err)
		}
		if got := Attributes(root); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", root["typeName"], got, tc.want)
		}
	}
}
