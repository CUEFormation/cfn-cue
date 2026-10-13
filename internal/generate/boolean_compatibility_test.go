package generate

import (
	"strconv"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/parser"
)

func TestBooleanSpellingsMatchConditionalBranches(t *testing.T) {
	f, err := parser.ParseFile("boolean.cue", `#Properties: { Enabled?: bool }`)
	if err != nil {
		t.Fatal(err)
	}
	addScalarCompatibility(f)
	propertySource, err := render(f)
	if err != nil {
		t.Fatal(err)
	}
	functionSource, err := render(Intrinsics(false))
	if err != nil {
		t.Fatal(err)
	}
	ctx := cuecontext.New()
	property := ctx.CompileBytes(propertySource).LookupPath(cue.MakePath(cue.Def("Properties")))
	function := ctx.CompileBytes(functionSource).LookupPath(cue.MakePath(cue.Def("BooleanExpression")))
	for _, spelling := range []string{"true", "True", "TRUE", "false", "False", "FALSE", "tRuE", "yes", " true "} {
		quoted := strconv.Quote(spelling)
		good := spelling != "tRuE" && spelling != "yes" && spelling != " true "
		validate(t, property, `{Enabled: `+quoted+`}`, good)
		validate(t, function, `{"Fn::If": ["Create", `+quoted+`, false]}`, good)
	}
}
