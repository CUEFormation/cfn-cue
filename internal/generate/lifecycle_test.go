package generate

import (
	"encoding/json"
	"testing"
)

func TestLifecycle(t *testing.T) {
	for _, tc := range []struct {
		schema string
		want   string
	}{
		{`{"typeName":"AWS::Test::Every",
			"primaryIdentifier":["/properties/Name"],
			"additionalIdentifiers":[["/properties/Arn"],["/properties/Account","/properties/Id"]],
			"createOnlyProperties":["/properties/Name","/properties/Engine"],
			"conditionalCreateOnlyProperties":["/properties/Engine"],
			"writeOnlyProperties":["/properties/Password"],
			"readOnlyProperties":["/properties/Arn","/properties/Endpoint/Address"],
			"deprecatedProperties":["/properties/Old"],
			"replacementStrategy":"create_then_delete",
			"propertyTransform":{"/properties/Name":"$lowercase(Name)"},
			"tagging":{"taggable":true,"tagOnCreate":true,"tagUpdatable":true,"cloudFormationSystemTags":true,"tagProperty":"/properties/Tags","permissions":["test:TagResource"]},
			"handlers":{"create":{"permissions":["test:Create"],"timeoutInMinutes":60},"list":{"permissions":["test:List"]}}}`,
			`{"primaryIdentifier":["/properties/Name"],` +
				`"additionalIdentifiers":[["/properties/Arn"],["/properties/Account","/properties/Id"]],` +
				`"createOnlyProperties":["/properties/Name","/properties/Engine"],` +
				`"conditionalCreateOnlyProperties":["/properties/Engine"],` +
				`"writeOnlyProperties":["/properties/Password"],` +
				`"readOnlyProperties":["/properties/Arn","/properties/Endpoint/Address"],` +
				`"deprecatedProperties":["/properties/Old"],` +
				`"replacementStrategy":"create_then_delete",` +
				`"propertyTransform":{"/properties/Name":"$lowercase(Name)"},` +
				`"tagging":{"taggable":true,"tagOnCreate":true,"tagUpdatable":true,"cloudFormationSystemTags":true,"tagProperty":"/properties/Tags","permissions":["test:TagResource"]},` +
				`"handlers":{"create":{"permissions":["test:Create"],"timeoutInMinutes":60},"list":{"permissions":["test:List"]}}}`},
		{`{"typeName":"AWS::Test::None","properties":{"Name":{"type":"string"}}}`, `{}`},
		// handlerSchema is dropped.
		{`{"typeName":"AWS::Test::List","handlers":{"list":{"handlerSchema":{"properties":{"Name":{"type":"string"}}},"permissions":["test:List"]}}}`,
			`{"handlers":{"list":{"permissions":["test:List"]}}}`},
		// false survives; absent flags stay absent.
		{`{"typeName":"AWS::Test::Untagged","tagging":{"taggable":false}}`, `{"tagging":{"taggable":false}}`},
		{`{"typeName":"AWS::Test::Partial","tagging":{"taggable":true,"tagUpdatable":false,"tagProperty":"/properties/Tags"}}`,
			`{"tagging":{"taggable":true,"tagUpdatable":false,"tagProperty":"/properties/Tags"}}`},
		// Malformed values are skipped.
		{`{"typeName":"AWS::Test::Malformed","primaryIdentifier":["/properties/Name",1],"additionalIdentifiers":[[2],"x"],
			"replacementStrategy":3,"propertyTransform":{"/properties/A":4,"/properties/B":"B"},
			"tagging":{"taggable":"yes","permissions":"test:Tag"},"handlers":{"read":{"permissions":"test:Read","timeoutInMinutes":"5"},"list":"x"}}`,
			`{"primaryIdentifier":["/properties/Name"],"propertyTransform":{"/properties/B":"B"},"tagging":{},"handlers":{"read":{}}}`},
	} {
		var root map[string]any
		if err := json.Unmarshal([]byte(tc.schema), &root); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(Lifecycle(root))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("%s:\ngot  %s\nwant %s", root["typeName"], got, tc.want)
		}
	}
}
