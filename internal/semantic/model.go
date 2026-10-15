package semantic

import (
	"regexp"
	"strconv"
	"strings"
)

// Pseudo parameters valid in Ref and Fn::Sub. Their values are known only at
// deployment, so the pass never substitutes them.
var pseudoParameters = map[string]bool{
	"AWS::AccountId": true, "AWS::NotificationARNs": true, "AWS::NoValue": true,
	"AWS::Partition": true, "AWS::Region": true, "AWS::StackId": true,
	"AWS::StackName": true, "AWS::URLSuffix": true,
}

// Kind names the construct that makes a reference.
type Kind string

const (
	KindRef       Kind = "Ref"
	KindGetAtt    Kind = "Fn::GetAtt"
	KindSub       Kind = "Fn::Sub"
	KindDependsOn Kind = "DependsOn"
	KindCondition Kind = "Condition"
	KindFindInMap Kind = "Fn::FindInMap"
)

// Reference is one symbol use found in the template.
type Reference struct {
	Kind Kind
	// Target is the referenced parameter, resource, condition or mapping.
	Target string
	// Attribute is a GetAtt name, or the attribute part of a dotted Fn::Sub
	// variable. AttributeUnresolved means an intrinsic computes it.
	Attribute           string
	AttributeUnresolved bool
	// Keys are FindInMap's top- and second-level keys. A nil entry is
	// computed by an intrinsic. HasDefault records a LanguageExtensions
	// DefaultValue argument.
	Keys       []*string
	HasDefault bool
	// Section and Owner locate the declaring entry, such as Resources/Bucket.
	Section, Owner string
	Path           []string
	// Guards are the conditions under which this reference is evaluated:
	// the owner's Condition attribute and enclosing Fn::If branches.
	Guards []Guard
}

// Resource is a declared resource. Type is empty when it is not a string.
type Resource struct {
	Type, Condition string
}

// Model is the symbol table and reference list of one template. It is shared
// infrastructure: checks read it without re-walking the template.
type Model struct {
	Parameters map[string]bool
	Resources  map[string]Resource
	Mappings   map[string]any
	Conditions *Conditions
	References []Reference
}

// Analyze builds a model from a decoded JSON template. Malformed sections are
// skipped; the structural schema reports them.
func Analyze(template map[string]any) *Model {
	section := func(name string) map[string]any {
		m, _ := template[name].(map[string]any)
		return m
	}
	m := &Model{Parameters: map[string]bool{}, Resources: map[string]Resource{}, Mappings: section("Mappings")}
	allowed := map[string]map[string]bool{}
	for name, v := range section("Parameters") {
		m.Parameters[name] = true
		p, _ := v.(map[string]any)
		values, _ := p["AllowedValues"].([]any)
		if typ, _ := p["Type"].(string); len(values) == 0 || typ == "CommaDelimitedList" || strings.HasPrefix(typ, "List<") {
			continue
		}
		set := map[string]bool{}
		for _, value := range values {
			if l, ok := literal(value); ok {
				set[l] = true
			}
		}
		allowed[canonical(map[string]any{"Ref": name})] = set
	}
	conditions := section("Conditions")
	m.Conditions = newConditions(conditions, allowed)
	resources := section("Resources")
	for name, v := range resources {
		r, _ := v.(map[string]any)
		typ, _ := r["Type"].(string)
		condition, _ := r["Condition"].(string)
		m.Resources[name] = Resource{typ, condition}
	}
	w := &walker{m: m}
	for _, name := range sortedKeys(conditions) {
		w.value(conditions[name], scope{section: "Conditions", owner: name, path: []string{"Conditions", name}, conditions: true})
	}
	for _, name := range sortedKeys(resources) {
		r, _ := resources[name].(map[string]any)
		w.entry(r, scope{section: "Resources", owner: name, path: []string{"Resources", name}})
	}
	outputs := section("Outputs")
	for _, name := range sortedKeys(outputs) {
		o, _ := outputs[name].(map[string]any)
		w.entry(o, scope{section: "Outputs", owner: name, path: []string{"Outputs", name}})
	}
	rules := section("Rules")
	for _, name := range sortedKeys(rules) {
		w.value(rules[name], scope{section: "Rules", owner: name, path: []string{"Rules", name}})
	}
	return m
}

type scope struct {
	section, owner string
	path           []string
	guards         []Guard
	// conditions makes {"Condition": name} a reference, as it is only
	// inside the Conditions section.
	conditions bool
}

func (s scope) child(key string) scope {
	s.path = append(s.path[:len(s.path):len(s.path)], key)
	return s
}

func (s scope) guarded(g Guard) scope {
	s.guards = append(s.guards[:len(s.guards):len(s.guards)], g)
	return s
}

type walker struct{ m *Model }

