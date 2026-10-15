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
//
// A value must match every pattern listed for its format. cfn-lint's
// S3::Bucket.Name pattern uses lookarounds, which RE2 lacks, so it is split
// into a length and alphabet check and a check that names start and end with
// a letter or digit, with no "..", ".-" or "-." inside. cfn-lint also checks
// Subnet.Id against its LogGroup.Name pattern, which it registers under the
// same format name; every subnet ID matches that pattern, so it is omitted.
// cfn-lint has no rule for the .Ids and .Names list formats.
var cloudFormationFormatPatterns = map[string][]string{
	"AWS::EC2::VPC.Id":             {`^vpc-(([0-9A-Fa-f]{8})|([0-9A-Fa-f]{17}))$`},
	"AWS::EC2::SecurityGroup.Id":   {`^sg-([a-fA-F0-9]{8}|[a-fA-F0-9]{17})$`},
	"AWS::EC2::SecurityGroup.Name": {`^[a-zA-Z0-9 \._\-:\/()#\,@\[\]+=&;\{\}!\$\*]+$`},
	"AWS::EC2::Subnet.Id":          {`^subnet-(([0-9A-Fa-f]{8})|([0-9A-Fa-f]{17}))$`},
	"AWS::EC2::Image.Id":           {`^ami-([0-9a-z]{8}|[0-9a-z]{17})$`},
	"AWS::IAM::Role.Arn":           {`^arn:aws[a-zA-Z-]*:iam::\d{12}:role/.+$`},
	"AWS::KMS::Key.Arn":            {`^arn:aws[a-zA-Z-]*:kms:[a-z0-9-]+:\d{12}:(key|alias)/.+$`},
	"AWS::KMS::Key.Id":             {`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|mrk-[0-9a-f]{32}|alias/[a-zA-Z0-9/_-]+)$`},
	"AWS::KMS::Alias.AliasName":    {`^alias/[a-zA-Z0-9:/_-]+$`},
	"AWS::S3::Bucket.Name":         {`^[a-z0-9.\-]{3,63}$`, `^[a-z0-9](?:[a-z0-9]|-+[a-z0-9]|\.[a-z0-9])*$`},
	"AWS::SNS::Topic.Arn":          {`^arn:aws[a-zA-Z-]*:sns:[a-z0-9-]+:\d{12}:.+$`},
	"AWS::ACM::Certificate.Arn":    {`^arn:aws[a-zA-Z-]*:acm:[a-z0-9-]+:\d{12}:certificate/.+$`},
	"AWS::Lambda::Function.Arn":    {`^arn:aws[a-zA-Z-]*:lambda:[a-z0-9-]+:\d{12}:function:.+(:.+)?$`},
}

// These formats are checked by CUE definitions in the fn package, because a
// regular expression cannot check a network's host bits. cfn-lint's
// extension schemas use the ipv4 and ipv6 address formats.
var cloudFormationFormatDefinitions = map[string]string{
	"ipv4":         "IPv4Address",
	"ipv6":         "IPv6Address",
	"ipv4-network": "IPv4Network",
	"ipv6-network": "IPv6Network",
}

// dynamicReferencePattern matches a string that contains a dynamic
// reference, such as {{resolve:ssm:/name}}. Like cfn-lint, it only searches
// the first line; as with Python's $, one final newline may follow it.
// fn.#DynamicReference uses it.
const dynamicReferencePattern = `^.*\{\{resolve:(ssm|ssm-secure|secretsmanager):[^\r\n]+\}\}.*\n?$`

// normalizeFormat adds a literal constraint without replacing an existing
// provider pattern. Intrinsic alternatives are added later at value boundaries
// by normalize, so Ref/GetAtt values and dynamic references do not have to
// match the literal pattern.
func (c *converter) normalizeFormat(m map[string]any, path string, value any) {
	name, ok := value.(string)
	if !ok {
		c.warn(path, "format", "format annotation not enforced: "+fmt.Sprint(value))
		return
	}
	if patterns, found := cloudFormationFormatPatterns[name]; found {
		for _, pattern := range patterns {
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
			if name == "AWS::S3::Bucket.Name" && c.resource == "AWS::S3::Bucket" && path == "#/properties/BucketName/format" {
				// cfn-lint accepts an empty BucketName, which CloudFormation
				// treats as absent and replaces with a generated name.
				pattern = "(?:" + pattern + ")|^$"
			}
			appendAll(m, map[string]any{"pattern": pattern})
		}
		return
	}
	if definition, found := cloudFormationFormatDefinitions[name]; found {
		appendAll(m, map[string]any{"$ref": "cue://intrinsics/" + definition})
		return
	}
	switch name {
	case "date-time", "date", "uri", "uri-reference", "regex":
		m["format"] = name
	default:
		c.warn(path, "format", "format annotation not enforced: "+name)
	}
}
