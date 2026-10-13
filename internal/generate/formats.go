package generate

import (
	"fmt"
	"strings"
)

// These format patterns follow cfn-lint v1.57.2 at commit
// be66fb3e224b41c85065cb3002ad0b24e31db535, src/cfnlint/rules/formats.
// The upstream patterns are Copyright Amazon.com, Inc. or its affiliates,
// SPDX-License-Identifier: MIT-0. All patterns below are RE2-compatible;
// unknown format annotations remain explicit generation diagnostics.
//
// In particular, Image.Id intentionally uses [0-9a-z], rather than a stricter
// hexadecimal alphabet, because the comparison target is cfn-lint's rule.
var cloudFormationFormatPatterns = map[string]string{
	"AWS::EC2::VPC.Id":           `^vpc-(([0-9A-Fa-f]{8})|([0-9A-Fa-f]{17}))$`,
	"AWS::EC2::SecurityGroup.Id": `^sg-([a-fA-F0-9]{8}|[a-fA-F0-9]{17})$`,
	"AWS::EC2::Image.Id":         `^ami-([0-9a-z]{8}|[0-9a-z]{17})$`,
	"AWS::IAM::Role.Arn":         `^arn:aws[a-zA-Z-]*:iam::\d{12}:role/.+$`,
	"AWS::KMS::Key.Arn":          `^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:\d{12}:(key|alias)/.+$`,
	"AWS::SNS::Topic.Arn":        `^arn:aws[a-zA-Z-]*:sns:[a-z0-9-]+:\d{12}:.+$`,
	"AWS::ACM::Certificate.Arn":  `^arn:aws[a-zA-Z-]*:acm:[a-z0-9-]+:\d{12}:certificate/.+$`,
	"AWS::Lambda::Function.Arn":  `^arn:aws[a-zA-Z-]*:lambda:[a-z0-9-]+:\d{12}:function:.+(:.+)?$`,
}

// normalizeFormat adds a literal constraint without replacing an existing
// provider pattern. Intrinsic alternatives are added later at value boundaries
// by normalize, so Ref/GetAtt values do not have to match the literal pattern.
func (c *converter) normalizeFormat(m map[string]any, path string, value any) {
	name, ok := value.(string)
	if !ok {
		c.warn(path, "format", "format annotation not enforced: "+fmt.Sprint(value))
		return
	}
	if pattern, found := cloudFormationFormatPatterns[name]; found {
		// Python's \d matches Unicode decimal digits; RE2's \d only
		// matches ASCII. Nd is the equivalent Unicode category in RE2.
		pattern = strings.ReplaceAll(pattern, `\d`, `\p{Nd}`)
		// Python's regular-expression $ also matches just before a final
		// newline; CUE/RE2's $ only matches at the end of the string.
		pattern = strings.TrimSuffix(pattern, "$") + `\n?$`
		if name == "AWS::EC2::Image.Id" && c.resource == "AWS::EC2::LaunchTemplate" && path == "#/definitions/LaunchTemplateData/properties/ImageId/format" {
			// The format rule exempts any resolve:ssm-prefixed value at
			// this particular LaunchTemplate property. It is not an AMI
			// exception for ordinary EC2 Instance.ImageId properties.
			pattern = "(?:" + pattern + `)|(?s:^resolve:ssm.*$)`
		}
		if c.template {
			// Dynamic references are unresolved template values. Defer
			// this format constraint to deployment, as cfn-lint does.
			// Other provider constraints still apply; placement, syntax,
			// and full constraint deferral need a separate semantic check.
			pattern = "(?:" + pattern + `)|^.*\{\{resolve:(ssm|ssm-secure|secretsmanager):[^\r\n]+\}\}.*$`
		}
		appendAll(m, map[string]any{"pattern": pattern})
		return
	}
	switch name {
	case "date-time", "date", "uri", "uri-reference", "regex":
		m["format"] = name
	default:
		c.warn(path, "format", "format annotation not enforced: "+name)
	}
}
