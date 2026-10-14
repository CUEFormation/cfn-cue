package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/load"
	cuejson "cuelang.org/go/encoding/json"
)

// TestCorpusRegionalTemplates checks the smoke corpus against each region's
// complete #Template in ../../generated. Check builds a schema from only the
// packages a template uses; this covers the regional package users import,
// including its selection of each resource's schema by Type. Results must
// match corpus/smoke/expectations.json. Set CFN_CUE_INTEGRATION=1 to run.
func TestCorpusRegionalTemplates(t *testing.T) {
	if os.Getenv("CFN_CUE_INTEGRATION") != "1" {
		t.Skip("set CFN_CUE_INTEGRATION=1 to check the corpus against generated regions")
	}
	corpus, err := filepath.Abs("../../corpus/smoke")
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := filepath.Abs("../../generated")
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Templates []struct{ ID, Path, Normalized string }
	}
	var expectations map[string]struct{ Cue, Note string }
	for path, target := range map[string]any{"index.json": &index, "expectations.json": &expectations} {
		b, err := os.ReadFile(filepath.Join(corpus, path))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, target); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	regions := map[string]bool{}
	for key := range expectations {
		regions[key[strings.LastIndex(key, ":")+1:]] = true
	}
	checked := 0
	for _, region := range sortedKeys(regions) {
		start := time.Now()
		instances := load.Instances([]string{"./regions/" + region}, &load.Config{Dir: schemas})
		if len(instances) != 1 || instances[0].Err != nil {
			t.Fatalf("%s: loading the region package failed: %v", region, instances)
		}
		ctx := cuecontext.New()
		schema := ctx.BuildInstance(instances[0]).LookupPath(cue.MakePath(cue.Def("Template")))
		if err := schema.Err(); err != nil {
			t.Fatalf("%s: %v", region, err)
		}
		t.Logf("%s: loaded in %s", region, time.Since(start).Round(time.Millisecond))
		start = time.Now()
		for _, template := range index.Templates {
			expected, ok := expectations[template.ID+":"+region]
			if !ok {
				t.Errorf("%s [%s]: no expectation", template.Path, region)
				continue
			}
			data, err := os.ReadFile(filepath.Join(corpus, template.Normalized))
			if err != nil {
				t.Fatal(err)
			}
			expr, err := cuejson.Extract(template.Normalized, data)
			if err != nil {
				t.Fatalf("%s: %v", template.Normalized, err)
			}
			err = schema.Unify(ctx.BuildExpr(expr)).Validate(cue.Concrete(true))
			// "unsupported" marks templates Check declines before evaluation,
			// such as unknown resource types. The region schema rejects them.
			if (err == nil) != (expected.Cue == "valid") {
				message := "accepted"
				if err != nil {
					message = "rejected: " + detail(err)
				}
				t.Errorf("%s [%s]: expected %s (%s), %s", template.Path, region, expected.Cue, expected.Note, message)
			}
			checked++
		}
		t.Logf("%s: checked %d templates in %s", region, len(index.Templates), time.Since(start).Round(time.Millisecond))
	}
	if checked != len(expectations) {
		t.Errorf("checked %d templates, but there are %d expectations", checked, len(expectations))
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
