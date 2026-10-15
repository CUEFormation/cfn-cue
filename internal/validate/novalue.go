package validate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cueformation/cfn-cue/internal/conditions"
)

// maxVariants bounds the distinct removal sets checked for one resource
// before falling back to checking each removed position on its own.
const maxVariants = 32

// removal is a position in a resource's Properties that AWS::NoValue can
// remove. Each leaf lists the Fn::If choices that select AWS::NoValue there;
// an empty leaf removes the position unconditionally.
type removal struct {
	location []any
	leaves   [][]conditions.Literal
}

// variant is a copy of a resource with one reachable set of removals applied.
type variant struct {
	resource any
	reason   string
	removed  [][]string
}

// noValueVariants returns a copy of the resource for each reachable
// combination of AWS::NoValue removals in its Properties. The schemas accept
// an Fn::If whose branch is AWS::NoValue as a present value; checking these
// copies finds required properties that a reachable branch removes.
func noValueVariants(model *conditions.Model, resource map[string]any) []variant {
	sites := map[string]*removal{}
	var order []string
	var walk func(node any, path, location []any, choices []conditions.Literal)
	walk = func(node any, path, location []any, choices []conditions.Literal) {
		if isNoValue(node) {
			key := pathKey(location)
			if sites[key] == nil {
				sites[key] = &removal{location: location}
				order = append(order, key)
			}
			sites[key].leaves = append(sites[key].leaves, choices)
			return
		}
		switch node := node.(type) {
		case map[string]any:
			if args, ok := fnIf(node); ok {
				name, ok := args[0].(string)
				if !ok {
					return
				}
				for i, value := range []bool{true, false} {
					next := append(append([]conditions.Literal{}, choices...), conditions.Literal{Condition: name, Value: value})
					walk(args[i+1], extend(path, "Fn::If", i+1), location, next)
				}
				return
			}
			if isFunction(node) {
				return
			}
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := extend(path, key)
				walk(node[key], child, child, choices)
			}
		case []any:
			for i, element := range node {
				child := extend(path, i)
				walk(element, child, child, choices)
			}
		}
	}
	properties, ok := resource["Properties"]
	if !ok {
		return nil
	}
	walk(properties, []any{"Properties"}, []any{"Properties"}, nil)
	if len(order) == 0 {
		return nil
	}
	var given []conditions.Literal
	if name, ok := resource["Condition"].(string); ok {
		given = append(given, conditions.Literal{Condition: name, Value: true})
	}
	var names []string
	index := map[string]int{}
	for _, key := range order {
		for _, leaf := range sites[key].leaves {
			for _, choice := range leaf {
				if _, ok := index[choice.Condition]; !ok {
					index[choice.Condition] = len(names)
					names = append(names, choice.Condition)
				}
			}
		}
	}
	holds := func(leaf []conditions.Literal, combo []bool) bool {
		for _, choice := range leaf {
			if combo[index[choice.Condition]] != choice.Value {
				return false
			}
		}
		return true
	}
	if combos, ok := model.Combinations(names, given); ok {
		type group struct {
			removed []*removal
			names   map[string]bool
			combos  [][]bool
			seen    map[string]bool
		}
		var groups []*group
		byID := map[string]*group{}
		for _, combo := range combos {
			var removed []*removal
			var ids []string
			var leaves [][]conditions.Literal
			for _, key := range order {
				for _, leaf := range sites[key].leaves {
					if holds(leaf, combo) {
						if len(ids) == 0 || ids[len(ids)-1] != key {
							removed = append(removed, sites[key])
							ids = append(ids, key)
						}
						leaves = append(leaves, leaf)
					}
				}
			}
			if len(removed) == 0 {
				continue
			}
			id := strings.Join(ids, "\x00")
			g := byID[id]
			if g == nil {
				g = &group{removed: removed, names: map[string]bool{}, seen: map[string]bool{}}
				byID[id] = g
				groups = append(groups, g)
			}
			for _, leaf := range leaves {
				g.seen[fmt.Sprint(leaf)] = true
				for _, choice := range leaf {
					g.names[choice.Condition] = true
				}
			}
			g.combos = append(g.combos, combo)
		}
		if len(groups) <= maxVariants {
			var variants []variant
			for _, g := range groups {
				// Name the conditions that select AWS::NoValue and keep the
				// same value in every combination with this removal set. If
				// one varies, list each branch path that selects it instead.
				var because []conditions.Literal
				varies := false
				for _, name := range names {
					if !g.names[name] {
						continue
					}
					value, constant := g.combos[0][index[name]], true
					for _, combo := range g.combos {
						constant = constant && combo[index[name]] == value
					}
					varies = varies || !constant
					because = append(because, conditions.Literal{Condition: name, Value: value})
				}
				alternatives := [][]conditions.Literal{because}
				if varies {
					alternatives = nil
					for _, site := range g.removed {
						for _, leaf := range site.leaves {
							if key := fmt.Sprint(leaf); g.seen[key] {
								delete(g.seen, key)
								alternatives = append(alternatives, leaf)
							}
						}
					}
				}
				variants = append(variants, makeVariant(resource, g.removed, alternatives))
			}
			return variants
		}
	}
	// Too many combinations to enumerate: check each reachable removal on its
	// own, together with every other removal the same choices force. Other
	// positions keep their Fn::If, which the schemas accept as present. This
	// misses failures that need independent removals at once.
	var variants []variant
	for _, key := range order {
		for _, leaf := range sites[key].leaves {
			if ok, decided := model.Satisfiable(append(append([]conditions.Literal{}, given...), leaf...)); !ok || !decided {
				continue
			}
			var removed []*removal
			for _, other := range order {
				for _, forced := range sites[other].leaves {
					if implies(leaf, forced) {
						removed = append(removed, sites[other])
						break
					}
				}
			}
			variants = append(variants, makeVariant(resource, removed, [][]conditions.Literal{leaf}))
			break
		}
	}
	return variants
}

