// Package validate checks concrete, normalized CloudFormation templates against
// a generated bundle. It imports only resource schemas used by the input.
package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/token"
	cuejson "cuelang.org/go/encoding/json"
	"github.com/cueformation/cfn-cue/internal/bundle"
	"github.com/cueformation/cfn-cue/internal/cfn"
	"github.com/cueformation/cfn-cue/internal/conditions"
	"github.com/cueformation/cfn-cue/internal/semantic"
)

// Check selections. Structural checks evaluate the generated CUE schemas;
// semantic checks resolve references without CUE (see internal/semantic).
const (
	ChecksStructural = "structural"
	ChecksSemantic   = "semantic"
	ChecksAll        = "all"
)

type Options struct {
	Schemas, Region, Resource string
	// Checks selects ChecksStructural (the default when empty),
	// ChecksSemantic or ChecksAll.
	Checks string
}

type Result struct {
	Status        string   `json:"status"`
	Message       string   `json:"message,omitempty"`
	Phase         string   `json:"phase"`
	Region        string   `json:"region"`
	Resource      string   `json:"resource,omitempty"`
	ResourceTypes []string `json:"resourceTypes,omitempty"`
	ElapsedMS     int64    `json:"elapsedMs"`
	HeapMiB       float64  `json:"heapMiB"`
	Checks        string   `json:"checks"`
	// Diagnostics lists semantic findings, including warnings that do not
	// make the template invalid.
	Diagnostics []semantic.Diagnostic `json:"diagnostics,omitempty"`
}

var logicalID = regexp.MustCompile(`^[A-Za-z0-9]{1,255}$`)

func truncate(message string) string {
	if len(message) > 8000 {
		message = message[:8000] + "\n[diagnostics truncated]"
	}
	return message
}

