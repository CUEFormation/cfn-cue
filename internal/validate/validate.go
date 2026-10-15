// Package validate checks concrete, normalized CloudFormation templates against
// a generated bundle. It imports only resource schemas used by the input.
package validate

import (
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
	"github.com/cueformation/cfn-cue/internal/cfn"
	"github.com/cueformation/cfn-cue/internal/generate"
)

type Options struct {
	Schemas, Region, Resource string
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
}

var logicalID = regexp.MustCompile(`^[A-Za-z0-9]{1,255}$`)
var schemaID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Check has no cancellation point inside CUE's evaluator. Call it in a process
// with a deadline; the CLI and corpus runner enforce that boundary.
func Check(data []byte, opt Options) (result Result) {
	start := time.Now()
	result = Result{Status: "error", Phase: "input", Region: opt.Region, Resource: opt.Resource}
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
	var template map[string]json.RawMessage
	if err := json.Unmarshal(data, &template); err != nil || template == nil {
		return fail("invalid", "input must be a normalized JSON object")
	}
	root, err := filepath.Abs(opt.Schemas)
	if err != nil {
		return fail("error", err.Error())
	}
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return fail("error", err.Error())
	}
	var manifest generate.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Generator != "cfn-cue/v2" {
		return fail("error", "invalid generated manifest")
	}
	mapping, ok := manifest.Regions[opt.Region]
	if !ok {
		return fail("unsupported", "region absent from generated bundle: "+opt.Region)
	}
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
		if !manifest.LanguageExtensions {
			return fail("unsupported", "generate with -language-extensions to check this template")
		}
	}
	var decoded any
	_ = json.Unmarshal(data, &decoded)
	if name := unexpanded(decoded); name != "" {
		return fail("unsupported", name+" requires preprocessing before schema validation")
	}
	var resources map[string]json.RawMessage
	if err := json.Unmarshal(template["Resources"], &resources); err != nil || len(resources) == 0 {
		return fail("invalid", "Resources must be a nonempty object")
	}
	if opt.Resource != "" {
		resource, ok := resources[opt.Resource]
		if !ok {
			return fail("invalid", "resource not found: "+opt.Resource)
		}
		resources = map[string]json.RawMessage{opt.Resource: resource}
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
		if !schemaID.MatchString(hash) {
			return fail("error", "unsafe schema identifier in manifest")
		}
		names = append(names, name)
		types[schemaType] = hash
		schemaTypes[name] = schemaType
	}
	for typ := range actualTypes {
		result.ResourceTypes = append(result.ResourceTypes, typ)
	}
	sort.Strings(result.ResourceTypes)
	if len(invalid) > 0 {
		return fail("invalid", classify(strings.Join(invalid, "\n"), unsupported))
	}
	if len(names) == 0 {
		return fail("unsupported", strings.Join(unsupported, "\n"))
	}
	f := &ast.File{Decls: []ast.Decl{&ast.Package{Name: ast.NewIdent("validation")}}}
	imports := &ast.ImportDecl{}
	if opt.Resource == "" {
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent("base"), manifest.Module+"/template:template"))
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
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent(alias), manifest.Module+"/resources/h"+types[typ]+":schema"))
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
	if err := v.Err(); err != nil {
		return fail("invalid", classify(detail(err), unsupported))
	}
	if err := v.LookupPath(cue.MakePath(cue.Str("value"))).Validate(cue.Concrete(true)); err != nil {
		return fail("invalid", classify(detail(err), unsupported))
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

func detail(err error) string {
	message := errors.Details(err, nil)
	if len(message) > 8000 {
		message = message[:8000] + "\n[diagnostics truncated]"
	}
	return message
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