// implies reports whether every choice in forced is also in leaf.
func implies(leaf, forced []conditions.Literal) bool {
	for _, choice := range forced {
		found := false
		for _, have := range leaf {
			found = found || have == choice
		}
		if !found {
			return false
		}
	}
	return true
}

// removed marks positions for deletion while the copy is still addressed by
// the original paths. strip deletes them afterwards so list indices hold.
var removed = &struct{ byte }{}

func makeVariant(resource map[string]any, sites []*removal, alternatives [][]conditions.Literal) variant {
	out := clone(resource)
	var locations []string
	var paths [][]string
	for _, site := range sites {
		locations = append(locations, formatPath(site.location))
		var path []string
		for _, step := range site.location {
			path = append(path, fmt.Sprint(step))
		}
		paths = append(paths, path)
		parent := any(out)
		for _, step := range site.location[:len(site.location)-1] {
			parent = child(parent, step)
		}
		switch parent := parent.(type) {
		case map[string]any:
			parent[site.location[len(site.location)-1].(string)] = removed
		case []any:
			parent[site.location[len(site.location)-1].(int)] = removed
		}
	}
	reason := "AWS::NoValue removes " + strings.Join(locations, ", ")
	var cases []string
	for _, alternative := range alternatives {
		var parts []string
		for _, choice := range alternative {
			parts = append(parts, fmt.Sprintf("condition %s is %t", choice.Condition, choice.Value))
		}
		if len(parts) > 0 {
			cases = append(cases, "when "+strings.Join(parts, " and "))
		}
	}
	if len(cases) > 0 {
		reason += " " + strings.Join(cases, ", or ")
	}
	return variant{resource: strip(out), reason: reason, removed: paths}
}

func child(node any, step any) any {
	switch node := node.(type) {
	case map[string]any:
		if key, ok := step.(string); ok {
			return node[key]
		}
	case []any:
		if i, ok := step.(int); ok && i < len(node) {
			return node[i]
		}
	}
	return nil
}

func clone(node any) any {
	switch node := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(node))
		for key, value := range node {
			out[key] = clone(value)
		}
		return out
	case []any:
		out := make([]any, len(node))
		for i, value := range node {
			out[i] = clone(value)
		}
		return out
	}
	return node
}

func strip(node any) any {
	switch node := node.(type) {
	case map[string]any:
		for key, value := range node {
			if value == any(removed) {
				delete(node, key)
			} else {
				node[key] = strip(value)
			}
		}
	case []any:
		out := node[:0]
		for _, value := range node {
			if value != any(removed) {
				out = append(out, strip(value))
			}
		}
		return out
	}
	return node
}

func isNoValue(node any) bool {
	object, ok := node.(map[string]any)
	return ok && len(object) == 1 && object["Ref"] == "AWS::NoValue"
}

func fnIf(node map[string]any) ([]any, bool) {
	if len(node) != 1 {
		return nil, false
	}
	args, ok := node["Fn::If"].([]any)
	return args, ok && len(args) == 3
}

// isFunction reports an intrinsic other than Fn::If. Its arguments are not
// property values, so AWS::NoValue inside them is not a removal.
func isFunction(node map[string]any) bool {
	if len(node) != 1 {
		return false
	}
	for key := range node {
		return key == "Ref" || strings.HasPrefix(key, "Fn::")
	}
	return false
}

func extend(path []any, steps ...any) []any {
	return append(append(make([]any, 0, len(path)+len(steps)), path...), steps...)
}

func pathKey(path []any) string {
	return fmt.Sprintf("%#v", path)
}

func formatPath(path []any) string {
	var b strings.Builder
	for _, step := range path {
		switch step := step.(type) {
		case int:
			b.WriteString("[" + strconv.Itoa(step) + "]")
		case string:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(step)
		}
	}
	return b.String()
}