// Check has no cancellation point inside CUE's evaluator. Call it in a process
// with a deadline; the CLI and corpus runner enforce that boundary.
func Check(data []byte, opt Options) (result Result) {
	start := time.Now()
	checks := opt.Checks
	if checks == "" {
		checks = ChecksStructural
	}
	result = Result{Status: "error", Phase: "input", Region: opt.Region, Resource: opt.Resource, Checks: checks}
	defer func() {
		result.ElapsedMS = time.Since(start).Milliseconds()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		result.HeapMiB = float64(mem.HeapAlloc) / (1 << 20)
	}()
	fail := func(status, message string) Result {
		result.Status, result.Message = status, message
		return result
	}
	if checks != ChecksStructural && checks != ChecksSemantic && checks != ChecksAll {
		return fail("error", "unknown checks selection: "+checks)
	}
	if checks != ChecksStructural && opt.Resource != "" {
		return fail("error", "semantic checks need the whole template; omit the resource selection")
	}
	var template map[string]json.RawMessage
	if err := json.Unmarshal(data, &template); err != nil || template == nil {
		return fail("invalid", "input must be a normalized JSON object")
	}
	root, err := filepath.Abs(opt.Schemas)
	if err != nil {
		return fail("error", err.Error())
	}
	// The region index holds only what this check needs; the full manifest
	// takes longer to decode than most templates take to validate.
	index, err := bundle.ReadRegionIndex(root, opt.Region)
	if errors.Is(err, os.ErrNotExist) {
		if _, e := os.Stat(filepath.Join(root, bundle.ManifestFile)); e != nil {
			return fail("error", "not a generated bundle: "+e.Error())
		}
		if bundle.ValidRegion(opt.Region) {
			if _, e := os.Stat(filepath.Join(root, filepath.FromSlash(bundle.RegionDir(opt.Region)))); e == nil {
				return fail("error", "generated bundle has no index for "+opt.Region+"; regenerate it")
			}
		}
		return fail("unsupported", "region absent from generated bundle: "+opt.Region)
	}
	if err != nil {
		return fail("error", "invalid generated region index: "+err.Error())
	}
	mapping := index.Resources
	var transforms []string
	if raw, ok := template["Transform"]; ok {
		// Unmarshal into an interface first: encoding/json accepts null for
		// string and []string targets without reporting a type mismatch.
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return fail("invalid", "Transform must be a string or a list of strings")
		}
		switch value := value.(type) {
		case string:
			transforms = []string{value}
		case []any:
			for _, entry := range value {
				name, ok := entry.(string)
				if !ok {
					return fail("invalid", "Transform must be a string or a list of strings")
				}
				transforms = append(transforms, name)
			}
		default:
			return fail("invalid", "Transform must be a string or a list of strings")
		}
	}
	for _, transform := range transforms {
		if transform != "AWS::LanguageExtensions" {
			return fail("unsupported", "macro expansion is not implemented: "+transform)
		}
		if !index.LanguageExtensions {
			return fail("unsupported", "generate with -language-extensions to check this template")
		}
	}
	// Numbers are kept as written so semantic checks compare them exactly.
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&decoded)
	var resources map[string]json.RawMessage
	if err := json.Unmarshal(template["Resources"], &resources); err != nil || len(resources) == 0 {
		resources = nil
	}
	// Single-resource mode ignores preprocessing needed only by other
	// resources or template sections.
	scope := decoded
	if opt.Resource != "" {
		if resources == nil {
			return fail("invalid", "Resources must be a nonempty object")
		}
		resource, ok := resources[opt.Resource]
		if !ok {
			return fail("invalid", "resource not found: "+opt.Resource)
		}
		resources = map[string]json.RawMessage{opt.Resource: resource}
		var value any
		_ = json.Unmarshal(resource, &value)
		scope = map[string]any{opt.Resource: value}
	}
	if name := unexpanded(scope); name != "" {
		return fail("unsupported", name+" requires preprocessing before schema validation")
	}
	if resources == nil {
		return fail("invalid", "Resources must be a nonempty object")
	}
	// Resources are classified in logical-ID order and every problem is kept, so
	// the status and message never depend on map iteration. See classify.
	order := make([]string, 0, len(resources))
	for name := range resources {
		order = append(order, name)
	}
	sort.Strings(order)
	names := make([]string, 0, len(resources))
	types := map[string]string{}
	actualTypes := map[string]bool{}
	schemaTypes := map[string]string{}
	var invalid, unsupported []string
	for _, name := range order {
		raw := resources[name]
		if !logicalID.MatchString(name) {
			invalid = append(invalid, "invalid resource logical ID: "+name)
			continue
		}
		var resource struct {
			Type string `json:"Type"`
		}
		if err := json.Unmarshal(raw, &resource); err != nil || resource.Type == "" {
			invalid = append(invalid, "resource "+name+" must have a string Type")
			continue
		}
		if strings.HasPrefix(resource.Type, "Custom::") && !cfn.IsCustomResource(resource.Type) {
			invalid = append(invalid, "resource "+name+" has an invalid custom resource Type")
			continue
		}
		actualTypes[resource.Type] = true
		schemaType := cfn.SchemaType(resource.Type)
		hash, ok := mapping[schemaType]
		if !ok {
			unsupported = append(unsupported, fmt.Sprintf("resource %s: %s is absent from %s", name, resource.Type, opt.Region))
			continue
		}
		names = append(names, name)
		types[schemaType] = hash
		schemaTypes[name] = schemaType
	}
	for typ := range actualTypes {
		result.ResourceTypes = append(result.ResourceTypes, typ)
	}
	sort.Strings(result.ResourceTypes)
	// Semantic checks need only the decoded template and the manifest, so they
	// run even when some resource types are unsupported. Their errors are
	// invalid results and take precedence over unsupported ones (see classify).
	var semanticErrors []string
	if checks != ChecksStructural {
		// Only semantic checks need GetAtt names and primary identifiers, which
		// are in the full manifest rather than the region index.
		b, err := os.ReadFile(filepath.Join(root, bundle.ManifestFile))
		if err != nil {
			return fail("error", err.Error())
		}
		var manifest bundle.Manifest
		if err := json.Unmarshal(b, &manifest); err != nil || manifest.Generator != bundle.Generator {
			return fail("error", "invalid generated manifest")
		}
		semanticErrors = checkSemantic(decoded, manifest.Schemas, mapping, &result)
	}
	if len(invalid) > 0 {
		return fail("invalid", classify(withSemantic(strings.Join(invalid, "\n"), semanticErrors), unsupported))
	}
	if checks == ChecksSemantic || len(names) == 0 {
		if checks != ChecksStructural {
			result.Phase = "semantic"
		}
		if len(semanticErrors) > 0 {
			return fail("invalid", classify(truncate(strings.Join(semanticErrors, "\n")), unsupported))
		}
		if len(unsupported) > 0 {
			return fail("unsupported", strings.Join(unsupported, "\n"))
		}
		result.Status = "valid"
		return result
	}
	f := &ast.File{Decls: []ast.Decl{&ast.Package{Name: ast.NewIdent("validation")}}}
	imports := &ast.ImportDecl{}
	if opt.Resource == "" {
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent("base"), index.Module+"/template:template"))
	}
	importTypes := make([]string, 0, len(types))
	for typ := range types {
		importTypes = append(importTypes, typ)
	}
	sort.Strings(importTypes)
	aliases, err := cfn.ResourceAliases(importTypes)
	if err != nil {
		return fail("error", err.Error())
	}
	for _, typ := range importTypes {
		alias := aliases[typ]
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent(alias), bundle.ResourceImport(index.Module, types[typ])))
	}
	f.Decls = append(f.Decls, imports)
	selected := &ast.StructLit{}
	for _, name := range names {
		var constraint ast.Expr = ast.NewSel(ast.NewIdent(aliases[schemaTypes[name]]), "#Resource")
		if opt.Resource != "" {
			// Whole-template mode supplies the data once below. Single-resource
			// mode needs only the selected resource's concrete input.
			expr, err := cuejson.Extract("input.json", resources[name])
			if err != nil {
				return fail("invalid", err.Error())
			}
			constraint = ast.NewBinExpr(token.AND, constraint, expr)
		}
		selected.Elts = append(selected.Elts, &ast.Field{Label: ast.NewString(name), Value: constraint})
	}
	var value ast.Expr
	if opt.Resource != "" {
		value = selected.Elts[0].(*ast.Field).Value
	} else {
		// Load the concrete data together with its constraints. Building an
		// abstract regional union first causes expensive disjunction expansion.
		expr, err := cuejson.Extract("input.json", data)
		if err != nil {
			return fail("invalid", err.Error())
		}
		logical := &ast.ListLit{Elts: []ast.Expr{ast.NewSel(ast.NewIdent("base"), "#LogicalID")}}
		constraints := &ast.StructLit{Elts: []ast.Decl{
			&ast.Field{Label: ast.NewIdent("Resources"), Value: selected},
			&ast.Field{Label: ast.NewIdent("Resources"), Value: &ast.StructLit{Elts: []ast.Decl{
				&ast.Field{Label: logical, Value: ast.NewSel(ast.NewIdent("base"), "#ResourceAttributes")},
			}}},
		}}
		value = ast.NewBinExpr(token.AND, ast.NewSel(ast.NewIdent("base"), "#Template"), constraints, expr)
	}
	f.Decls = append(f.Decls, &ast.Field{Label: ast.NewIdent("value"), Value: value})
	// Each variant is a resource with a reachable set of AWS::NoValue
	// removals applied. They are checked only after the template passes.
	variants, err := removalVariants(data, names)
	if err != nil {
		return fail("invalid", err.Error())
	}
	for i, v := range variants {
		expr, err := cuejson.Extract("input.json", v.data)
		if err != nil {
			return fail("error", err.Error())
		}
		constraint := ast.NewBinExpr(token.AND, ast.NewSel(ast.NewIdent(aliases[schemaTypes[v.name]]), "#Resource"), expr)
		f.Decls = append(f.Decls, &ast.Field{Label: ast.NewIdent(variantLabel(i)), Value: &ast.StructLit{Elts: []ast.Decl{
			&ast.Field{Label: ast.NewIdent("Resources"), Value: &ast.StructLit{Elts: []ast.Decl{
				&ast.Field{Label: ast.NewString(v.name), Value: constraint},
			}}},
		}}})
	}
	source, err := format.Node(f)
	if err != nil {
		return fail("error", err.Error())
	}
	result.Phase = "load"
	// An overlay keeps the generated module read-only and avoids copying it for
	// every template. Imports still resolve locally inside that module.
	path := filepath.Join(root, "__cfn_cue_validate__.cue")
	instances := load.Instances([]string{path}, &load.Config{Dir: root, Overlay: map[string]load.Source{path: load.FromBytes(source)}})
	if len(instances) != 1 {
		return fail("error", "expected one validation instance")
	}
	if instances[0].Err != nil {
		return fail("error", instances[0].Err.Error())
	}
	result.Phase = "evaluate"
	v := cuecontext.New().BuildInstance(instances[0])
	// The root error also includes failing variants, so it is examined after
	// them. Each stage keeps every diagnostic it finds; see classify.
	rootErr := v.Err()
	if err := v.LookupPath(cue.MakePath(cue.Str("value"))).Validate(cue.Concrete(true)); err != nil {
		return fail("invalid", classify(withSemantic(detail(err), semanticErrors), unsupported))
	}
	// Variants follow resource and removal order, so the message is stable.
	var removals []string
	for i, variant := range variants {
		label := variantLabel(i)
		err := v.LookupPath(cue.MakePath(cue.Str(label))).Validate(cue.Concrete(true))
		if missing := missingRemoved(err, label, variant); missing != nil {
			message := strings.ReplaceAll(detail(missing), label+".", "")
			removals = append(removals, fmt.Sprintf("Resources.%s: %s:\n%s", variant.name, variant.reason, strings.TrimSuffix(message, "\n")))
		}
	}
	// Root errors inside variants were either collected above or are not
	// removal failures.
	var other errors.Error
	for _, e := range errors.Errors(rootErr) {
		if path := e.Path(); len(path) == 0 || !strings.HasPrefix(path[0], "noValueVariant") {
			other = errors.Append(other, e)
		}
	}
	if other != nil {
		removals = append(removals, strings.TrimSuffix(detail(other), "\n"))
	}
	if len(removals) > 0 {
		return fail("invalid", classify(withSemantic(strings.Join(removals, "\n"), semanticErrors), unsupported))
	}
	// Semantic errors are invalid results, so they come before unsupported.
	if len(semanticErrors) > 0 {
		result.Phase = "semantic"
		return fail("invalid", classify(truncate(strings.Join(semanticErrors, "\n")), unsupported))
	}
	if len(unsupported) > 0 {
		return fail("unsupported", strings.Join(unsupported, "\n"))
	}
	result.Status = "valid"
	return result
}

