// Package cfn contains CloudFormation resource naming shared by generation and
// concrete template validation.
package cfn

import (
	"fmt"
	"regexp"
	"strings"
)

const CustomResourceType = "AWS::CloudFormation::CustomResource"
const CustomResourcePattern = `^Custom::[A-Za-z0-9_@-]{1,60}$`

var customResource = regexp.MustCompile(CustomResourcePattern)
var resourceAlias = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func IsCustomResource(name string) bool { return customResource.MatchString(name) }

// SchemaType selects the provider schema while preserving the actual Type in
// the template. Custom resource names share one provider schema.
func SchemaType(name string) string {
	if IsCustomResource(name) {
		return CustomResourceType
	}
	return name
}

// ResourceAliases preserves readable resource type names while checking that
// each import has a valid, unique name. Input order determines error ordering.
func ResourceAliases(types []string) (map[string]string, error) {
	aliases := make(map[string]string, len(types))
	owners := make(map[string]string, len(types))
	for _, typ := range types {
		alias := strings.ReplaceAll(typ, "::", "_")
		if !resourceAlias.MatchString(alias) || alias == "base" || alias == "fn" {
			return nil, fmt.Errorf("resource type %q produces an invalid or reserved import alias %q", typ, alias)
		}
		if previous, ok := owners[alias]; ok && previous != typ {
			return nil, fmt.Errorf("resource types %q and %q produce the same import alias %q", previous, typ, alias)
		}
		owners[alias] = typ
		aliases[typ] = alias
	}
	return aliases, nil
}
