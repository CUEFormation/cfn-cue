package generate

import (
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
)

func TestFieldRules(t *testing.T) {
	s := &Source{Label: "rules", SHA256: "rules", files: map[string][]byte{
		"providers/us-east-1.json": []byte(`{"AWS::Test::Rules":"rules"}`),
		"resources/rules.json": []byte(`{"typeName":"AWS::Test::Rules","type":"object","additionalProperties":false,
			"properties":{"A":{"type":"string"},"B":{"type":"string"},"C":{"type":"string"},"D":{"type":"string"},"E":{"type":"string"},
				"Nested":{"$ref":"#/definitions/Nested"},"Open":{"type":"object","properties":{"H":{"type":"string"},"I":{"type":"string"}},"anyOf":[{"required":["H"]},{"required":["I"]}]},
				"lower":{"type":"string"},"upper":{"type":"string"}},
			"requiredXor":["A","B","C"],
			"dependentExcluded":{"D":["E"],"E":["D"]},
			"oneOf":[{"required":["lower"]},{"required":["upper"]},{"required":["A"]},{"required":["B"]},{"required":["C"]}],
			"definitions":{"Nested":{"type":"object","additionalProperties":false,"properties":{"F":{"type":"string"},"G":{"type":"string"}},"oneOf":[{"required":["F"]},{"required":["G"]}]}}}`),
	}}
	out := filepath.Join(t.TempDir(), "generated")
	if _, e := Generate(s, Options{Out: out}); e != nil {
		t.Fatal(e)
	}
	for _, pkg := range []string{"./resources/hrules", "./literal/hrules"} {
		schema := propertiesSchema(t, out, pkg)
		if e := schema.Validate(); e != nil {
			t.Fatalf("%s: definition does not validate on its own: %v", pkg, e)
		}
		message := func(data, want string) {
			t.Helper()
			e := schema.Unify(schema.Context().CompileString(data)).Validate(cue.Concrete(true))
			if e == nil || !strings.Contains(errors.Details(e, nil), want) {
				t.Fatalf("%s %s: want error containing %q, got:\n%s", pkg, data, want, errors.Details(e, nil))
			}
		}
		validate(t, schema, `{A:"a",D:"d",Nested:{F:"f"},Open:{I:"i"}}`, true)
		message(`{A:"a",B:"b"}`, "B cannot be used with A; set exactly one of A, B, C")
		message(`{B:"b",C:"c"}`, "C cannot be used with B; set exactly one of A, B, C")
		message(`{A:"a",D:"d",E:"e"}`, "E cannot be used with D")
		message(`{A:"a",Nested:{F:"f",G:"g"}}`, "Nested.G: G cannot be used with F; set exactly one of F, G")
		message(`{D:"d"}`, "A: field is required but not present")
		message(`{A:"a",Open:{}}`, "H: field is required but not present")
		validate(t, schema, `{A:"a",Nested:{F:"f"},Bogus:1}`, false)
		validate(t, schema, `{A:"a",Nested:{F:"f",X:1}}`, false)
		// oneOf naming lowercase properties keeps the JSON Schema form.
		validate(t, schema, `{A:"a",lower:"x"}`, false)
	}
	// An Fn::If may resolve to AWS::NoValue, so it does not conflict.
	schema := propertiesSchema(t, out, "./resources/hrules")
	validate(t, schema, `{A:"a",D:{"Fn::If":["UseD","d",{Ref:"AWS::NoValue"}]},E:{"Fn::If":["UseD",{Ref:"AWS::NoValue"},"e"]}}`, true)
	validate(t, schema, `{A:"a",D:"d",E:{"Fn::If":["UseE","e",{Ref:"AWS::NoValue"}]}}`, true)
	validate(t, schema, `{A:{"Fn::If":["UseA","a","b"]},D:"d",E:"e"}`, false)
}
