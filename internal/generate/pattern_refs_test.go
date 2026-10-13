package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
)

func TestReferencesToNormalizedPatternProperties(t *testing.T) {
	for _, pattern := range []string{`(?=^x)x$`, `^\u0061/~$`} {
		t.Run(pattern, func(t *testing.T) {
			source := fixture(t)
			root := map[string]any{
				"typeName": "AWS::Test::Thing", "type": "object", "additionalProperties": false,
				"properties": map[string]any{"Value": map[string]any{"$ref": "#/definitions/Bag/patternProperties/" + pointer(pattern)}},
				"definitions": map[string]any{"Bag": map[string]any{
					"type": "object", "additionalProperties": false,
					"patternProperties": map[string]any{pattern: map[string]any{"type": "string", "enum": []any{"one", "two"}}},
				}},
			}
			source.files["resources/test.json"], _ = json.Marshal(root)
			out := filepath.Join(t.TempDir(), "generated")
			if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
				t.Fatal(err)
			}
			schema := build(t, out, "./resources/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
			validate(t, schema, `{Value: "one"}`, true)
			validate(t, schema, `{Value: {Ref: "Parameter"}}`, true)
			validate(t, schema, `{Value: "bad"}`, false)
			validate(t, schema, `{Value: {"Fn::Split": [",", "one,two"]}}`, false)
			literal := build(t, out, "./literal/htest").LookupPath(cue.MakePath(cue.Def("Properties")))
			validate(t, literal, `{Value: "one"}`, true)
			validate(t, literal, `{Value: {Ref: "Parameter"}}`, false)
		})
	}
}
