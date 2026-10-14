package generate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
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
	rules            []*fieldRules
	iam              bool
	iamHits          map[string]bool
	hoisted          map[string]bool
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
	if profile, ok := iamPolicies[c.resource][path]; ok && c.template && c.iam {
		// The profile already admits intrinsics; replacing the provider
		// schema avoids intersecting two open policy disjunctions.
		c.iamHits[path] = true
		return iamRef(profile)
	}
	s, ok := v.(map[string]any)
	if !ok {
		if c.template && lift && v == true {
			return map[string]any{"$ref": "cue://intrinsics/Value"}
		}
		return v
	}
	m := map[string]any{}
	rules := &fieldRules{}
	for _, k := range keys(s) {
		x := s[k]
		p := path + "/" + pointer(k)
		switch {
		case includes(annotations, k):
			continue
		case k == "requiredXor" && ruleNames(s, stringsOf(x)):
			rules.exactlyOne(stringsOf(x))
		case k == "requiredOr" && ruleNames(s, stringsOf(x)):
			rules.atLeast = append(rules.atLeast, stringsOf(x))
		case k == "oneOf" && ruleNames(s, singleRequired(x)):
			rules.exactlyOne(singleRequired(x))
		case k == "anyOf" && ruleNames(s, singleRequired(x)):
			rules.atLeast = append(rules.atLeast, singleRequired(x))
		case k == "dependentExcluded" && isObject(x) && ruleNames(s, excludedNames(x)):
			rules.exclude(x.(map[string]any))
		case k == "$schema" || k == "$id":
			continue // provider dialects become one explicit 2020-12 document
		case k == "$ref":
			r, ok := x.(string)
			if !ok {
				m[k] = x
				continue
			}
			if strings.HasPrefix(r, "#") {
				m[k] = c.localRef(r)
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
			if k == "anyOf" {
				a = deduplicateAnyOf(a)
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
			c.normalizeFormat(m, p, x)
		case includes(standard, k):
			m[k] = x
		default:
			c.warn(p, k, "unsupported validation keyword omitted")
		}
	}
	c.mark(m, rules)
	// Merge generated allOf constraints with an existing allOf regardless of key order.
	if a, ok := m["_extra"].([]any); ok {
		delete(m, "_extra")
		m["allOf"] = append(asArray(m["allOf"]), a...)
	}
	if !lift || !c.template {
		return m
	}
	if onlyReference(m) && localReferenceIsLifted(s, root) {
		return m
	}
	if len(m) == 0 {
		return map[string]any{"$ref": "cue://intrinsics/Value"}
	}
	// Literal alternatives already belong to m. Adding #Value here would
	// duplicate open objects and arrays, leaving nested values ambiguous in
	// CUE v0.18, and would let literals bypass the property's constraints.
	classes := schemaClasses(s, root, map[string]bool{})
	if len(classes) == 0 {
		classes = []string{"ValueExpression"}
	} else if len(classes) > 1 {
		name := ""
		for _, class := range []string{"String", "Number", "Boolean", "List", "Object"} {
			if includes(classes, class+"Expression") {
				name += class
			}
		}
		classes = []string{name + "Expression"}
	}
	alternatives := []any{m}
	if includes(schemaTypes(s), "object") && s["additionalProperties"] == nil && s["properties"] == nil && s["patternProperties"] == nil {
		// Generic objects must use the same literal/expression distinction as
		// #Value. Otherwise an intrinsic also matches an open object, leaving
		// intersections with typed properties ambiguous. Split mixed types so
		// the object guard does not constrain their scalar/array alternatives.
		object := clone(m).(map[string]any)
		object["type"] = "object"
		object["allOf"] = append(asArray(object["allOf"]), map[string]any{"$ref": "cue://intrinsics/Object"})
		alternatives = []any{object}
		var otherTypes []any
		for _, typ := range schemaTypes(s) {
			if typ != "object" {
				otherTypes = append(otherTypes, typ)
			}
		}
		if len(otherTypes) > 0 {
			other := clone(m).(map[string]any)
			other["type"] = otherTypes
			alternatives = append(alternatives, other)
		}
	}
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
	preservePatternReferences(s)
	c.iamHits, c.rules, c.hoisted = map[string]bool{}, nil, hoistable(s)
	n := c.normalize(s, "#", false, s, map[string]bool{})
	if template && c.iam {
		for _, path := range sortedStringKeys(iamPolicies[c.resource]) {
			if !c.iamHits[path] {
				c.warn(path, "iamPolicy", "IAM policy placement not found; property keeps its provider schema")
			}
		}
		c.selectPolicy(n.(map[string]any))
	}
	if template {
		c.pruneDefinitions(n.(map[string]any))
	}
	wrapper := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": c.hoist(n.(map[string]any)), "$ref": "#/$defs/Properties"}
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
		if loc.ID.Scheme == "cue" && loc.ID.Host == "iam" {
			return c.module + "/iam:iam", cue.MakePath(cue.Def(strings.TrimPrefix(loc.ID.Path, "/"))), nil
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
		propertiesFirst(f)
		simplifyApplicators(f)
		if e = c.applyRules(f); e != nil {
			return nil, e
		}
		if template {
			if n := addScalarCompatibility(f); n > 0 {
				c.warn("#", "scalarCompatibility", fmt.Sprintf("quoted scalar coercion not applied to %d composed scalar constraints; exact constraints retained", n))
			}
		}
		e = astutil.Sanitize(f)
	}
	return f, e
}

// anyOf and allOf are direct CUE operations. Keeping the extractor's matchN
// wrappers for these makes recursive intrinsic arguments repeatedly evaluate
// entire schemas. Required-key presence predicates retain matchN so absent
// fields do not survive as incomplete alternatives. oneOf/not also retain it.
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
			if n, ok := bound.X.(*ast.BasicLit); ok && n.Value == "1" && !presenceOnlyAlternatives(args.Elts) {
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

// definitionName matches provider definitions that become package-level
// definitions such as #Tag. Names starting with an underscore are generated.
var definitionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// hoistable lists the provider definitions to place beside #Properties. The
// extractor names only direct children of $defs; anything deeper becomes a
// hidden _#defs entry that other packages cannot reference. Names that would
// replace #Properties or #Resource stay nested.
func hoistable(root map[string]any) map[string]bool {
	out := map[string]bool{}
	defs, _ := root["definitions"].(map[string]any)
	for name := range defs {
		if definitionName.MatchString(name) && name != "Properties" && name != "Resource" {
			out[name] = true
		}
	}
	return out
}

// localRef rewrites a provider reference for the wrapper built by hoist.
func (c *converter) localRef(r string) string {
	if rest, ok := strings.CutPrefix(r, "#/definitions/"); ok {
		if name, _, _ := strings.Cut(rest, "/"); c.hoisted[name] {
			return "#/$defs/" + rest
		}
	}
	return "#/$defs/Properties" + strings.TrimPrefix(r, "#")
}

// hoist moves hoistable definitions out of the normalized root schema.
func (c *converter) hoist(root map[string]any) map[string]any {
	out := map[string]any{"Properties": root}
	defs, _ := root["definitions"].(map[string]any)
	for name := range c.hoisted {
		if def, ok := defs[name]; ok {
			out[name] = def
			delete(defs, name)
		}
	}
	if defs != nil && len(defs) == 0 {
		delete(root, "definitions")
	}
	return out
}

// pruneDefinitions deletes provider definitions the template input schema no
// longer reaches, such as those used only by read-only properties or by
// policies replaced with IAM profiles. Only #Resource is referenced from other
// packages; literal packages keep the full set. Field rules placed inside a
// pruned definition are dropped so applyRules does not expect them.
func (c *converter) pruneDefinitions(root map[string]any) {
	defs, _ := root["definitions"].(map[string]any)
	if len(defs) == 0 {
		return
	}
	reached := map[string]bool{}
	var pending []string
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if r, ok := v["$ref"].(string); ok {
				if name, ok := definitionRef(r); ok && !reached[name] {
					if _, ok := defs[name]; ok {
						reached[name] = true
						pending = append(pending, name)
					}
				}
			}
			for _, x := range v {
				visit(x)
			}
		case []any:
			for _, x := range v {
				visit(x)
			}
		}
	}
	rest := map[string]any{}
	for k, v := range root {
		if k != "definitions" {
			rest[k] = v
		}
	}
	visit(rest)
	for i := 0; i < len(pending); i++ {
		visit(defs[pending[i]])
	}
	for _, name := range keys(defs) {
		if reached[name] {
			continue
		}
		walkSchemaObjects(defs[name], func(s map[string]any) {
			props, _ := s["properties"].(map[string]any)
			for prop := range props {
				var index int
				if _, e := fmt.Sscanf(prop, ruleMarker+"%d", &index); e == nil && index < len(c.rules) {
					c.rules[index] = nil
				}
			}
		})
		delete(defs, name)
	}
	if len(defs) == 0 {
		delete(root, "definitions")
	}
}

