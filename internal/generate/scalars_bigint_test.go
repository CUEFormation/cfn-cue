package generate

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/parser"
)

func TestQuotedIntegersHaveUnlimitedPrecision(t *testing.T) {
	f, err := parser.ParseFile("bigint.cue", `#Properties: {
		Unbounded?: int
		AtLeast?: int & >=99999999999999999999
		AtMost?: int & <=99999999999999999999
	}`)
	if err != nil {
		t.Fatal(err)
	}
	addScalarCompatibility(f)
	source, err := render(f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := cuecontext.New()
	schema := ctx.CompileBytes(source).LookupPath(cue.MakePath(cue.Def("Properties")))
	for _, data := range []string{
		`{Unbounded: "99999999999999999999"}`,
		`{Unbounded: "` + strings.Repeat("9", 200) + `"}`,
		`{Unbounded: " -` + strings.Repeat("9", 200) + ` "}`,
		`{AtLeast: "99999999999999999999", AtMost: "99999999999999999999"}`,
		`{AtLeast: "100000000000000000000"}`,
	} {
		validate(t, schema, data, true)
	}
	for _, data := range []string{
		`{AtLeast: "99999999999999999998"}`,
		`{AtMost: "100000000000000000000"}`,
		`{Unbounded: "99999999999999999999.0"}`,
		`{Unbounded: "9e20"}`,
	} {
		validate(t, schema, data, false)
	}
}
