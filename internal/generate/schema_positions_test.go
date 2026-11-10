package generate

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"cuelang.org/go/cue"
)

func generateOne(t *testing.T, root string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(&Source{Label: "data", SHA256: "data", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::Test::Data":"data"}`),
		"resources/data.json":      []byte(root),
	}}, Options{Out: out, Regions: []string{"us-east-1"}}); e != nil {
		t.Fatal(e)
	}
	return out
}

// TestReferenceKeysInData covers constants whose data has keys named like
// schema keywords. Read-only removal used to rewrite a "$ref" inside const and
// enum values as if it were a reference, so the template schema rejected the
// constant the provider published. A "$ref" inside examples also kept a
// definition that only a read-only property uses.
func TestReferenceKeysInData(t *testing.T) {
	out := generateOne(t, `{"typeName":"AWS::Test::Data","type":"object","additionalProperties":false,
		"properties":{
			"Name":{"type":"string"},
			"Status":{"type":"string"},
			"Id":{"$ref":"#/definitions/Orphan"},
			"Pointer":{"type":"object","const":{"$ref":"#/properties/Name"}},
			"Choice":{"enum":["plain",{"$ref":"#/properties/Status","properties":{"Name":{"$ref":"#/properties/Name"}},"items":[{"$ref":"#/definitions/Orphan"}]}]},
			"Keep":{"type":"string","const":"x","default":{"$ref":"#/properties/Name"},"examples":[{"$ref":"#/$defs/Orphan"}]}
		},
		"readOnlyProperties":["/properties/Status","/properties/Id"],
		"definitions":{"Orphan":{"type":"string"}}}`)
	for _, pkg := range []string{"./resources/hdata", "./literal/hdata"} {
		t.Run(pkg, func(t *testing.T) {
			schema := propertiesSchema(t, out, pkg)
			validate(t, schema, `{Pointer: {"$ref": "#/properties/Name"}}`, true)
			validate(t, schema, `{Pointer: {"$ref": "#/properties/Other"}}`, false)
			validate(t, schema, `{Choice: "plain"}`, true)
			validate(t, schema, `{Choice: {"$ref": "#/properties/Status", properties: {Name: {"$ref": "#/properties/Name"}}, items: [{"$ref": "#/definitions/Orphan"}]}}`, true)
			validate(t, schema, `{Choice: {"$ref": "#/properties/Status", properties: {Name: {"$ref": "#/properties/Other"}}, items: [{"$ref": "#/definitions/Orphan"}]}}`, false)
			validate(t, schema, `{Keep: "x"}`, true)
		})
	}
	if build(t, out, "./resources/hdata").LookupPath(cue.MakePath(cue.Def("Orphan"))).Exists() {
		t.Error("resources #Orphan kept by a $ref key inside examples data")
	}
	if !build(t, out, "./literal/hdata").LookupPath(cue.MakePath(cue.Def("Orphan"))).Exists() {
		t.Error("literal #Orphan missing")
	}
}

// TestPropertyNamedLikeKeyword covers the opposite confusion: the keys of a
// properties object are names. Read-only removal used to skip a property
// called "definitions", leaving its reference to a removed read-only property
// dangling, and generation failed.
func TestPropertyNamedLikeKeyword(t *testing.T) {
	out := generateOne(t, `{"typeName":"AWS::Test::Data","type":"object","additionalProperties":false,
		"properties":{
			"Status":{"type":"string","enum":["on","off"]},
			"definitions":{"type":"object","additionalProperties":false,"properties":{"Copy":{"$ref":"#/properties/Status"}}},
			"enum":{"type":"object","additionalProperties":false,"properties":{"Copy":{"$ref":"#/properties/Status"}}}
		},
		"readOnlyProperties":["/properties/Status"]}`)
	schema := propertiesSchema(t, out, "./resources/hdata")
	validate(t, schema, `{definitions: {Copy: "on"}, enum: {Copy: "off"}}`, true)
	validate(t, schema, `{definitions: {Copy: "bad"}}`, false)
	validate(t, schema, `{enum: {Copy: "bad"}}`, false)
	validate(t, schema, `{Status: "on"}`, false)
}