// definitionRef returns the provider definition a reference written by
// localRef points into. The reference is a JSON Pointer in a URI fragment, so
// the whole fragment is percent-decoded before it is split into tokens and ~1
// and ~0 are unescaped.
func definitionRef(r string) (string, bool) {
	if decoded, e := url.PathUnescape(r); e == nil {
		r = decoded
	}
	rest, ok := strings.CutPrefix(r, "#/$defs/Properties/definitions/")
	if ok {
		name, _, _ := strings.Cut(rest, "/")
		return strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~"), true
	}
	if rest, ok = strings.CutPrefix(r, "#/$defs/"); ok {
		name, _, _ := strings.Cut(rest, "/")
		return name, name != "Properties"
	}
	return "", false
}

// propertiesFirst moves #Properties, which carries the resource description,
// ahead of the hoisted definitions the extractor emits in name order.
func propertiesFirst(f *ast.File) {
	first := -1
	for i, d := range f.Decls {
		field, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		if first < 0 {
			first = i
		}
		if label, ok := field.Label.(*ast.Ident); ok && label.Name == "#Properties" {
			copy(f.Decls[first+1:i+1], f.Decls[first:i])
			f.Decls[first] = field
			return
		}
	}
}

// onlyReference reports whether m is a $ref with at most documentation
// beside it. The referenced definition already admits intrinsics, so lifting
// the reference again would make every intrinsic match two alternatives.
func onlyReference(m map[string]any) bool {
	if _, ok := m["$ref"]; !ok {
		return false
	}
	for k := range m {
		if k != "$ref" && k != "description" && k != "title" && k != "$comment" && k != "examples" {
			return false
		}
	}
	return true
}
