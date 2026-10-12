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

var logicalID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,254}$`)
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
		var single string
		if json.Unmarshal(raw, &single) == nil {
			transforms = []string{single}
		} else if json.Unmarshal(raw, &transforms) != nil {
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
	names := make([]string, 0, len(resources))
	types := map[string]string{}
	for name, raw := range resources {
		if strings.HasPrefix(name, "Fn::ForEach::") {
			return fail("unsupported", "Fn::ForEach requires loop expansion before resource validation")
		}
		if !logicalID.MatchString(name) {
			return fail("invalid", "invalid resource logical ID: "+name)
		}
		var resource struct {
			Type string `json:"Type"`
		}
		if err := json.Unmarshal(raw, &resource); err != nil || resource.Type == "" {
			return fail("invalid", "resource "+name+" must have a string Type")
		}
		hash, ok := mapping[resource.Type]
		if !ok {
			return fail("unsupported", fmt.Sprintf("resource %s: %s is absent from %s", name, resource.Type, opt.Region))
		}
		if !schemaID.MatchString(hash) {
			return fail("error", "unsafe schema identifier in manifest")
		}
		names = append(names, name)
		types[resource.Type] = hash
	}
	sort.Strings(names)
	for typ := range types {
		result.ResourceTypes = append(result.ResourceTypes, typ)
	}
	sort.Strings(result.ResourceTypes)
	aliases := map[string]string{}
	f := &ast.File{Decls: []ast.Decl{&ast.Package{Name: ast.NewIdent("validation")}}}
	imports := &ast.ImportDecl{}
	if opt.Resource == "" {
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent("base"), manifest.Module+"/template:template"))
	}
	for i, typ := range result.ResourceTypes {
		alias := fmt.Sprintf("r%d", i)
		aliases[typ] = alias
		imports.Specs = append(imports.Specs, ast.NewImport(ast.NewIdent(alias), manifest.Module+"/resources/h"+types[typ]+":schema"))
	}
	f.Decls = append(f.Decls, imports)
	selected := &ast.StructLit{}
	for _, name := range names {
		var resource struct{ Type string }
		_ = json.Unmarshal(resources[name], &resource)
		expr, err := cuejson.Extract("input.json", resources[name])
		if err != nil {
			return fail("invalid", err.Error())
		}
		x := ast.NewBinExpr(token.AND, ast.NewSel(ast.NewIdent(aliases[resource.Type]), "#Resource"), expr)
		selected.Elts = append(selected.Elts, &ast.Field{Label: ast.NewString(name), Value: x})
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
		return fail("invalid", detail(err))
	}
	if err := v.LookupPath(cue.MakePath(cue.Str("value"))).Validate(cue.Concrete(true)); err != nil {
		return fail("invalid", detail(err))
	}
	result.Status = "valid"
	return result
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
		for key, child := range node {
			if key == "Fn::Transform" || strings.HasPrefix(key, "Fn::ForEach::") {
				return key
			}
			if name := unexpanded(child); name != "" {
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
