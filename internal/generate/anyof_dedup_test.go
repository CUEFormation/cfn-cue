package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDeduplicateNormalizedAnyOf(t *testing.T) {
	alternatives := []any{
		map[string]any{"type": "string", "minLength": 1},
		map[string]any{"minLength": 1, "type": "string"},
		map[string]any{},
		map[string]any{},
		map[string]any{"type": "string", "minLength": 2},
		map[string]any{"enum": []any{"one", "two"}},
		map[string]any{"enum": []any{"two", "one"}},
	}
	unique := deduplicateAnyOf(alternatives)
	if len(unique) != 5 {
		t.Fatalf("unique alternatives: %v", unique)
	}
	for i, original := range []int{0, 2, 4, 5, 6} {
		got, _ := json.Marshal(unique[i])
		want, _ := json.Marshal(alternatives[original])
		if string(got) != string(want) {
			t.Fatalf("branch order changed: got %s want %s", got, want)
		}
	}
}

func TestArrayWithDuplicateNormalizedAlternatives(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	properties["SecurityGroupIds"] = map[string]any{
		"type": "array",
		"anyOf": []any{
			map[string]any{"format": "AWS::EC2::SecurityGroup.Ids"},
			map[string]any{"format": "AWS::EC2::SecurityGroup.Names"},
		},
		"items": map[string]any{"type": "string"},
	}
	// Identical oneOf branches match twice. Deduplicating them would wrongly
	// turn an unsatisfiable exclusive alternative into an accepted value.
	properties["Exclusive"] = map[string]any{
		"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "string"}},
	}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	formatWarnings := 0
	for _, diagnostic := range manifest.Diagnostics {
		if diagnostic.Keyword == "format" {
			formatWarnings++
		}
	}
	if formatWarnings != 2 { // retain a diagnostic for each unsupported source format
		t.Fatalf("lost unsupported-format diagnostics: got %d want 2", formatWarnings)
	}
	template := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Name:"one",SecurityGroupIds:[{Ref:"SecurityGroup"}]}`,
		`{Name:"one",SecurityGroupIds:["sg-1234abcd"]}`,
		`{Name:"one",SecurityGroupIds:[]}`,
	} {
		validate(t, template, data, true)
	}
	for _, data := range []string{
		`{Name:"one",SecurityGroupIds:[12]}`,
		`{Name:"one",SecurityGroupIds:[{"Fn::Bogus":"x"}]}`,
		`{Name:"one",Exclusive:"matches-both"}`,
	} {
		validate(t, template, data, false)
	}
	literal := propertiesSchema(t, out, "./literal/htest")
	validate(t, literal, `{Name:"one",SecurityGroupIds:["sg-1234abcd"]}`, true)
	validate(t, literal, `{Name:"one",SecurityGroupIds:[{Ref:"SecurityGroup"}]}`, false)
	validate(t, literal, `{Name:"one",Exclusive:"matches-both"}`, false)
}
