package semantic

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// secureStringProperties are the resource properties where CloudFormation
// resolves {{resolve:ssm-secure:...}}, as cfn-lint 1.57.2 lists them for
// E1027. Each is a resource type and the property path below Properties; "*"
// is any list item.
var secureStringProperties = map[string]bool{
	"AWS::DirectoryService::MicrosoftAD/Password":                                    true,
	"AWS::DirectoryService::SimpleAD/Password":                                       true,
	"AWS::ElastiCache::ReplicationGroup/AuthToken":                                   true,
	"AWS::IAM::User/LoginProfile/Password":                                           true,
	"AWS::KinesisFirehose::DeliveryStream/RedshiftDestinationConfiguration/Password": true,
	"AWS::OpsWorks::App/AppSource/Password":                                          true,
	"AWS::OpsWorks::Stack/RdsDbInstances/*/DbPassword":                               true,
	"AWS::OpsWorks::Stack/CustomCookbooksSource/Password":                            true,
	"AWS::RDS::DBCluster/MasterUserPassword":                                         true,
	"AWS::RDS::DBInstance/MasterUserPassword":                                        true,
	"AWS::Redshift::Cluster/MasterUserPassword":                                      true,
}

// dynamicReference is cfn-lint's REGEX_DYN_REF with Python's $, which also
// matches before a final newline. Like cfn-lint, it reads one line, and its
// greedy prefix selects the last reference in the string.
var dynamicReference = regexp.MustCompile(`^.*\{\{(resolve:.+)\}\}.*\n?$`)

// opaqueFunctions are the intrinsics whose arguments cfn-lint does not check
// for dynamic references. Fn::If is transparent: each branch is a value of
// the enclosing property. Condition functions such as Fn::Equals are not
// intrinsics here, so their arguments are checked.
var opaqueFunctions = map[string]bool{
	"Ref": true, "Fn::Base64": true, "Fn::Cidr": true, "Fn::Contains": true,
	"Fn::FindInMap": true, "Fn::GetAtt": true, "Fn::GetAZs": true,
	"Fn::GetStackOutput": true, "Fn::ImportValue": true, "Fn::Join": true,
	"Fn::Length": true, "Fn::Select": true, "Fn::Split": true, "Fn::Sub": true,
	"Fn::ToJsonString": true, "Fn::Transform": true,
}

// secureStrings reports ssm-secure dynamic references outside the listed
// properties, anywhere in the template. Properties of a resource without a
// Type are skipped, since their property paths are unknown.
func (c *checker) secureStrings(template map[string]any) {
	resources, _ := template["Resources"].(map[string]any)
	for _, section := range sortedKeys(template) {
		if section != "Resources" {
			c.secureValue(template[section], []string{section}, nil)
			continue
		}
		for _, name := range sortedKeys(resources) {
			r, _ := resources[name].(map[string]any)
			typ, _ := r["Type"].(string)
			for _, key := range sortedKeys(r) {
				path := []string{"Resources", name, key}
				if key != "Properties" {
					c.secureValue(r[key], path, nil)
				} else if typ != "" {
					c.secureValue(r[key], path, []string{typ})
				}
			}
		}
	}
}

// secureValue walks one value. path is the JSON path; property is the type
// and property path below Properties, or nil outside resource properties.
func (c *checker) secureValue(v any, path, property []string) {
	child := func(p []string, s string) []string { return append(p[:len(p):len(p)], s) }
	switch v := v.(type) {
	case string:
		if !strings.Contains(v, "{{resolve:") {
			return
		}
		m := dynamicReference.FindStringSubmatch(v)
		if m == nil || strings.Split(m[1], ":")[1] != "ssm-secure" {
			return
		}
		if property != nil && secureStringProperties[strings.Join(property, "/")] {
			return
		}
		where := "outside resource properties"
		if property != nil {
			where = "in " + property[0] + " property " + strings.Join(property[1:], "/")
		}
		c.report("ssm-secure-placement", Error, path, nil,
			"dynamic reference {{%s}} is %s; CloudFormation resolves ssm-secure only in %d resource properties, such as AWS::RDS::DBInstance MasterUserPassword",
			m[1], where, len(secureStringProperties))
	case []any:
		for i, x := range v {
			var p []string
			if property != nil {
				p = child(property, "*")
			}
			c.secureValue(x, child(path, strconv.Itoa(i)), p)
		}
	case map[string]any:
		if len(v) == 1 {
			for key, arg := range v {
				if opaqueFunctions[key] {
					return
				}
				if args, ok := arg.([]any); ok && key == "Fn::If" && len(args) == 3 {
					// Branches keep the enclosing property path.
					for i := 1; i <= 2; i++ {
						c.secureValue(args[i], child(child(path, key), strconv.Itoa(i)), property)
					}
					return
				}
			}
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var p []string
			if property != nil {
				p = child(property, key)
			}
			c.secureValue(v[key], child(path, key), p)
		}
	}
}
