package generate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/encoding/jsonschema"
)

type Diagnostic struct {
	Resource string `json:"resource"`
	Path     string `json:"path"`
	Keyword  string `json:"keyword"`
	Detail   string `json:"detail"`
}
type converter struct {
	module, resource string
	diagnostics      []Diagnostic
	extensions       bool
	template         bool
}

func (c *converter) warn(path, key, detail string) {
	c.diagnostics = append(c.diagnostics, Diagnostic{c.resource, path, key, detail})
}

var annotations = strings.Fields("typeName primaryIdentifier additionalIdentifiers handlers tagging taggable readOnlyProperties writeOnlyProperties createOnlyProperties conditionalCreateOnlyProperties deprecatedProperties replacementStrategy propertyTransform relationshipRef resourceLink typeConfiguration sourceUrl documentationUrl lifecycle insertionOrder arrayType default")
var standard = strings.Fields("$id $schema $ref $comment title description examples type properties patternProperties additionalProperties required definitions $defs items prefixItems additionalItems allOf anyOf oneOf not if then else enum const minimum maximum exclusiveMinimum exclusiveMaximum multipleOf minLength maxLength pattern format minItems maxItems uniqueItems contains minContains maxContains minProperties maxProperties propertyNames dependencies dependentRequired dependentSchemas")
var unicodeEscape = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)

func re2Pattern(s string) string { return unicodeEscape.ReplaceAllString(s, `\x{$1}`) }

