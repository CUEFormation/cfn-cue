package generate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// nestedIf returns depth Fn::If expressions nested in their first branch.
func nestedIf(depth int, leaf, other string) string {
	x := leaf
	for i := 0; i < depth; i++ {
		x = fmt.Sprintf(`{"Fn::If": ["C", %s, %s]}`, x, other)
	}
	return x
}

// BenchmarkNestedStringIf measures validation of nested Fn::If expressions
// against string result views, whose branches are shared recursive
// definitions. String leaves are valid with or without scalar compatibility,
// so results compare before and after changes to the branch definitions.
func BenchmarkNestedStringIf(b *testing.B) {
	source, err := render(Intrinsics(false))
	if err != nil {
		b.Fatal(err)
	}
	ctx := cuecontext.New()
	fn := ctx.CompileBytes(source)
	for _, view := range []string{"StringExpression", "StringNumberExpression", "StringListObjectExpression", "StringNumberBooleanListObjectExpression"} {
		schema := fn.LookupPath(cue.MakePath(cue.Def(view)))
		for _, depth := range []int{1, 4, 16} {
			b.Run(fmt.Sprintf("%s/depth=%d", view, depth), func(b *testing.B) {
				data := ctx.CompileString(nestedIf(depth, `"a"`, `{"Fn::Sub": "b"}`))
				for i := 0; i < b.N; i++ {
					if err := schema.Unify(data).Validate(cue.Concrete(true)); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Fn::If branches of a string result accept numbers and booleans, as a
// literal string property does. Lists, objects and null stay invalid, and
// numeric and boolean results keep their own branch types.
func TestStringConditionalScalarBranches(t *testing.T) {
	source, err := render(Intrinsics(false))
	if err != nil {
		t.Fatal(err)
	}
	fn := cuecontext.New().CompileBytes(source)
	for _, tc := range []struct {
		view, data string
		valid      bool
	}{
		{"StringExpression", `{"Fn::If":["C",2,"x"]}`, true},
		{"StringExpression", `{"Fn::If":["C","x",-1.5]}`, true},
		{"StringExpression", `{"Fn::If":["C",true,false]}`, true},
		{"StringExpression", nestedIf(3, `2`, `true`), true},
		{"StringExpression", `{"Fn::If":["C",{"Fn::If":["D",2,{Ref:"AWS::NoValue"}]},{"Fn::Sub":"x"}]}`, true},
		{"StringExpression", `{"Fn::If":["C",[],"x"]}`, false},
		{"StringExpression", `{"Fn::If":["C",[2],"x"]}`, false},
		{"StringExpression", `{"Fn::If":["C",{a:"b"},"x"]}`, false},
		{"StringExpression", `{"Fn::If":["C",null,"x"]}`, false},
		{"StringExpression", `{"Fn::If":["C",2]}`, false},
		{"StringExpression", nestedIf(3, `[]`, `2`), false},
		{"StringNumberExpression", `{"Fn::If":["C",true,1]}`, true},
		{"StringNumberExpression", `{"Fn::If":["C",[],1]}`, false},
		{"StringBooleanExpression", `{"Fn::If":["C",2,true]}`, true},
		{"StringListObjectExpression", `{"Fn::If":["C",2,[]]}`, true},
		{"StringListObjectExpression", `{"Fn::If":["C",false,{a:"b"}]}`, true},
		{"StringListObjectExpression", `{"Fn::If":["C",null,"x"]}`, false},
		{"NumberExpression", `{"Fn::If":["C","abc",1]}`, false},
		{"NumberExpression", `{"Fn::If":["C",true,1]}`, false},
		{"BooleanExpression", `{"Fn::If":["C",2,true]}`, false},
		{"ListExpression", `{"Fn::If":["C",2,[]]}`, false},
		{"ObjectExpression", `{"Fn::If":["C",2,{}]}`, false},
	} {
		t.Run(tc.view+"/"+tc.data, func(t *testing.T) {
			validate(t, fn.LookupPath(cue.MakePath(cue.Def(tc.view))), tc.data, tc.valid)
		})
	}
}

// Scalar Fn::If branches do not loosen literal property constraints. A
// number or boolean literal must still have a string form that satisfies an
// enum or pattern, and numeric or boolean properties keep strict branches.
func TestStringConditionalScalarBranchProperties(t *testing.T) {
	root := map[string]any{
		"typeName": "AWS::Test::Thing", "type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"Label": map[string]any{"type": "string"},
			"Mode":  map[string]any{"type": "string", "enum": []any{"one", "-1"}},
			"Code":  map[string]any{"type": "string", "pattern": "^[a-z]+$"},
			"Port":  map[string]any{"type": "integer"},
			"Flag":  map[string]any{"type": "boolean"},
			"Mixed": map[string]any{"type": []any{"string", "integer"}},
			"Names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "generated")
	if _, err = Generate(&Source{Label: "test", SHA256: "test", files: map[string][]byte{"providers/us-east-1.json": []byte(`{"AWS::Test::Thing":"test"}`), "resources/test.json": b}}, Options{Out: out}); err != nil {
		t.Fatal(err)
	}
	schema := propertiesSchema(t, out, "./resources/htest")
	for _, data := range []string{
		`{Label:{"Fn::If":["C",2,"x"]}}`,
		`{Label:{"Fn::If":["C",true,{"Fn::If":["D",1.5,{Ref:"AWS::NoValue"}]}]}}`,
		`{Names:[{"Fn::If":["C",2,"x"]}]}`,
		`{Mixed:{"Fn::If":["C",true,1]}}`,
		`{Mode:-1,Code:{"Fn::If":["C",2,"abc"]}}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{Label:{"Fn::If":["C",[],"x"]}}`,
		`{Label:{"Fn::If":["C",{a:"b"},"x"]}}`,
		`{Names:{"Fn::If":["C",2,["x"]]}}`,
		`{Mode:2}`,
		`{Mode:true}`,
		`{Code:12}`,
		`{Code:-1.5}`,
		`{Port:{"Fn::If":["C","abc",1]}}`,
		`{Flag:{"Fn::If":["C",2,true]}}`,
		`{Mixed:{"Fn::If":["C",[],1]}}`,
	} {
		validate(t, schema, data, false)
	}
}
