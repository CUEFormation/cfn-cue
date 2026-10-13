package generate

import (
	"regexp"
	"strconv"
	"strings"
)

// localReferenceIsLifted reports whether a local reference points to a schema
// position normalize already lifts for template inputs. Lifting both that
// definition and its reference produces duplicate intrinsic alternatives.
func localReferenceIsLifted(s, root map[string]any) bool {
	ref, ok := s["$ref"].(string)
	if !ok {
		return false
	}
	target, ok := localSchemaReference(root, ref)
	if !ok || !target.lifted || !target.retained {
		return false
	}
	switch schema := target.value.(type) {
	case map[string]any:
		return true
	case bool:
		return schema
	default:
		return false
	}
}

type schemaReference struct {
	value            any
	lifted, retained bool
	throughPattern   bool
}

// Applicator children stay unlifted. Walk schema positions, never maps within
// const/enum/annotation data. retained tracks dropped or renamed pattern keys.
func localSchemaReference(root map[string]any, ref string) (schemaReference, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return schemaReference{}, false
	}
	target := schemaReference{retained: true}
	parts := strings.Split(ref[2:], "/")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	var current any = root
	for len(parts) > 0 {
		node, ok := current.(map[string]any)
		if !ok {
			return schemaReference{}, false
		}
		keyword := parts[0]
		parts = parts[1:]
		child, exists := node[keyword]
		if !exists {
			return schemaReference{}, false
		}
		switch keyword {
		case "definitions", "$defs", "properties", "patternProperties", "dependentSchemas", "dependencies":
			children, ok := child.(map[string]any)
			if !ok || len(parts) == 0 {
				return schemaReference{}, false
			}
			if keyword == "patternProperties" {
				target.throughPattern = true
				pattern := re2Pattern(parts[0])
				_, err := regexp.Compile(pattern)
				target.retained = target.retained && pattern == parts[0] && err == nil
			}
			current, exists = children[parts[0]]
			parts = parts[1:]
			if !exists {
				return schemaReference{}, false
			}
			target.lifted = keyword != "dependentSchemas" && keyword != "dependencies"
		case "items", "prefixItems", "allOf", "anyOf", "oneOf":
			if children, ok := child.([]any); ok {
				if len(parts) == 0 {
					return schemaReference{}, false
				}
				index, err := strconv.Atoi(parts[0])
				if err != nil || index < 0 || index >= len(children) || strconv.Itoa(index) != parts[0] {
					return schemaReference{}, false
				}
				current = children[index]
				parts = parts[1:]
				target.lifted = keyword == "items" || keyword == "prefixItems"
			} else {
				if keyword != "items" {
					return schemaReference{}, false
				}
				current, target.lifted = child, true
			}
		case "additionalItems":
			if !isArray(node["items"]) {
				return schemaReference{}, false
			}
			current, target.lifted = child, true
		case "additionalProperties", "contains":
			current, target.lifted = child, true
		case "not", "if", "then", "else", "propertyNames":
			current, target.lifted = child, false
		default:
			return schemaReference{}, false
		}
	}
	target.value = current
	return target, true
}