func (w *walker) add(r Reference, s scope) {
	r.Section, r.Owner, r.Path, r.Guards = s.section, s.owner, s.path, s.guards
	w.m.References = append(w.m.References, r)
}

// entry walks a resource or output. Its Condition attribute guards every
// value in it, and DependsOn names resources directly.
func (w *walker) entry(e map[string]any, s scope) {
	if name, ok := e["Condition"].(string); ok {
		w.add(Reference{Kind: KindCondition, Target: name}, s.child("Condition"))
		s = s.guarded(Guard{name, true})
	}
	switch d := e["DependsOn"].(type) {
	case string:
		w.add(Reference{Kind: KindDependsOn, Target: d}, s.child("DependsOn"))
	case []any:
		for i, x := range d {
			if name, ok := x.(string); ok {
				w.add(Reference{Kind: KindDependsOn, Target: name}, s.child("DependsOn").child(strconv.Itoa(i)))
			}
		}
	}
	for _, key := range sortedKeys(e) {
		switch key {
		case "Type", "Condition", "DependsOn":
		default:
			w.value(e[key], s.child(key))
		}
	}
}

func (w *walker) value(v any, s scope) {
	switch v := v.(type) {
	case []any:
		for i, x := range v {
			w.value(x, s.child(strconv.Itoa(i)))
		}
	case map[string]any:
		if len(v) == 1 {
			for key, arg := range v {
				if key == "Ref" || strings.HasPrefix(key, "Fn::") || (key == "Condition" && s.conditions) {
					w.intrinsic(key, arg, s.child(key))
					return
				}
			}
		}
		for _, key := range sortedKeys(v) {
			w.value(v[key], s.child(key))
		}
	}
}

func (w *walker) intrinsic(key string, arg any, s scope) {
	args, _ := arg.([]any)
	switch key {
	case "Ref", "Condition":
		if name, ok := arg.(string); ok {
			kind := KindRef
			if key == "Condition" {
				kind = KindCondition
			}
			w.add(Reference{Kind: kind, Target: name}, s)
			return
		}
	case "Fn::GetAtt":
		if name, ok := arg.(string); ok {
			if resource, attribute, ok := strings.Cut(name, "."); ok {
				w.add(Reference{Kind: KindGetAtt, Target: resource, Attribute: attribute}, s)
			}
			return
		}
		if len(args) == 2 {
			if resource, ok := args[0].(string); ok {
				attribute, known := args[1].(string)
				w.add(Reference{Kind: KindGetAtt, Target: resource, Attribute: attribute, AttributeUnresolved: !known}, s)
				if !known {
					w.value(args[1], s.child("1"))
				}
				return
			}
		}
	case "Fn::Sub":
		text, ok := arg.(string)
		var bound map[string]any
		if len(args) == 2 {
			text, ok = args[0].(string)
			bound, _ = args[1].(map[string]any)
			if !ok {
				w.value(args[0], s.child("0"))
			}
			for _, name := range sortedKeys(bound) {
				w.value(bound[name], s.child("1").child(name))
			}
			if bound == nil {
				w.value(args[1], s.child("1"))
			}
		}
		if ok {
			for _, name := range SubVariables(text) {
				if _, ok := bound[name]; ok {
					continue
				}
				target, attribute, _ := strings.Cut(name, ".")
				w.add(Reference{Kind: KindSub, Target: target, Attribute: attribute}, s)
			}
		}
		return
	case "Fn::If":
		if len(args) == 3 {
			if name, ok := args[0].(string); ok {
				w.add(Reference{Kind: KindCondition, Target: name}, s.child("0"))
				w.value(args[1], s.child("1").guarded(Guard{name, true}))
				w.value(args[2], s.child("2").guarded(Guard{name, false}))
				return
			}
		}
	case "Fn::FindInMap":
		if len(args) >= 3 {
			if name, ok := args[0].(string); ok {
				r := Reference{Kind: KindFindInMap, Target: name, HasDefault: len(args) == 4}
				for i, x := range args[1:3] {
					if key, ok := x.(string); ok {
						r.Keys = append(r.Keys, &key)
					} else {
						r.Keys = append(r.Keys, nil)
						w.value(x, s.child(strconv.Itoa(i+1)))
					}
				}
				w.add(r, s)
				if len(args) == 4 {
					w.value(args[3], s.child("3"))
				}
				return
			}
		}
	}
	w.value(arg, s)
}

// subVariable follows cfn-lint's REGEX_SUB_PARAMETERS: surrounding whitespace
// is ignored and ${!Literal} is an escape, not a variable.
var subVariable = regexp.MustCompile(`\$\{\s*([^!\s].*?)\s*\}`)

// SubVariables returns the variable names in a Fn::Sub string.
func SubVariables(text string) []string {
	var names []string
	for _, match := range subVariable.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}