// classify appends the resources that were not checked to an invalid result's
// diagnostics. Status precedence is error, then invalid, then unsupported: a
// template is invalid when any check that can run fails, and unsupported only
// when every checkable part passes but some resource type has no schema.
func classify(message string, unsupported []string) string {
	for _, line := range unsupported {
		message += "\nnot checked: " + line
	}
	return message
}

type resourceVariant struct {
	name, reason string
	data         []byte
	removed      [][]string
}

// missingRemoved keeps the errors that report a removed position as a
// required field. A copy can fail other rules because the Fn::If values it
// keeps count as present, which no single condition assignment may produce;
// those failures are not reported.
func missingRemoved(err error, label string, variant resourceVariant) error {
	if err == nil {
		return nil
	}
	var kept errors.Error
	for _, e := range errors.Errors(err) {
		if !strings.Contains(e.Error(), "field is required but not present") {
			continue
		}
		path := e.Path()
		for _, removed := range variant.removed {
			want := append([]string{label, "Resources", variant.name}, removed...)
			if len(path) != len(want) {
				continue
			}
			match := true
			for i := range want {
				match = match && strings.Trim(path[i], `"`) == want[i]
			}
			if match {
				kept = errors.Append(kept, e)
				break
			}
		}
	}
	if kept == nil {
		return nil
	}
	return kept
}

