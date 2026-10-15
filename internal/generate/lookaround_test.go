package generate

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type lookaroundOracle struct {
	Engine   string   `json:"engine"`
	Samples  []string `json:"samples"`
	Patterns []struct {
		Pattern        string   `json:"pattern"`
		Samples        string   `json:"samples"`
		Examples       []string `json:"examples"`
		ExampleResults string   `json:"exampleResults"`
	} `json:"patterns"`
}

type lookaroundPatterns struct {
	Patterns []struct {
		Pattern   string `json:"pattern"`
		Redundant string `json:"redundant"`
	} `json:"patterns"`
	Unsupported []struct {
		Pattern string `json:"pattern"`
		Reason  string `json:"reason"`
	} `json:"unsupported"`
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "lookaround", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

var compiledPatterns = map[string]*regexp.Regexp{}

// matchesSchema evaluates the JSON Schema subset decomposeLookarounds emits
// against a string.
func matchesSchema(t *testing.T, schema map[string]any, s string) bool {
	t.Helper()
	compiled := func(p string) *regexp.Regexp {
		re, ok := compiledPatterns[p]
		if !ok {
			re = regexp.MustCompile(p)
			compiledPatterns[p] = re
		}
		return re
	}
	for k, v := range schema {
		switch k {
		case "type":
			if v != "string" {
				t.Fatalf("unexpected type %v", v)
			}
		case "pattern":
			if !compiled(v.(string)).MatchString(s) {
				return false
			}
		case "not":
			if matchesSchema(t, v.(map[string]any), s) {
				return false
			}
		case "allOf":
			for _, x := range v.([]any) {
				if !matchesSchema(t, x.(map[string]any), s) {
					return false
				}
			}
		case "anyOf":
			found := false
			for _, x := range v.([]any) {
				found = found || matchesSchema(t, x.(map[string]any), s)
			}
			if !found {
				return false
			}
		default:
			t.Fatalf("unexpected keyword %s", k)
		}
	}
	return true
}

// TestLookaroundOracle compares each translation with ECMAScript's result
// for the same pattern, recorded by scripts/lookaround-oracle.mjs.
func TestLookaroundOracle(t *testing.T) {
	var oracle lookaroundOracle
	readJSON(t, "oracle.json", &oracle)
	if len(oracle.Patterns) == 0 {
		t.Fatal("empty oracle")
	}
	var patterns lookaroundPatterns
	readJSON(t, "patterns.json", &patterns)
	redundant := map[string]bool{}
	for _, p := range patterns.Patterns {
		redundant[p.Pattern] = p.Redundant != ""
	}
	for _, p := range oracle.Patterns {
		schema, superset, err := decomposeLookarounds(p.Pattern)
		if err != nil {
			t.Errorf("%s: %v", p.Pattern, err)
			continue
		}
		widerRE := regexp.MustCompile(superset)
		samples := append(append([]string(nil), oracle.Samples...), p.Examples...)
		want := p.Samples + p.ExampleResults
		if len(want) != len(samples) {
			t.Fatalf("%s: %d results for %d samples", p.Pattern, len(want), len(samples))
		}
		accepted, rejected, excluded := 0, 0, 0
		for i, s := range samples {
			got := matchesSchema(t, schema, s)
			if got != (want[i] == '1') {
				t.Errorf("%s: %q: translation %v, ECMAScript %v", p.Pattern, s, got, want[i] == '1')
			}
			wider := widerRE.MatchString(s)
			if got && !wider {
				t.Errorf("%s: %q matches, but not the wider pattern %s", p.Pattern, s, superset)
			}
			switch {
			case got:
				accepted++
			case wider:
				excluded++
			default:
				rejected++
			}
		}
		// Each pattern needs a string its lookarounds reject that the rest
		// of the pattern would accept, or the comparison proves little.
		// That is impossible when the lookarounds only assert the end of the
		// string, or when the rest of the pattern already excludes the same
		// strings, as recorded in patterns.json.
		onlyEnd := reflect.DeepEqual(schema, map[string]any{"pattern": superset})
		if accepted == 0 || excluded == 0 && !onlyEnd && !redundant[p.Pattern] {
			t.Errorf("%s: %d accepted, %d rejected only by lookarounds, %d otherwise rejected; add examples", p.Pattern, accepted, excluded, rejected)
		}
	}
}

func TestLookaroundShapes(t *testing.T) {
	for _, tt := range []struct{ pattern, schema, superset string }{
		// A prefix lookahead is checked at offset 0.
		{`^(?!aws:)[a-z:]+$`,
			`{"allOf":[{"pattern":"^[a-z:]+$"},{"not":{"pattern":"^(?:aws:)","type":"string"}}]}`, `^[a-z:]+$`},
		// After a fixed-width prefix, at that offset.
		{`^[a-z](?!.*--)[a-z-]*$`,
			`{"allOf":[{"pattern":"^[a-z][a-z-]*$"},{"not":{"pattern":"^(?s:.){1}(?:.*--)","type":"string"}}]}`, `^[a-z][a-z-]*$`},
		// A positive lookahead whose body starts with ^ anchors the match.
		{`(?=^.{3,63}$)[a-z.]+`,
			`{"allOf":[{"pattern":"^(?:[a-z.]+)"},{"pattern":"^(?:^.{3,63}$)"}]}`, `^(?:[a-z.]+)`},
		// A lookbehind before $ is checked at the end of the string.
		{`[a-z-]+(?<!-)$`,
			`{"allOf":[{"pattern":"[a-z-]+$"},{"not":{"pattern":"(?:-)$","type":"string"}}]}`, `[a-z-]+$`},
		{`^[a-z]{2}(?<!ab)c$`,
			`{"allOf":[{"pattern":"^[a-z]{2}c$"},{"not":{"pattern":"^(?:ab)","type":"string"}}]}`, `^[a-z]{2}c$`},
		// (?![\s\S]) only holds at the end.
		{`^\S([\s\S]*\S)?(?![\s\S])`, `{"pattern":"^\\S([\\s\\S]*\\S)?$"}`, `^\S([\s\S]*\S)?$`},
		// Without an anchor, a later match start makes ^ in the body fail.
		{`(?!^[0-9]+$)[a-z0-9]+`,
			`{"anyOf":[{"allOf":[{"pattern":"^(?:[a-z0-9]+)"},{"not":{"pattern":"^(?:^[0-9]+$)","type":"string"}}]},{"pattern":"^(?s:.)+(?:[a-z0-9]+)"}]}`, `[a-z0-9]+`},
		// Group alternatives are distributed; alternatives without
		// lookarounds stay one pattern.
		{`^(?:(?=.*[0-9])|(?=.*-))[a-z0-9-]+$|^_$|^\.$`,
			`{"anyOf":[{"pattern":"^_$|^\\.$"},{"allOf":[{"pattern":"^[a-z0-9-]+$"},{"pattern":"^(?:.*[0-9])"}]},{"allOf":[{"pattern":"^[a-z0-9-]+$"},{"pattern":"^(?:.*-)"}]}]}`,
			`(?:^_$|^\.$)|(?:^[a-z0-9-]+$)|(?:^[a-z0-9-]+$)`},
		// An alternative that can never match is dropped.
		{`^(?<=ab)x|y`, `{"pattern":"y"}`, `y`},
		{`^[a-z-]+(?<!-)(?![\s\S])`,
			`{"allOf":[{"pattern":"^[a-z-]+$"},{"not":{"pattern":"(?:-)$","type":"string"}}]}`, `^[a-z-]+$`},
		// A nullable remainder adds no pattern.
		{`^(?!arn:)`, `{"not":{"pattern":"^(?:arn:)","type":"string"}}`, `^`},
	} {
		schema, superset, err := decomposeLookarounds(tt.pattern)
		if err != nil {
			t.Errorf("%s: %v", tt.pattern, err)
			continue
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(tt.schema), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(schema, want) || superset != tt.superset {
			b, _ := json.Marshal(schema)
			t.Errorf("%s:\n got %s %s\nwant %s %s", tt.pattern, b, superset, tt.schema, tt.superset)
		}
	}
}

// TestLookaroundUnsupported keeps the shapes that are not translated, and
// the reason each diagnostic gives.
func TestLookaroundUnsupported(t *testing.T) {
	var patterns lookaroundPatterns
	readJSON(t, "patterns.json", &patterns)
	cases := append(patterns.Unsupported, []struct {
		Pattern string `json:"pattern"`
		Reason  string `json:"reason"`
	}{
		{`a(?=b)`, "lookahead at a variable position"},
		{`^(?i)(?!aws)[a-z]+$`, "inline flags outside a lookaround"},
		{`(?i)^x$|^(?!aws)[a-z]+$`, "inline flags outside a lookaround"},
		{`^(?i:(?!aws)[a-z]+)$`, "lookaround inside a group with inline flags"},
		{`^((?!-)[a-z-])+$`, "lookaround inside a repeated group"},
		{`^(a)(?!\1)`, "backreference"},
		{`^[](?!a)`, "class starting with ]"},
		{`^a+(?<=a)b`, "lookbehind at a variable position or with variable width"},
	}...)
	for _, tt := range cases {
		_, _, err := decomposeLookarounds(tt.Pattern)
		if err == nil || err.Error() != tt.Reason {
			t.Errorf("%s: got %v, want %s", tt.Pattern, err, tt.Reason)
		}
	}
	if _, _, err := decomposeLookarounds(`^[a-z]{1,2000}$`); err != errNoLookaround {
		t.Errorf("pattern without lookarounds: %v", err)
	}
}

// TestSnapshotLookaroundInventory checks that every provider pattern with a
// lookaround in the pinned snapshot is either compared with ECMAScript in
// the oracle or listed as unsupported.
func TestSnapshotLookaroundInventory(t *testing.T) {
	z, err := zip.OpenReader(filepath.Join("..", "..", "snapshots", "schemas-2026-10-12.zip"))
	if err != nil {
		t.Skip(err)
	}
	defer z.Close()
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch k {
				case "pattern":
					if s, ok := child.(string); ok {
						found[s] = true
					}
				case "patternProperties":
					if m, ok := child.(map[string]any); ok {
						for name := range m {
							found[name] = true
						}
					}
				case "enum", "const", "default", "examples":
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	for _, f := range z.File {
		if !strings.HasPrefix(f.Name, "resources/") || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var schema any
		if err := json.NewDecoder(r).Decode(&schema); err != nil {
			t.Fatal(err)
		}
		r.Close()
		walk(schema)
	}
	var oracle lookaroundOracle
	readJSON(t, "oracle.json", &oracle)
	var patterns lookaroundPatterns
	readJSON(t, "patterns.json", &patterns)
	known := map[string]string{}
	for _, p := range oracle.Patterns {
		known[p.Pattern] = "supported"
	}
	for _, p := range patterns.Unsupported {
		known[p.Pattern] = "unsupported"
	}
	var lookarounds []string
	for p := range found {
		if _, err := regexp.Compile(re2Pattern(p)); err == nil || !hasLookaround(p) {
			continue
		}
		lookarounds = append(lookarounds, p)
	}
	sort.Strings(lookarounds)
	supported := 0
	for _, p := range lookarounds {
		_, _, err := decomposeLookarounds(p)
		switch {
		case err == nil && known[p] != "supported":
			t.Errorf("translated pattern missing from oracle.json: %q", p)
		case err != nil && known[p] != "unsupported":
			t.Errorf("untranslated pattern missing from patterns.json unsupported: %q: %v", p, err)
		case err == nil:
			supported++
		}
	}
	t.Logf("%d snapshot patterns with lookarounds: %d translated, %d diagnosed", len(lookarounds), supported, len(lookarounds)-supported)
}

func TestGeneratedLookaroundConstraints(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	properties["Key"] = map[string]any{"type": "string", "pattern": `^(?!aws:)[a-zA-Z+-=._:/]+$`}
	properties["Label"] = map[string]any{"type": "string", "pattern": `(?!^[0-9]+$)([a-zA-Z0-9-_' ']+)`}
	properties["Tags"] = map[string]any{"type": "object", "additionalProperties": false, "patternProperties": map[string]any{
		`^(?!aws:)[a-zA-Z+-=._:/]+$`: map[string]any{"type": "string", "maxLength": 3},
	}}
	// A provider pattern and a cfn-lint format on one property both apply.
	properties["Bucket"] = map[string]any{"type": "string", "format": "AWS::S3::Bucket.Name", "pattern": `(?!^(\d+\.)+\d+$)(^(([a-z0-9]|[a-z0-9][a-z0-9\-]*[a-z0-9])\.)*([a-z0-9]|[a-z0-9][a-z0-9\-]*[a-z0-9])$)`}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Diagnostics) != 0 {
		t.Fatalf("translated patterns diagnosed: %v", manifest.Diagnostics)
	}
	template := propertiesSchema(t, out, "./resources/htest")
	literal := propertiesSchema(t, out, "./literal/htest")
	for _, data := range []string{
		`{Name:"one",Key:"Name",Label:"a1",Tags:{Name:"x","a:b":"y"},Bucket:"my-bucket"}`,
		`{Name:"one",Key:"aws",Label:"12"}`, // ECMAScript also finds "2" at offset 1
		`{Name:"one",Bucket:"1.2.3.a"}`,
	} {
		validate(t, literal, data, true)
		validate(t, template, data, true)
	}
	for _, data := range []string{
		`{Name:"one",Key:"aws:Name"}`,
		`{Name:"one",Key:"a b"}`,
		`{Name:"one",Label:"1"}`,
		`{Name:"one",Tags:{"aws:Name":"x"}}`,
		`{Name:"one",Tags:{"a b":"x"}}`,
		`{Name:"one",Tags:{Name:"long"}}`,
		`{Name:"one",Bucket:"1.2.3.4"}`,   // provider pattern
		`{Name:"one",Bucket:"My-Bucket"}`, // both
		`{Name:"one",Bucket:"a_b"}`,       // both
	} {
		validate(t, literal, data, false)
		validate(t, template, data, false)
	}
	for _, data := range []string{
		`{Name:"one",Key:{Ref:"KeyParameter"},Label:12,Tags:{Name:{Ref:"Value"}}}`,
		`{Name:"one",Key:"{{resolve:ssm:/key}}"}`,
	} {
		validate(t, template, data, true)
		validate(t, literal, data, false)
	}
	// Numbers are checked through their string form.
	validate(t, template, `{Name:"one",Label:1}`, false)
}

func TestUntranslatedLookaroundDiagnostics(t *testing.T) {
	source := fixture(t)
	var root map[string]any
	if err := json.Unmarshal(source.files["resources/test.json"], &root); err != nil {
		t.Fatal(err)
	}
	properties := root["properties"].(map[string]any)
	properties["Host"] = map[string]any{"type": "string", "pattern": `^((?!-)[a-z0-9-]+\.)+[a-z]+$`}
	// With other keys allowed, excluding a key would change their schema.
	properties["Open"] = map[string]any{"type": "object", "patternProperties": map[string]any{`^(?!aws:).+$`: map[string]any{"type": "string"}}}
	// A positive lookahead in a key cannot become an excluded key pattern.
	properties["Sized"] = map[string]any{"type": "object", "additionalProperties": false, "patternProperties": map[string]any{`^(?=.{1,3}$)[a-z]+$`: map[string]any{"type": "string"}}}
	// Without lookarounds, the diagnostic gives no lookaround reason.
	properties["Long"] = map[string]any{"type": "object", "additionalProperties": false, "patternProperties": map[string]any{`^.{1,2048}$`: map[string]any{"type": "string"}}}
	source.files["resources/test.json"], _ = json.Marshal(root)
	out := filepath.Join(t.TempDir(), "generated")
	manifest, err := Generate(source, Options{Out: out, Regions: []string{"us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"#/properties/Long/patternProperties/^.{1,2048}$":          "object opened for unmatched keys: error parsing regexp: invalid repeat count: `{1,2048}`",
		"#/properties/Host/pattern":                                "lookarounds not translated: lookaround inside a repeated group",
		"#/properties/Open/patternProperties/^(?!aws:).+$":         "lookarounds not translated: key pattern must be the object's only way to name a property",
		"#/properties/Sized/patternProperties/^(?=.{1,3}$)[a-z]+$": "lookarounds not translated: key pattern requires more than excluded prefixes",
	}
	for _, d := range manifest.Diagnostics {
		if suffix, ok := want[d.Path]; ok && strings.HasSuffix(d.Detail, suffix) {
			delete(want, d.Path)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing diagnostics %v in %v", want, manifest.Diagnostics)
	}
	if _, err := Generate(source, Options{Out: filepath.Join(t.TempDir(), "strict"), Regions: []string{"us-east-1"}, Strict: true}); err == nil {
		t.Error("strict generation accepted an untranslated pattern")
	}
}

// Service-rule conditions keep their own rule: a condition pattern RE2 cannot
// compile skips the whole rule, even when a provider pattern of the same
// shape would be translated.
func TestExtensionConditionLookaroundSkipsRule(t *testing.T) {
	var schema any
	if err := json.Unmarshal([]byte(`{"if":{"properties":{"Name":{"pattern":"^(?!.*--)[a-z-]+$"}}},"then":{"required":["Other"]}}`), &schema); err != nil {
		t.Fatal(err)
	}
	if got := unsupportedPattern(prepareExtension(schema, false, false, "")); got != "^(?!.*--)[a-z-]+$" {
		t.Errorf("unsupportedPattern = %q", got)
	}
}
