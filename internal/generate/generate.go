package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

type Options struct {
	Out, Module                            string
	Regions, Resources                     []string
	IncludeSAM, LanguageExtensions, Strict bool
}
type SchemaInfo struct {
	Type               string `json:"type"`
	SHA256             string `json:"sha256"`
	PropertiesRequired bool   `json:"propertiesRequired"`
}
type Manifest struct {
	Generator          string                       `json:"generator"`
	Source             string                       `json:"source"`
	SourceSHA256       string                       `json:"sourceSHA256"`
	Module             string                       `json:"module"`
	LanguageExtensions bool                         `json:"languageExtensions"`
	IncludeSAM         bool                         `json:"includeSAM"`
	Regions            map[string]map[string]string `json:"regions"`
	Schemas            map[string]SchemaInfo        `json:"schemas"`
	Diagnostics        []Diagnostic                 `json:"diagnostics"`
	ValidationNotes    []string                     `json:"validationNotes"`
}

var safeHash = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var regionName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)+-[0-9]+$`)

// Generate writes to a staging directory and publishes only after every file
// formats and reparses. Existing output must carry our manifest marker.
func Generate(source *Source, opt Options) (*Manifest, error) {
	if opt.Out == "" {
		return nil, fmt.Errorf("output directory is required")
	}
	if opt.Module == "" {
		opt.Module = "github.com/cueformation/cfn-cue"
	}
	if strings.ContainsAny(opt.Module, "\n\r\" ") || strings.Contains(opt.Module, "@") {
		return nil, fmt.Errorf("module must be an unversioned CUE module path")
	}
	if st, e := os.Stat(opt.Out); e == nil {
		if !st.IsDir() {
			return nil, fmt.Errorf("output exists and is not a directory")
		}
		b, e := os.ReadFile(filepath.Join(opt.Out, "manifest.json"))
		var old Manifest
		if e != nil || json.Unmarshal(b, &old) != nil || old.Generator != "cfn-cue/v2" {
			return nil, fmt.Errorf("refusing to replace an unmanaged directory: %s", opt.Out)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	m := &Manifest{Generator: "cfn-cue/v2", Source: source.Label, SourceSHA256: source.SHA256, Module: opt.Module, LanguageExtensions: opt.LanguageExtensions, IncludeSAM: opt.IncludeSAM, Regions: map[string]map[string]string{}, Schemas: map[string]SchemaInfo{}, Diagnostics: []Diagnostic{}}
	m.ValidationNotes = []string{
		"Each region has a full resource package and a separate types package. The types package checks template structure and regional type names, leaving resource Properties unconstrained.",
		"Full regional aggregate evaluation has exceeded prior verification budgets; use the reproducible regional performance script to measure current CUE versions and resource scales.",
		"Intrinsic schemas validate argument shapes and result type classes. Function-specific nesting permissions and context permissions require cfn-lint.",
		"Ref, GetAtt, Sub, mapping lookups, condition evaluation, macro expansion, and property-specific constraints on intrinsic results require semantic validation.",
		"CreationPolicy, UpdatePolicy, parameter type names, and SAM Globals use structural schemas; service-specific restrictions require cfn-lint.",
		"LanguageExtensions ForEach schemas describe resource fragments; loop expansion and ForEach in Conditions or Outputs are not implemented.",
	}
	requested := map[string]bool{}
	for _, r := range opt.Regions {
		requested[r] = true
	}
	sam := map[string]string{}
	if opt.IncludeSAM {
		b, e := source.read("providers/sam.json")
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &sam); e != nil {
			return nil, e
		}
	}
	for _, name := range sortedFileNames(source.files) {
		if !strings.HasPrefix(name, "providers/") {
			continue
		}
		region := strings.TrimSuffix(strings.TrimPrefix(name, "providers/"), ".json")
		if region == "sam" {
			continue
		}
		if !regionName.MatchString(region) {
			return nil, fmt.Errorf("unexpected provider region %q", region)
		}
		if len(opt.Regions) > 0 && !includes(opt.Regions, region) {
			continue
		}
		delete(requested, region)
		mapping := map[string]string{}
		if e := json.Unmarshal(source.files[name], &mapping); e != nil {
			return nil, fmt.Errorf("%s: %w", name, e)
		}
		for typ, hash := range sam {
			mapping[typ] = hash
		}
		for typ, hash := range mapping {
			if !safeHash.MatchString(hash) {
				return nil, fmt.Errorf("unsafe schema identifier %q", hash)
			}
			if len(opt.Resources) > 0 && !includes(opt.Resources, typ) {
				delete(mapping, typ)
				continue
			}
		}
		if len(mapping) == 0 {
			return nil, fmt.Errorf("no selected resource types available in %s", region)
		}
		m.Regions[region] = mapping
	}
	if len(requested) > 0 {
		return nil, fmt.Errorf("requested regions not found: %v", sortedBoolKeys(requested))
	}
	if len(m.Regions) == 0 {
		return nil, fmt.Errorf("source contains no regional providers")
	}
	available := map[string]bool{}
	hashTypes := map[string]string{}
	for _, mapping := range m.Regions {
		for typ, hash := range mapping {
			available[typ] = true
			if prev, ok := hashTypes[hash]; ok && prev != typ {
				return nil, fmt.Errorf("schema %s maps to multiple resource types", hash)
			}
			hashTypes[hash] = typ
		}
	}
	for _, typ := range opt.Resources {
		if !available[typ] {
			return nil, fmt.Errorf("requested resource type unavailable: %s", typ)
		}
	}
	parent := filepath.Dir(opt.Out)
	if e := os.MkdirAll(parent, 0755); e != nil {
		return nil, e
	}
	stage, e := os.MkdirTemp(parent, ".cfn-cue-stage-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(stage)
	write := func(path string, b []byte) error {
		p := filepath.Join(stage, path)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		return os.WriteFile(p, b, 0644)
	}
	writeAST := func(path string, f *ast.File) error {
		b, e := render(f)
		if e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		if _, e = parser.ParseFile(path, b, parser.ParseComments); e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
		return write(path, b)
	}
	if e = write("cue.mod/module.cue", []byte(fmt.Sprintf("module: %q\nlanguage: version: \"v0.18.0\"\n", opt.Module+"@v0"))); e != nil {
		return nil, e
	}
	if e = writeAST("fn/functions.cue", Intrinsics(opt.LanguageExtensions)); e != nil {
		return nil, e
	}
	if e = writeAST("template/template.cue", Template(opt.Module, opt.LanguageExtensions, opt.IncludeSAM)); e != nil {
		return nil, e
	}
	for _, hash := range sortedStringKeys(hashTypes) {
		b, e := source.read("resources/" + hash + ".json")
		if e != nil {
			return nil, e
		}
		root := map[string]any{}
		if e = json.Unmarshal(b, &root); e != nil {
			return nil, e
		}
		if e = validateSchema(root, "#"); e != nil {
			return nil, fmt.Errorf("schema %s: %w", hash, e)
		}
		typ, _ := root["typeName"].(string)
		if typ != hashTypes[hash] {
			return nil, fmt.Errorf("schema %s typeName %q does not match %q", hash, typ, hashTypes[hash])
		}
		sum := sha256.Sum256(b)
		m.Schemas[hash] = SchemaInfo{typ, hex.EncodeToString(sum[:]), len(asArray(root["required"])) > 0}
		c := &converter{module: opt.Module, resource: typ, extensions: opt.LanguageExtensions}
		f, e := c.extract(root, false)
		if e != nil {
			return nil, fmt.Errorf("literal %s: %w", typ, e)
		}
		stripRootEmbed(f)
		if e = writeAST("literal/h"+hash+"/schema.cue", f); e != nil {
			return nil, e
		}
		f, e = c.extract(root, true)
		if e != nil {
			return nil, fmt.Errorf("template %s: %w", typ, e)
		}
		stripRootEmbed(f)
		addImport(f, "base", opt.Module+"/template:template")
		propsRequired := len(asArray(root["required"])) > 0
		resource := and(sel("base", "ResourceAttributes"), obj(field("Type", str(typ), false), field("Properties", id("#Properties"), !propsRequired)))
		if typ == "AWS::CloudFormation::CustomResource" {
			resource = and(sel("base", "ResourceAttributes"), obj(field("Type", union(str(typ), &ast.UnaryExpr{Op: token.MAT, X: str("^Custom::[A-Za-z0-9_@-]{1,60}$")}), false), field("Properties", id("#Properties"), false)))
		}
		f.Decls = append(f.Decls, def("Resource", resource))
		if e = writeAST("resources/h"+hash+"/schema.cue", f); e != nil {
			return nil, e
		}
		m.Diagnostics = append(m.Diagnostics, c.diagnostics...)
	}
	for _, region := range sortedRegionKeys(m.Regions) {
		if e = writeAST("regions/"+region+"/region.cue", Region(opt.Module, m.Regions[region], opt.LanguageExtensions)); e != nil {
			return nil, e
		}
		if e = writeAST("regions/"+region+"/types/region.cue", RegionTypes(opt.Module, m.Regions[region], opt.LanguageExtensions)); e != nil {
			return nil, e
		}
	}
	m.Diagnostics = dedupDiagnostics(m.Diagnostics)
	if opt.Strict && len(m.Diagnostics) > 0 {
		return m, fmt.Errorf("strict generation refused %d diagnostics; no output published", len(m.Diagnostics))
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = write("manifest.json", append(b, '\n')); e != nil {
		return nil, e
	}
	// Keep rollback possible until the fully generated directory is in place.
	backup := ""
	if _, e = os.Stat(opt.Out); e == nil {
		backup, e = os.MkdirTemp(parent, ".cfn-cue-backup-")
		if e != nil {
			return nil, e
		}
		if e = os.Remove(backup); e != nil {
			return nil, e
		}
		if e = os.Rename(opt.Out, backup); e != nil {
			return nil, e
		}
	}
	if e = os.Rename(stage, opt.Out); e != nil {
		if backup != "" {
			_ = os.Rename(backup, opt.Out)
		}
		return nil, e
	}
	if backup != "" {
		if e = os.RemoveAll(backup); e != nil {
			return m, fmt.Errorf("generation published; old output retained at %s: %w", backup, e)
		}
	}
	return m, nil
}
func stripRootEmbed(f *ast.File) {
	decls := f.Decls[:0]
	for _, d := range f.Decls {
		if _, ok := d.(*ast.EmbedDecl); !ok {
			decls = append(decls, d)
		}
	}
	f.Decls = decls
}
func sortedFileNames(m map[string][]byte) []string {
	a := []string{}
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}
func sortedStringKeys(m map[string]string) []string {
	a := []string{}
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}
func sortedBoolKeys(m map[string]bool) []string {
	a := []string{}
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}
func sortedRegionKeys(m map[string]map[string]string) []string {
	a := []string{}
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}
func dedupDiagnostics(ds []Diagnostic) []Diagnostic {
	sort.Slice(ds, func(i, j int) bool {
		a, _ := json.Marshal(ds[i])
		b, _ := json.Marshal(ds[j])
		return string(a) < string(b)
	})
	out := []Diagnostic{}
	for _, d := range ds {
		if len(out) == 0 || d != out[len(out)-1] {
			out = append(out, d)
		}
	}
	return out
}