func includes(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func clone(v any) any { b, _ := json.Marshal(v); var out any; _ = json.Unmarshal(b, &out); return out }
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func pointer(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }

// normalize walks schema positions, never arbitrary enum/default/annotation data.
// lift applies only at value boundaries; applicator constraints do not become
// independent escape hatches around required fields or closed objects.
func (c *converter) normalize(v any, path string, lift bool, root map[string]any, visiting map[string]bool) any {
	s, ok := v.(map[string]any)
	if !ok {
		if c.template && lift && v == true {
			return map[string]any{"$ref": "cue://intrinsics/Value"}
		}
		return v
	}
	m := map[string]any{}
	for _, k := range keys(s) {
		x := s[k]
		p := path + "/" + pointer(k)
		switch {
		case includes(annotations, k):
			continue
		case k == "$schema" || k == "$id":
			continue // provider dialects become one explicit 2020-12 document
		case k == "$ref":
			r, ok := x.(string)
			if !ok {
				m[k] = x
				continue
			}
			if strings.HasPrefix(r, "#") {
				m[k] = "#/$defs/Properties" + strings.TrimPrefix(r, "#")
			} else {
				m[k] = r
			}
		case k == "definitions" || k == "$defs" || k == "properties" || k == "patternProperties" || k == "dependentSchemas":
			n := map[string]any{}
			for _, name := range keys(x.(map[string]any)) {
				outName := name
				if k == "patternProperties" {
					outName = re2Pattern(name)
					if _, e := regexp.Compile(outName); e != nil {
						c.warn(p+"/"+pointer(name), k, "pattern is incompatible with RE2; object opened for unmatched keys: "+e.Error())
						m["additionalProperties"] = true
						continue
					}
				}
				n[outName] = c.normalize(x.(map[string]any)[name], p+"/"+pointer(name), k != "dependentSchemas", root, visiting)
			}
			m[k] = n
		case k == "items" && isArray(x):
			a := []any{}
			for i, child := range x.([]any) {
				a = append(a, c.normalize(child, fmt.Sprintf("%s/%d", p, i), true, root, visiting))
			}
			m["prefixItems"] = a
			if tail, exists := s["additionalItems"]; exists {
				m["items"] = c.normalize(tail, path+"/additionalItems", true, root, visiting)
			}
		case k == "additionalItems":
			continue
		case k == "items" || k == "additionalProperties" || k == "contains":
			m[k] = c.normalize(x, p, true, root, visiting)
		case k == "not" || k == "if" || k == "then" || k == "else" || k == "propertyNames":
			m[k] = c.normalize(x, p, false, root, visiting)
		case k == "allOf" || k == "anyOf" || k == "oneOf" || k == "prefixItems":
			a := []any{}
			for i, child := range x.([]any) {
				a = append(a, c.normalize(child, fmt.Sprintf("%s/%d", p, i), k == "prefixItems", root, visiting))
			}
			m[k] = a
		case k == "dependencies":
			n := map[string]any{}
			for _, name := range keys(x.(map[string]any)) {
				child := x.(map[string]any)[name]
				if isArray(child) {
					n[name] = child
				} else {
					n[name] = c.normalize(child, p+"/"+pointer(name), false, root, visiting)
				}
			}
			m[k] = n
		case k == "requiredOr" || k == "requiredXor":
			a := []any{}
			for _, name := range x.([]any) {
				a = append(a, map[string]any{"required": []any{name}})
			}
			kw := "anyOf"
			if k == "requiredXor" {
				kw = "oneOf"
			}
			appendAll(m, map[string]any{kw: a})
		case k == "dependentExcluded":
			for _, name := range keys(x.(map[string]any)) {
				for _, other := range x.(map[string]any)[name].([]any) {
					appendAll(m, map[string]any{"not": map[string]any{"required": []any{name, other}}})
				}
			}
		case k == "enumCaseInsensitive":
			a := []string{}
			for _, val := range x.([]any) {
				a = append(a, regexp.QuoteMeta(fmt.Sprint(val)))
			}
			appendAll(m, map[string]any{"pattern": "(?i)^(?:" + strings.Join(a, "|") + ")$"})
		case k == "uniqueKeys":
			m["uniqueItems"] = true
			c.warn(p, k, "only whole-item uniqueness enforced; uniqueness by selected keys needs semantic validation")
		case k == "pattern":
			pat, ok := x.(string)
			if !ok {
				m[k] = x
				continue
			}
			pat = re2Pattern(pat)
			if _, e := regexp.Compile(pat); e != nil {
				c.warn(p, k, "pattern is incompatible with RE2: "+e.Error())
			} else {
				m[k] = pat
			}
		case k == "format":
			if x == "date-time" || x == "date" || x == "uri" || x == "uri-reference" || x == "regex" {
				m[k] = x
			} else {
				c.warn(p, k, "format annotation not enforced: "+fmt.Sprint(x))
			}
		case includes(standard, k):
			m[k] = x
		default:
			c.warn(p, k, "unsupported validation keyword omitted")
		}
	}
	// Merge generated allOf constraints with an existing allOf regardless of key order.
	if a, ok := m["_extra"].([]any); ok {
		delete(m, "_extra")
		m["allOf"] = append(asArray(m["allOf"]), a...)
	}
	if !lift || !c.template {
		return m
	}
	if len(m) == 0 {
		return map[string]any{"$ref": "cue://intrinsics/Value"}
	}
	if includes(schemaTypes(s), "object") && s["additionalProperties"] == nil && s["properties"] == nil && s["patternProperties"] == nil {
		m["additionalProperties"] = map[string]any{"$ref": "cue://intrinsics/Value"}
	}
	// Literal alternatives already belong to m. Adding #Value here would
	// duplicate open objects and arrays, leaving nested values ambiguous in
	// CUE v0.18, and would let literals bypass the property's constraints.
	classes := schemaClasses(s, root, map[string]bool{})
	if len(classes) == 0 {
		classes = []string{"ValueExpression"}
	}
	alternatives := []any{m}
	for _, class := range classes {
		alternatives = append(alternatives, map[string]any{"$ref": "cue://intrinsics/" + class})
	}
	return map[string]any{"anyOf": alternatives}
}
func isArray(v any) bool                { _, ok := v.([]any); return ok }
func asArray(v any) []any               { a, _ := v.([]any); return a }
func appendAll(m map[string]any, v any) { m["_extra"] = append(asArray(m["_extra"]), v) }
func schemaTypes(s map[string]any) []string {
	if typ, ok := s["type"].(string); ok {
		return []string{typ}
	}
	var types []string
	for _, val := range asArray(s["type"]) {
		if typ, ok := val.(string); ok {
			types = append(types, typ)
		}
	}
	return types
}
func schemaClasses(s, root map[string]any, seen map[string]bool) []string {
	if r, ok := s["$ref"].(string); ok && strings.HasPrefix(r, "#/") && !seen[r] {
		seen[r] = true
		v := any(root)
		for _, p := range strings.Split(r[2:], "/") {
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")]
		}
		if m, ok := v.(map[string]any); ok {
			return schemaClasses(m, root, seen)
		}
	}
	var classes []string
	for _, typ := range schemaTypes(s) {
		class := map[string]string{"string": "StringExpression", "number": "NumberExpression", "integer": "NumberExpression", "boolean": "BooleanExpression", "array": "ListExpression", "object": "ObjectExpression"}[typ]
		if class != "" && !includes(classes, class) {
			classes = append(classes, class)
		}
	}
	return classes
}

func (c *converter) extract(root map[string]any, template bool) (*ast.File, error) {
	c.template = template
	s := clone(root).(map[string]any)
	s["type"] = "object"
	if template {
		c.removeReadOnly(s)
	}
	n := c.normalize(s, "#", false, s, map[string]bool{})
	wrapper := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": map[string]any{"Properties": n}, "$ref": "#/$defs/Properties"}
	b, e := json.Marshal(wrapper)
	if e != nil {
		return nil, e
	}
	v := cuecontext.New().CompileBytes(b)
	if e = v.Err(); e != nil {
		return nil, e
	}
	cfg := &jsonschema.Config{PkgName: "schema", DefaultVersion: jsonschema.VersionDraft2020_12, MapRef: func(loc jsonschema.SchemaLoc) (string, cue.Path, error) {
		if loc.ID.Scheme == "cue" && loc.ID.Host == "intrinsics" {
			return c.module + "/fn:fn", cue.MakePath(cue.Def(strings.TrimPrefix(loc.ID.Path, "/"))), nil
		}
		if !loc.IsLocal {
			return "", cue.Path{}, fmt.Errorf("external schema reference requires explicit resolution: %s", loc.ID)
		}
		return jsonschema.DefaultMapRef(loc)
	}}
	cfg.StrictFeatures = true
	f, e := jsonschema.Extract(v, cfg)
	if e != nil {
		c.warn("#", "jsonschema", "CUE extractor approximation: "+e.Error())
		cfg.StrictFeatures = false
		f, e = jsonschema.Extract(v, cfg)
	}
	if e == nil {
		simplifyApplicators(f)
	}
	return f, e
}

// anyOf and allOf are direct CUE operations. Keeping the extractor's matchN
// wrappers for these makes recursive intrinsic arguments repeatedly evaluate
// entire schemas. oneOf/not keep matchN so exclusivity is preserved.
func simplifyApplicators(f *ast.File) {
	astutil.Apply(f, nil, func(cur astutil.Cursor) bool {
		call, ok := cur.Node().(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		fun, ok := call.Fun.(*ast.Ident)
		if !ok || fun.Name != "matchN" {
			return true
		}
		args, ok := call.Args[1].(*ast.ListLit)
		if !ok || len(args.Elts) == 0 {
			return true
		}
		if bound, ok := call.Args[0].(*ast.UnaryExpr); ok && bound.Op == token.GEQ {
			if n, ok := bound.X.(*ast.BasicLit); ok && n.Value == "1" {
				cur.Replace(union(args.Elts...))
			}
		}
		return true
	})
}

// Provider read-only pointers name instance paths, not paths through the schema.
// Inline a referenced shape before editing it so a shared writable use stays intact.
func (c *converter) removeReadOnly(root map[string]any) {
	// Some writable properties refer to the schema of a read-only property.
	// Preserve those target schemas as private definitions before removing
	// the corresponding instance field from template inputs.
	defs, _ := root["definitions"].(map[string]any)
	if defs == nil {
		defs = map[string]any{}
		root["definitions"] = defs
	}
	saved := map[string]string{}
	var rewrite func(any)
	rewrite = func(v any) {
		switch v := v.(type) {
		case []any:
			for _, x := range v {
				rewrite(x)
			}
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, "#/properties/") {
				name, exists := saved[ref]
				if !exists {
					if target, ok := resolvePointer(root, ref); ok {
						sum := sha256.Sum256([]byte(ref))
						name = fmt.Sprintf("_PropertyRef%x", sum[:8])
						saved[ref] = name
						defs[name] = clone(target)
						rewrite(defs[name])
					}
				}
				if name != "" {
					v["$ref"] = "#/definitions/" + name
				}
			}
			for k, x := range v {
				if k != "definitions" {
					rewrite(x)
				}
			}
		}
	}
	rewrite(root)
	for _, name := range keys(defs) {
		rewrite(defs[name])
	}
	for _, v := range asArray(root["readOnlyProperties"]) {
		p, ok := v.(string)
		if !ok {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(p, "/properties/"), "/")
		for i := range parts {
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
		}
		if !removeInstancePath(root, root, parts, 0) {
			c.warn("#"+p, "readOnlyProperties", "read-only path could not be resolved; retained for review")
		}
	}
}
func resolvePointer(root map[string]any, ref string) (map[string]any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var v any = root
	for _, p := range strings.Split(ref[2:], "/") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v = m[strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")]
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func validateSchema(v any, path string) error {
	if _, ok := v.(bool); ok {
		return nil
	}
	s, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a schema object or boolean", path)
	}
	for _, k := range keys(s) {
		x := s[k]
		p := path + "/" + pointer(k)
		switch k {
		case "properties", "patternProperties", "definitions", "$defs", "dependentSchemas":
			m, ok := x.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be an object", p)
			}
			for _, n := range keys(m) {
				if e := validateSchema(m[n], p+"/"+pointer(n)); e != nil {
					return e
				}
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			a, ok := x.([]any)
			if !ok {
				return fmt.Errorf("%s must be an array", p)
			}
			for i, v := range a {
				if e := validateSchema(v, fmt.Sprintf("%s/%d", p, i)); e != nil {
					return e
				}
			}
		case "items":
			if a, ok := x.([]any); ok {
				for i, v := range a {
					if e := validateSchema(v, fmt.Sprintf("%s/%d", p, i)); e != nil {
						return e
					}
				}
			} else {
				if e := validateSchema(x, p); e != nil {
					return e
				}
			}
		case "additionalProperties", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames":
			if e := validateSchema(x, p); e != nil {
				return e
			}
		case "required", "requiredOr", "requiredXor", "enumCaseInsensitive", "readOnlyProperties":
			if e := stringArray(x, p); e != nil {
				return e
			}
		case "dependencies", "dependentExcluded", "dependentRequired":
			m, ok := x.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be an object", p)
			}
			for _, n := range keys(m) {
				if k == "dependencies" && !isArray(m[n]) {
					if e := validateSchema(m[n], p+"/"+pointer(n)); e != nil {
						return e
					}
				} else {
					if e := stringArray(m[n], p+"/"+pointer(n)); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
func stringArray(v any, path string) error {
	a, ok := v.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array of strings", path)
	}
	for _, v := range a {
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s must contain only strings", path)
		}
	}
	return nil
}
func removeInstancePath(s, root map[string]any, parts []string, depth int) bool {
	if len(parts) == 0 || depth > 100 {
		return false
	}
	if ref, ok := s["$ref"].(string); ok {
		target, ok := resolvePointer(root, ref)
		if !ok {
			return false
		}
		target = clone(target).(map[string]any)
		delete(s, "$ref")
		for k, v := range target {
			if _, exists := s[k]; !exists {
				s[k] = v
			}
		}
	}
	if parts[0] == "*" {
		if item, ok := s["items"].(map[string]any); ok {
			return removeInstancePath(item, root, parts[1:], depth+1)
		}
		return false
	}
	ps, ok := s["properties"].(map[string]any)
	if !ok {
		return false
	}
	child, exists := ps[parts[0]]
	if !exists {
		return true
	}
	if len(parts) == 1 {
		delete(ps, parts[0])
		if req, ok := s["required"].([]any); ok {
			out := []any{}
			for _, p := range req {
				if p != parts[0] {
					out = append(out, p)
				}
			}
			s["required"] = out
		}
		return true
	}
	sub, ok := child.(map[string]any)
	if !ok {
		return false
	}
	return removeInstancePath(sub, root, parts[1:], depth+1)
}
