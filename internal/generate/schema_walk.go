package generate

import "strings"

// These three lists are the package's definition of where nested schemas
// live. Every other keyword holds data: enum, const, default and examples
// hold instance values, required and dependentRequired hold property names,
// and provider annotations hold whatever the provider wrote. An object in a
// data position is not a schema even when it has keys named $ref, properties
// or items. Each list also applies only to a schema's own keywords: the keys
// of a properties or definitions object are names, so a property called
// "definitions" or "enum" is a schema like any other.
var (
	// Objects whose values are schemas. A dependencies value may instead be
	// an array of property names.
	schemaMapKeywords = strings.Fields("definitions $defs properties patternProperties dependentSchemas dependencies")
	// Arrays of schemas. items belongs here when it is an array.
	schemaListKeywords = strings.Fields("prefixItems allOf anyOf oneOf")
	// A single schema.
	schemaValueKeywords = strings.Fields("items additionalItems additionalProperties contains not if then else propertyNames")
)

// holdsSchemas reports whether keyword's value contains nested schemas.
func holdsSchemas(keyword string) bool {
	return includes(schemaMapKeywords, keyword) || includes(schemaListKeywords, keyword) || includes(schemaValueKeywords, keyword)
}

// schemaChildren calls visit with each value directly nested in a schema
// position of schema, in keyword order. A child is a schema object, a boolean
// schema, or malformed input; visit decides what to do with it.
func schemaChildren(schema map[string]any, visit func(child any)) {
	for _, keyword := range keys(schema) {
		value := schema[keyword]
		switch {
		case includes(schemaMapKeywords, keyword):
			children, _ := value.(map[string]any)
			for _, name := range keys(children) {
				if keyword == "dependencies" && isArray(children[name]) {
					continue
				}
				visit(children[name])
			}
		case includes(schemaListKeywords, keyword) || keyword == "items" && isArray(value):
			for _, child := range asArray(value) {
				visit(child)
			}
		case includes(schemaValueKeywords, keyword):
			visit(value)
		}
	}
}

// walkSchemaObjects calls visit with value and every schema object nested in
// it, parents first. It never enters data positions. visit may replace the
// keywords of the schema it is given; children are read after it returns.
func walkSchemaObjects(value any, visit func(map[string]any)) {
	schema, ok := value.(map[string]any)
	if !ok {
		return
	}
	visit(schema)
	schemaChildren(schema, func(child any) { walkSchemaObjects(child, visit) })
}
