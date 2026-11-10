package generate

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 9223372036854775807 is the largest int64 and 9007199254740993 is 2^53+1,
// the smallest positive integer a float64 cannot hold. Limit sits in a
// definition with a read-only sibling, so the resource schema copies it.
const numberPrecisionSchema = `{
	"typeName": "AWS::Test::Thing", "type": "object", "additionalProperties": false,
	"properties": {
		"Max": {"type": "integer", "minimum": -9223372036854775808, "maximum": 9223372036854775807},
		"Above": {"type": "integer", "minimum": 9007199254740993},
		"Between": {"type": "integer", "exclusiveMinimum": 9007199254740993, "exclusiveMaximum": 9007199254740995},
		"Step": {"type": "integer", "multipleOf": 9007199254740993},
		"Choice": {"type": "integer", "enum": [9007199254740993, 9223372036854775807]},
		"Fixed": {"type": "integer", "const": 9007199254740993, "default": 9007199254740993},
		"Text": {"type": "string", "minLength": 1, "maxLength": 9007199254740993},
		"List": {"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 9007199254740993},
		"Decimal": {"type": "number", "maximum": 0.1000000000000000055511151231257827},
		"Ordinary": {"type": "number", "minimum": 1.0, "maximum": 1e2, "multipleOf": 0.5},
		"Node": {"$ref": "#/definitions/Node"}
	},
	"readOnlyProperties": ["/properties/Node/State"],
	"definitions": {"Node": {"type": "object", "additionalProperties": false, "properties": {
		"State": {"type": "string"},
		"Limit": {"type": "integer", "maximum": 9223372036854775807}
	}}}
}`

func TestSchemaNumbersAreExact(t *testing.T) {
	source := fixture(t)
	source.files["resources/test.json"] = []byte(numberPrecisionSchema)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"literal", "resources"} {
		b, err := os.ReadFile(filepath.Join(out, pkg, "htest", "schema.cue"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, want := range []string{
			"strings.MaxRunes(9007199254740993)",
			"list.MaxItems(9007199254740993)",
			"<=0.1000000000000000055511151231257827",
			// Numbers a float64 holds as written keep their short form.
			"strings.MinRunes(1)",
			"list.MinItems(1)",
			"math.MultipleOf(0.5) & <=100 & >=1",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s schema lacks %s:\n%s", pkg, want, text)
			}
		}
		schema := propertiesSchema(t, out, "./"+pkg+"/htest")
		for _, data := range []string{
			`{Max: 9223372036854775807}`,
			`{Max: -9223372036854775808}`,
			`{Above: 9007199254740993}`,
			`{Between: 9007199254740994}`,
			`{Step: 9007199254740993}`,
			`{Choice: 9007199254740993}`,
			`{Choice: 9223372036854775807}`,
			`{Fixed: 9007199254740993}`,
			`{Decimal: 0.1000000000000000055511151231257827}`,
			`{Ordinary: 1}`,
			`{Ordinary: 100}`,
			`{Node: {Limit: 9223372036854775807}}`,
		} {
			validate(t, schema, data, true)
		}
		for _, data := range []string{
			`{Max: 9223372036854775808}`,
			`{Max: -9223372036854775809}`,
			`{Above: 9007199254740992}`,
			`{Between: 9007199254740993}`,
			`{Between: 9007199254740995}`,
			`{Step: 9007199254740992}`,
			`{Choice: 9007199254740992}`,
			`{Choice: 9223372036854775808}`,
			`{Fixed: 9007199254740992}`,
			`{Decimal: 0.10000000000000001}`,
			`{Ordinary: 0.5}`,
			`{Ordinary: 100.5}`,
			`{Node: {Limit: 9223372036854775808}}`,
		} {
			validate(t, schema, data, false)
		}
	}
	// A quoted integer is checked against the same bounds.
	resource := propertiesSchema(t, out, "./resources/htest")
	validate(t, resource, `{Max: "9223372036854775807", Node: {Limit: "9223372036854775807"}}`, true)
	validate(t, resource, `{Max: "9223372036854775808"}`, false)
	validate(t, resource, `{Node: {Limit: "9223372036854775808"}}`, false)
}

// The cfn-lint rule compiler reads numbers as float64, as cfn-lint reads
// fractions. A vendored file with a number float64 cannot hold as written
// would be rounded, so that file needs exact handling before it is used.
func TestExtensionNumbersFitFloat64(t *testing.T) {
	var inexact func(v any) bool
	inexact = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for _, child := range x {
				if inexact(child) {
					return true
				}
			}
		case []any:
			for _, child := range x {
				if inexact(child) {
					return true
				}
			}
		case json.Number:
			return true
		}
		return false
	}
	files := 0
	err := fs.WalkDir(extensionFiles, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		files++
		b, err := extensionFiles.ReadFile(path)
		if err != nil {
			return err
		}
		v, err := decodeJSON(b)
		if err != nil {
			return err
		}
		if inexact(v) {
			t.Errorf("%s has a number that float64 would round", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no vendored extension files were read")
	}
}
