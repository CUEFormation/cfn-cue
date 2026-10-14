package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

func TestLocalReferenceIntrinsicLift(t *testing.T) {
	root := map[string]any{
		"definitions": map[string]any{
			"Node": map[string]any{
				"properties":           map[string]any{"a/b~c": map[string]any{"type": "string"}},
				"items":                []any{map[string]any{"type": "object"}},
				"additionalProperties": true,
				"contains":             map[string]any{"type": "string"},
				"allOf":                []any{map[string]any{"type": "object"}},
				"prefixItems":          []any{map[string]any{"type": "object"}},
				"const":                map[string]any{"properties": map[string]any{"Node": map[string]any{"type": "object"}}},
				"enum":                 []any{map[string]any{"properties": map[string]any{"Node": map[string]any{"type": "object"}}}},
				"definitions":          map[string]any{"Closed": map[string]any{"additionalProperties": false}},
				"anyOf":                []any{map[string]any{"type": "object"}},
				"not":                  map[string]any{"type": "object"},
				"patternProperties": map[string]any{
					"valid":  map[string]any{"type": "string"},
					"(?=x)x": map[string]any{"type": "string"},
					`\u0061`: map[string]any{"type": "string"},
				},
			},
			"Forbidden": false,
			"Anything":  true,
			"Alias":     map[string]any{"$ref": "#/definitions/Node"},
		},
	}
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"#/definitions/Node", true},
		{"#/definitions/Alias", true},
		{"#/definitions/Anything", true},
		{"#/definitions/Node/properties/a~1b~0c", true},
		{"#/definitions/Node/items/0", true},
		{"#/definitions/Node/additionalProperties", true},
		{"#/definitions/Node/contains", true},
		{"#/definitions/Node/anyOf/0", false},
		{"#/definitions/Node/allOf/0", false},
		{"#/definitions/Node/prefixItems/0", true},
		{"#/definitions/Node/const/properties/Node", false},
		{"#/definitions/Node/enum/0/properties/Node", false},
		{"#/definitions/Node/definitions/Closed/additionalProperties", false},
		{"#/definitions/Node/not", false},
		{"#/definitions/Node/patternProperties/valid", true},
		{"#/definitions/Node/patternProperties/(?=x)x", false},
		{`#/definitions/Node/patternProperties/\u0061`, false},
		{"#/definitions/Forbidden", false},
		{"#/definitions/Missing", false},
		{"#/definitions/Node/items/00", false},
		{"#/definitions/Node/items/-1", false},
		{"#", false},
		{"cue://intrinsics/ObjectExpression", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			if got := localReferenceIsLifted(map[string]any{"$ref": tc.ref}, root); got != tc.want {
				t.Fatalf("localReferenceIsLifted(%q)=%v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

// The EMR Applications regression is caused by lifting an object definition
// and lifting its reference again. This smaller recursive Node schema covers
// the same failure without requiring a downloaded provider schema.
func TestConditionalObjectThroughLocalReference(t *testing.T) {
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(fixture(t), Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Name:"one",Node:{"Fn::If":["Enabled",{Label:"a"},{Ref:"AWS::NoValue"}]}}`,
		`{Name:"one",Node:{Children:[{"Fn::If":["Enabled",{Label:"a",Children:[{Label:"b"}]},{Ref:"AWS::NoValue"}]}]}}`,
		`{Name:"one",Node:{"Fn::If":["Outer",{"Fn::If":["Inner",{Label:"a"},{Ref:"AWS::NoValue"}]},{Ref:"AWS::NoValue"}]}}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Name:"one",Node:{"Fn::If":["Enabled",[],{Ref:"AWS::NoValue"}]}}`,
		`{Name:"one",Node:{"Fn::If":["Enabled",{Label:"a"}]}}`,
		`{Name:"one",Node:{"Fn::If":["Enabled",{Label:"a"},{Ref:"AWS::NoValue"}],Ref:"P"}}`,
		`{Name:"one",Node:{"Fn::If":["Enabled",{"Fn::Bogus":"x"},{Ref:"AWS::NoValue"}]}}`,
	} {
		validate(t, schema, data, false)
	}
}

func TestConditionalObjectThroughReferenceChain(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	definitions := root["definitions"].(map[string]any)
	definitions["Alias"] = map[string]any{"$ref": "#/definitions/Node"}
	definitions["Any"] = true
	properties := root["properties"].(map[string]any)
	properties["Alias"] = map[string]any{"$ref": "#/definitions/Alias"}
	properties["Any"] = map[string]any{"$ref": "#/definitions/Any"}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	validate(t, schema, `{Name:"one",Alias:{"Fn::If":["Enabled",{Label:"a"},{Ref:"AWS::NoValue"}]},Any:{"Fn::If":["Enabled",{Nested:[1,"a",true]},{Ref:"AWS::NoValue"}]}}`, true)
	validate(t, schema, `{Name:"one",Alias:{"Fn::If":["Enabled",[],{Ref:"AWS::NoValue"}]}}`, false)
}

func TestIntrinsicConditionalQuotedScalars(t *testing.T) {
	source, err := render(Intrinsics(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, schema, data string
		valid              bool
	}{
		{"number", "#NumberExpression", `{"Fn::If":["Enabled","22",2]}`, true},
		{"numberNested", "#NumberExpression", `{"Fn::If":["Enabled",{"Fn::If":["Inner","1.5","2e1"]},{Ref:"AWS::NoValue"}]}`, true},
		{"boolean", "#BooleanExpression", `{"Fn::If":["Enabled","True","FALSE"]}`, true},
		{"numberInvalid", "#NumberExpression", `{"Fn::If":["Enabled","twenty-two",2]}`, false},
		{"booleanInvalid", "#BooleanExpression", `{"Fn::If":["Enabled","yes",false]}`, false},
		{"arity", "#NumberExpression", `{"Fn::If":["Enabled","22"]}`, false},
		{"stringNativeNumber", "#StringExpression", `{"Fn::If":["Enabled",22,"two"]}`, false},
		{"reservedFunction", "#NumberExpression", `{"Fn::If":["Enabled",{"Fn::Bogus":"22"},2]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := cuecontext.New().CompileString(string(source) + "\nvalue: " + tc.schema + " & " + tc.data)
			err := value.Err()
			if err == nil {
				err = value.LookupPath(cue.ParsePath("value")).Validate(cue.Concrete(true))
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v want=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
}
