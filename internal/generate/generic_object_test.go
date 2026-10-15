package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestGenericObjectIntrinsicOverlap(t *testing.T) {
	source := fixture(t)
	root := map[string]any{
		"typeName": "AWS::Test::Thing", "type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"Name":    map[string]any{"type": "string"},
			"Count":   map[string]any{"type": "integer", "minimum": 1},
			"Bag":     map[string]any{"type": "object", "minProperties": 1},
			"Limited": map[string]any{"type": []any{"string", "integer"}},
		},
		"patternProperties": map[string]any{".*": map[string]any{"type": []any{"string", "integer", "number", "array", "object"}}},
	}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Name: {"Fn::Sub": "${Parameter}"}}`,
		`{Name: {Ref: "Parameter"}, Count: {"Fn::If": ["Create", 2, 3]}}`,
		`{Count: "2", Extra: ["x", 3], Payload: {Nested: {Ref: "Parameter"}}}`,
		`{Extra: 1.5}`,
		`{Other: true}`,
		`{Bag: {Key: {"Fn::Sub": "${Parameter}"}}}`,
		`{Bag: {"Fn::If": ["Create", {Key: "value"}, {Ref: "Parameter"}]}}`,
		`{Limited: {"Fn::If": ["Create", "value", {"Fn::If": ["Create", 2, {Ref: "Parameter"}]}]}}`,
		`{Name: 2, Limited: true}`, // string form of a number or boolean
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Name: {"Fn::Split": [",", "a,b"]}}`,
		`{Name: {"Fn::Sub": ["x"]}}`,
		`{Count: "0"}`,
		`{Extra: {"Fn::Sub": ["x"]}}`,
		`{Bag: {}}`,
		`{Limited: {"Fn::Split": [",", "a,b"]}}`,
		`{Limited: {"Fn::If": ["Create", true, "value"]}}`,
	} {
		validate(t, schema, data, false)
	}
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name: "one", Extra: {"Fn::Sub": ["literal data"]}}`, true)
	validate(t, literal, `{Name: {"Fn::Sub": "${Parameter}"}}`, false)
}

// An unanchored key pattern such as Stack Parameters' [a-zA-Z0-9]+ also
// matches "Fn::If". Those keys are reserved, so a conditional map is only
// an expression rather than both an expression and a parameter named Fn::If.
func TestKeyPatternReservesIntrinsicKeys(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	root["properties"].(map[string]any)["Parameters"] = map[string]any{
		"type": "object", "additionalProperties": false,
		"patternProperties": map[string]any{"[a-zA-Z0-9]+": map[string]any{"type": []any{"string", "number", "integer", "object", "array", "boolean"}}},
	}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Name: "one", Parameters: {One: "a", Two: 2, Three: true}}`,
		`{Name: "one", Parameters: {"Fn::If": ["Create", {One: "a"}, {"Fn::If": ["Create", {One: "c"}, {One: "e"}]}]}}`,
		`{Name: "one", Parameters: {"Fn::If": ["Create", {One: "a"}, {Ref: "AWS::NoValue"}]}}`,
		`{Name: "one", Parameters: {Ref: "Parameters"}}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Name: "one", Parameters: {"Fn::If": ["Create", {One: "a"}], Two: "b"}}`,
		`{Name: "one", Parameters: {"Fn::Sub": "x"}}`,
	} {
		validate(t, schema, data, false)
	}
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name: "one", Parameters: {Ref: "literal data"}}`, true)
}
