package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
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
	schema := build(t, out, "./resources/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
	for _, data := range []string{
		`{Name: {"Fn::Sub": "${Parameter}"}}`,
		`{Name: {Ref: "Parameter"}, Count: {"Fn::If": ["Create", 2, 3]}}`,
		`{Count: "2", Extra: ["x", 3], Payload: {Nested: {Ref: "Parameter"}}}`,
		`{Bag: {Key: {"Fn::Sub": "${Parameter}"}}}`,
		`{Bag: {"Fn::If": ["Create", {Key: "value"}, {Ref: "Parameter"}]}}`,
		`{Limited: {"Fn::If": ["Create", "value", {"Fn::If": ["Create", 2, {Ref: "Parameter"}]}]}}`,
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
	literal := build(t, out, "./literal/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
	validate(t, literal, `{Name: "one", Extra: {"Fn::Sub": ["literal data"]}}`, true)
	validate(t, literal, `{Name: {"Fn::Sub": "${Parameter}"}}`, false)
}
