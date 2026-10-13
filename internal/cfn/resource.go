// Package cfn contains CloudFormation resource naming shared by generation and
// concrete template validation.
package cfn

import "regexp"

const CustomResourceType = "AWS::CloudFormation::CustomResource"
const CustomResourcePattern = `^Custom::[A-Za-z0-9_@-]{1,60}$`

var customResource = regexp.MustCompile(CustomResourcePattern)

func IsCustomResource(name string) bool { return customResource.MatchString(name) }

// SchemaType selects the provider schema while preserving the actual Type in
// the template. Custom resource names share one provider schema.
func SchemaType(name string) string {
	if IsCustomResource(name) {
		return CustomResourceType
	}
	return name
}
