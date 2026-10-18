// Package semantic checks CloudFormation template references that the
// structural CUE schemas leave unresolved: Ref, Fn::GetAtt, Fn::Sub variables,
// DependsOn, condition names, mapping names and dependency cycles. It also
// checks where ssm-secure dynamic references appear.
//
// The pass reads a decoded, normalized JSON template and never evaluates a
// deployment-time value. Parameters, pseudo parameters, resource attributes
// and imports remain unresolved. Conditions are evaluated only where literal
// comparisons decide them. See docs/semantic-validation.md.
package semantic

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severity of a diagnostic. Errors make a template invalid; warnings record a
// problem that depends on unresolved values.
const (
	Error   = "error"
	Warning = "warning"
)

// Diagnostic is one semantic finding. Path is a JSON pointer into the template.
type Diagnostic struct {
	Rule       string  `json:"rule"`
	Severity   string  `json:"severity"`
	Path       string  `json:"path"`
	Message    string  `json:"message"`
	Conditions []Guard `json:"conditions,omitempty"`
}

// Options supplies schema data the template does not contain.
type Options struct {
	// Attributes returns the Fn::GetAtt names for a resource Type. A name
	// containing "*" is a pattern; "*" alone accepts every name. ok=false
	// means the names are unknown, and attribute names are not checked.
	Attributes func(resourceType string) (names []string, ok bool)
}

// Check analyzes a template and returns its diagnostics, sorted by path.
func Check(template map[string]any, opt Options) []Diagnostic {
	return CheckModel(Analyze(template), template, opt)
}

// CheckModel runs the reference checks on an existing model.
func CheckModel(m *Model, template map[string]any, opt Options) []Diagnostic {
	c := &checker{m: m, opt: opt, patterns: map[string]*regexp.Regexp{}}
	parameters, _ := template["Parameters"].(map[string]any)
	for _, name := range sortedKeys(parameters) {
		if _, ok := m.Resources[name]; ok {
			c.report("duplicate-logical-id", Error, []string{"Resources", name}, nil,
				"%s is declared as both a parameter and a resource", name)
		}
	}
	for _, name := range sortedUnique(m.Conditions.cycles) {
		c.report("condition-cycle", Error, []string{"Conditions", name}, nil,
			"condition %s refers to itself through Condition", name)
	}
	for _, r := range m.References {
		c.reference(r)
	}
	c.cycles()
	c.secureStrings(template)
	c.duplicateSids(template)
	sort.SliceStable(c.out, func(i, j int) bool {
		if c.out[i].Path != c.out[j].Path {
			return c.out[i].Path < c.out[j].Path
		}
		return c.out[i].Rule < c.out[j].Rule
	})
	return c.out
}

type checker struct {
	m        *Model
	opt      Options
	out      []Diagnostic
	patterns map[string]*regexp.Regexp
}

func (c *checker) report(rule, severity string, path []string, guards []Guard, format string, args ...any) {
	c.out = append(c.out, Diagnostic{Rule: rule, Severity: severity, Path: Pointer(path),
		Message: fmt.Sprintf(format, args...), Conditions: guards})
}

// parameterOnly reports whether a section may refer only to parameters and
// pseudo parameters. Conditions and Rules are evaluated before resources exist.
func parameterOnly(section string) bool { return section == "Conditions" || section == "Rules" }