// TestSchemaPositionsAgree checks the keyword lists in schema_walk.go against
// the two traversals that keep their own per-keyword switch, so the three
// cannot drift apart: reference resolution and input validation.
func TestSchemaPositionsAgree(t *testing.T) {
	child := map[string]any{"title": "child"}
	visited := func(root map[string]any) bool {
		found := false
		walkSchemaObjects(root, func(s map[string]any) { found = found || s["title"] == "child" })
		return found
	}
	check := func(keyword string, value, malformed any, ref string) {
		t.Helper()
		root := map[string]any{keyword: value}
		if keyword == "additionalItems" {
			root["items"] = []any{}
		}
		if !visited(root) {
			t.Errorf("%s: walkSchemaObjects did not visit the child", keyword)
		}
		if target, ok := localSchemaReference(root, ref); !ok || !reflect.DeepEqual(target.value, child) {
			t.Errorf("%s: localSchemaReference(%s) did not resolve the child", keyword, ref)
		}
		root[keyword] = malformed
		if validateSchema(root, "#") == nil {
			t.Errorf("%s: validateSchema accepted a number in a schema position", keyword)
		}
	}
	for _, keyword := range schemaMapKeywords {
		check(keyword, map[string]any{"a": child}, map[string]any{"a": 7.0}, "#/"+keyword+"/a")
	}
	for _, keyword := range append([]string{"items"}, schemaListKeywords...) {
		check(keyword, []any{child}, []any{7.0}, "#/"+keyword+"/0")
	}
	for _, keyword := range schemaValueKeywords {
		check(keyword, child, 7.0, "#/"+keyword)
	}
	for _, keyword := range append(append([]string{}, standard...), annotations...) {
		if holdsSchemas(keyword) {
			continue
		}
		for _, value := range []any{child, []any{child}, map[string]any{"a": child}} {
			root := map[string]any{keyword: value}
			if visited(root) {
				t.Errorf("%s: walkSchemaObjects entered a data position", keyword)
			}
			for _, ref := range []string{"#/" + keyword, "#/" + keyword + "/0", "#/" + keyword + "/a"} {
				if _, ok := localSchemaReference(root, ref); ok {
					t.Errorf("%s: localSchemaReference resolved %s through a data position", keyword, ref)
				}
			}
		}
	}
}

// TestExtensionDataPositions checks the walkers that prepare vendored cfn-lint
// rules. None of the vendored rules has such data today.
func TestExtensionDataPositions(t *testing.T) {
	parse := func(s string) map[string]any {
		t.Helper()
		var v map[string]any
		if e := json.Unmarshal([]byte(s), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	t.Run("moveDefinitions", func(t *testing.T) {
		root := map[string]any{}
		moveDefinitions(root, parse(`{"definitions":{"A":{"const":{"$ref":"#/definitions/B"}},"B":{"type":"string"}},
			"properties":{"definitions":{"$ref":"#/definitions/A"},"X":{"enum":[{"$ref":"#/definitions/A"}]}}}`), "P")
		want := parse(`{"definitions":{"PA":{"const":{"$ref":"#/definitions/B"}},"PB":{"type":"string"}}}`)
		if !reflect.DeepEqual(root, want) {
			t.Errorf("definitions: got %v want %v", root, want)
		}
		schema := parse(`{"definitions":{"A":{"type":"string"}},
			"properties":{"definitions":{"$ref":"#/definitions/A"},"X":{"enum":[{"$ref":"#/definitions/A"}]}}}`)
		moveDefinitions(map[string]any{}, schema, "P")
		want = parse(`{"properties":{"definitions":{"$ref":"#/definitions/PA"},"X":{"enum":[{"$ref":"#/definitions/A"}]}}}`)
		if !reflect.DeepEqual(schema, want) {
			t.Errorf("schema: got %v want %v", schema, want)
		}
	})
	t.Run("unsupportedPattern", func(t *testing.T) {
		if p := unsupportedPattern(parse(`{"properties":{"X":{"const":{"pattern":"(?!a)"},"examples":[{"pattern":"(?!b)"}]}}}`)); p != "" {
			t.Errorf("pattern %q read from data", p)
		}
		if p := unsupportedPattern(parse(`{"properties":{"pattern":{"pattern":"(?<=c)d"}}}`)); p != "(?<=c)d" {
			t.Errorf("got %q, want the pattern of the property named pattern", p)
		}
	})
	t.Run("prepareExtension", func(t *testing.T) {
		got := prepareExtension(parse(`{"if":{"dependentRequired":{"A":["B"]},"default":{"const":"0"}},
			"definitions":{"const":{"type":"string","const":"0"}}}`), false, false, "")
		want := parse(`{"if":{"dependentRequired":{"A":["B"]},"default":{"const":"0"},"x-cfn-cue-literal":true},
			"definitions":{"const":{"type":"string","enum":["0",0]}}}`)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v want %v", got, want)
		}
	})
}
