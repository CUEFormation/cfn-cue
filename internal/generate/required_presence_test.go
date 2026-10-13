package generate

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

func TestPresenceOnlyAlternatives(t *testing.T) {
	for _, tc := range []struct {
		name    string
		schemas []string
		want    bool
	}{
		{"requiredOr", []string{`{EventPattern!: _, ...}`, `{ScheduleExpression!: _, ...}`}, true},
		{"multipleKeys", []string{`{A!: _, B!: _, ...}`, `({"C"!: _, ...})`}, true},
		{"optional", []string{`{A?: _, ...}`}, false},
		{"regularField", []string{`{A: _, ...}`}, false},
		{"typedField", []string{`{A!: string, ...}`}, false},
		{"intrinsic", []string{`{Ref!: string}`, `{"Fn::Join"!: [string,[...string]]}`}, false},
		{"pattern", []string{`{[string]: _, ...}`}, false},
		{"closed", []string{`{A!: _}`}, false},
		{"empty", []string{`{...}`}, false},
		{"nonObject", []string{`string`}, false},
		{"mixed", []string{`{A!: _, ...}`, `string`}, false},
		{"none", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var alternatives []ast.Expr
			for _, text := range tc.schemas {
				expr, err := parser.ParseExpr("predicate.cue", text)
				if err != nil {
					t.Fatal(err)
				}
				alternatives = append(alternatives, expr)
			}
			if got := presenceOnlyAlternatives(alternatives); got != tc.want {
				t.Fatalf("presenceOnlyAlternatives()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequiredPresenceAlternatives(t *testing.T) {
	for _, keyword := range []string{"requiredOr", "anyOf"} {
		t.Run(keyword, func(t *testing.T) {
			source := fixture(t)
			var root map[string]any
			if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
				t.Fatal(err)
			}
			delete(root, "required")
			if keyword == "requiredOr" {
				root[keyword] = []any{"Name", "Count"}
			} else {
				root[keyword] = []any{map[string]any{"required": []any{"Name"}}, map[string]any{"required": []any{"Count"}}}
			}
			source.files["resources/test.json"], _ = json.Marshal(root)
			out := filepath.Join(t.TempDir(), "generated")
			if _, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}}); err != nil {
				t.Fatal(err)
			}
			for _, pkg := range []string{"./resources/htest", "./literal/htest"} {
				schema := build(t, out, pkg).LookupPath(cue.MakePath(cue.Def("Properties")))
				for _, data := range []string{`{Name:"one"}`, `{Count:2}`, `{Name:"one",Count:2}`} {
					validate(t, schema, data, true)
				}
				for _, data := range []string{`{}`, `{Name:"bad"}`, `{Count:5}`, `{Name:"one",Count:5}`} {
					validate(t, schema, data, false)
				}
			}
		})
	}
}