func variantLabel(i int) string { return fmt.Sprintf("noValueVariant%d", i) }

// removalVariants decodes the template again with numbers kept as written and
// returns the AWS::NoValue variants of the named resources.
func removalVariants(data []byte, names []string) ([]resourceVariant, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var template map[string]any
	if err := decoder.Decode(&template); err != nil {
		return nil, err
	}
	resources, _ := template["Resources"].(map[string]any)
	model := conditions.New(template)
	var out []resourceVariant
	for _, name := range names {
		resource, _ := resources[name].(map[string]any)
		for _, v := range noValueVariants(model, resource) {
			b, err := json.Marshal(v.resource)
			if err != nil {
				return nil, err
			}
			out = append(out, resourceVariant{name: name, reason: v.reason, data: b, removed: v.removed})
		}
	}
	return out, nil
}

// withSemantic appends semantic errors to another invalid result's message,
// so a template with both kinds of problem reports both.
func withSemantic(message string, semanticErrors []string) string {
	if len(semanticErrors) == 0 {
		return message
	}
	return message + "\n" + truncate(strings.Join(semanticErrors, "\n"))
}

// checkSemantic records semantic diagnostics and returns the error messages.
// GetAtt names and primary identifiers come from the manifest; a manifest
// without them leaves attribute names and identifiers unchecked rather than
// rejecting every GetAtt.
func checkSemantic(decoded any, schemas map[string]bundle.SchemaInfo, mapping map[string]string, result *Result) []string {
	template, _ := decoded.(map[string]any)
	diagnostics := semantic.Check(template, semantic.Options{Attributes: func(typ string) ([]string, bool) {
		hash, ok := mapping[cfn.SchemaType(typ)]
		if !ok {
			return nil, false
		}
		names := schemas[hash].Attributes
		return names, names != nil
	}, PrimaryIdentifier: func(typ string) []string {
		if hash, ok := mapping[typ]; ok {
			return schemas[hash].PrimaryIdentifier
		}
		return nil
	}})
	result.Diagnostics = diagnostics
	var messages []string
	for _, d := range diagnostics {
		if d.Severity == semantic.Error {
			messages = append(messages, d.Path+": "+d.Message)
		}
	}
	return messages
}

func detail(err error) string {
	return truncate(errors.Details(err, nil))
}

func unexpanded(value any) string {
	switch node := value.(type) {
	case map[string]any:
		// Visit keys in order so the reported construct is stable.
		keys := make([]string, 0, len(node))
		for key := range node {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "Fn::Transform" || strings.HasPrefix(key, "Fn::ForEach::") {
				return key
			}
			if name := unexpanded(node[key]); name != "" {
				return name
			}
		}
	case []any:
		for _, child := range node {
			if name := unexpanded(child); name != "" {
				return name
			}
		}
	}
	return ""
}