func (c *checker) reference(r Reference) {
	_, isResource := c.m.Resources[r.Target]
	isParameter := c.m.Parameters[r.Target] || pseudoParameters[r.Target]
	if isResource && !isParameter {
		c.conditionalTarget(r)
	}
	switch r.Kind {
	case KindRef:
		switch {
		case isParameter:
		case isResource && parameterOnly(r.Section):
			c.report("ref-context", Error, r.Path, nil, "Ref to resource %s is not allowed in %s; only parameters and pseudo parameters are available", r.Target, r.Section)
		case !isResource:
			c.report("ref-undefined", Error, r.Path, nil, "Ref target %s is not a parameter, resource or pseudo parameter", r.Target)
		}
	case KindGetAtt:
		c.getAtt(r, "getatt", "Fn::GetAtt", isResource, isParameter)
	case KindSub:
		if r.Attribute != "" {
			c.getAtt(r, "sub", "Fn::Sub variable", isResource, isParameter)
			return
		}
		switch {
		case isParameter:
		case isResource && parameterOnly(r.Section):
			c.report("ref-context", Error, r.Path, nil, "Fn::Sub variable %s names a resource, which is not allowed in %s", r.Target, r.Section)
		case !isResource:
			c.report("sub-undefined", Error, r.Path, nil, "Fn::Sub variable %s is not a parameter, resource, pseudo parameter or listed variable", r.Target)
		}
	case KindDependsOn:
		if !isResource {
			c.report("dependson-undefined", Error, r.Path, nil, "DependsOn target %s is not a resource", r.Target)
		}
	case KindCondition:
		if !c.m.Conditions.Defined(r.Target) {
			c.report("condition-undefined", Error, r.Path, nil, "condition %s is not defined in Conditions", r.Target)
		}
	case KindFindInMap:
		mapping, ok := c.m.Mappings[r.Target]
		if !ok {
			c.report("mapping-undefined", Error, r.Path, nil, "Fn::FindInMap mapping %s is not defined in Mappings", r.Target)
			return
		}
		if r.HasDefault {
			return
		}
		top, _ := mapping.(map[string]any)
		if r.Keys[0] == nil || top == nil {
			return
		}
		second, ok := top[*r.Keys[0]]
		if !ok {
			c.report("mapping-key", Error, r.Path, nil, "mapping %s has no top-level key %s", r.Target, *r.Keys[0])
			return
		}
		values, _ := second.(map[string]any)
		if r.Keys[1] == nil || values == nil {
			return
		}
		if _, ok := values[*r.Keys[1]]; !ok {
			c.report("mapping-key", Error, r.Path, nil, "mapping %s key %s has no second-level key %s", r.Target, *r.Keys[0], *r.Keys[1])
		}
	}
}

// conditionalTarget reports a reference to a resource whose Condition may be
// false where the reference is evaluated. CloudFormation then has no such
// resource. The reference's guards include its owner's Condition and its
// enclosing Fn::If branches, so If [C, Ref R, AWS::NoValue] with R's own
// condition C is never reported. As for cycles, the failure is an error only
// when it occurs for every value of the unresolved parameters.
func (c *checker) conditionalTarget(r Reference) {
	condition := c.m.Resources[r.Target].Condition
	if condition == "" || parameterOnly(r.Section) {
		return
	}
	switch r.Kind {
	case KindRef, KindGetAtt, KindSub, KindDependsOn:
	default:
		return
	}
	guards := append(r.Guards[:len(r.Guards):len(r.Guards)], Guard{condition, false})
	switch c.m.Conditions.Classify(guards) {
	case Never:
	case Always:
		c.report("conditional-target", Error, r.Path, nil,
			"%s refers to %s, which never exists here because condition %s is false", r.Kind, r.Target, condition)
	default:
		c.report("conditional-target", Warning, r.Path, guards,
			"%s refers to %s, which does not exist when condition %s is false", r.Kind, r.Target, condition)
	}
}

func (c *checker) getAtt(r Reference, prefix, label string, isResource, isParameter bool) {
	switch {
	case !isResource && isParameter:
		c.report(prefix+"-undefined", Error, r.Path, nil, "%s target %s is a parameter, not a resource", label, r.Target)
		return
	case !isResource:
		c.report(prefix+"-undefined", Error, r.Path, nil, "%s target %s is not a resource", label, r.Target)
		return
	case parameterOnly(r.Section):
		c.report("ref-context", Error, r.Path, nil, "%s to resource %s is not allowed in %s", label, r.Target, r.Section)
		return
	}
	if r.AttributeUnresolved || c.opt.Attributes == nil {
		return
	}
	typ := c.m.Resources[r.Target].Type
	if typ == "" {
		return
	}
	names, ok := c.opt.Attributes(typ)
	if !ok || c.hasAttribute(names, r.Attribute) {
		return
	}
	c.report("getatt-attribute", Error, r.Path, nil, "%s has no attribute %s for Fn::GetAtt", typ, r.Attribute)
}

func (c *checker) hasAttribute(names []string, attribute string) bool {
	for _, name := range names {
		if name == attribute {
			return true
		}
		if !strings.Contains(name, "*") {
			continue
		}
		re, ok := c.patterns[name]
		if !ok {
			re = regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(name), `\*`, ".*") + "$")
			c.patterns[name] = re
		}
		if re.MatchString(attribute) {
			return true
		}
	}
	return false
}

// Pointer formats a path as a JSON pointer.
func Pointer(path []string) string {
	var b strings.Builder
	for _, p := range path {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1"))
	}
	return b.String()
}

func sortedUnique(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
