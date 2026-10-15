package generate

import (
	"sort"
	"strings"
)

// Attribute names follow cfn-lint v1.57.2 at commit
// be66fb3e224b41c85065cb3002ad0b24e31db535, src/cfnlint/schema/_getatts.py.
// The type lists below are Copyright Amazon.com, Inc. or its affiliates,
// SPDX-License-Identifier: MIT-0.
//
// The manifest records the names so a semantic pass can check Fn::GetAtt
// without loading CUE. A name containing "*" is a pattern in which "*"
// matches any sequence; "*" alone accepts every attribute.

// GetAtt accepts every property path of these types, not only the read-only
// ones. Arrays of objects or arrays are not addressable.
var attributeAllPropertyTypes = map[string]bool{
	"AWS::Amplify::Branch":                             true,
	"AWS::Amplify::Domain":                             true,
	"AWS::AppSync::DomainName":                         true,
	"AWS::AppSync::FunctionConfiguration":              true,
	"AWS::AppSync::Resolver":                           true,
	"AWS::AutoScaling::AutoScalingGroup":               true,
	"AWS::Backup::BackupSelection":                     true,
	"AWS::Backup::BackupVault":                         true,
	"AWS::Backup::LogicallyAirGappedBackupVault":       true,
	"AWS::CodeArtifact::Domain":                        true,
	"AWS::CodeArtifact::Repository":                    true,
	"AWS::EC2::CapacityReservation":                    true,
	"AWS::EC2::Instance":                               true,
	"AWS::EC2::SecurityGroup":                          true,
	"AWS::EC2::Subnet":                                 true,
	"AWS::EC2::VPC":                                    true,
	"AWS::EFS::MountTarget":                            true,
	"AWS::EKS::Nodegroup":                              true,
	"AWS::ElastiCache::ReplicationGroup":               true,
	"AWS::ElasticLoadBalancingV2::LoadBalancer":        true,
	"AWS::Events::EventBus":                            true,
	"AWS::EventSchemas::Discoverer":                    true,
	"AWS::EventSchemas::Registry":                      true,
	"AWS::EventSchemas::Schema":                        true,
	"AWS::GameLift::GameSessionQueue":                  true,
	"AWS::GameLift::MatchmakingConfiguration":          true,
	"AWS::GameLift::MatchmakingRuleSet":                true,
	"AWS::Grafana::Workspace":                          true,
	"AWS::Greengrass::ConnectorDefinition":             true,
	"AWS::Greengrass::CoreDefinition":                  true,
	"AWS::Greengrass::DeviceDefinition":                true,
	"AWS::Greengrass::FunctionDefinition":              true,
	"AWS::Greengrass::Group":                           true,
	"AWS::Greengrass::LoggerDefinition":                true,
	"AWS::Greengrass::ResourceDefinition":              true,
	"AWS::Greengrass::SubscriptionDefinition":          true,
	"AWS::ImageBuilder::Component":                     true,
	"AWS::ImageBuilder::ContainerRecipe":               true,
	"AWS::ImageBuilder::DistributionConfiguration":     true,
	"AWS::ImageBuilder::ImagePipeline":                 true,
	"AWS::ImageBuilder::ImageRecipe":                   true,
	"AWS::ImageBuilder::InfrastructureConfiguration":   true,
	"AWS::ManagedBlockchain::Node":                     true,
	"AWS::Neptune::DBCluster":                          true,
	"AWS::RDS::DBInstance":                             true,
	"AWS::RDS::DBParameterGroup":                       true,
	"AWS::RedshiftServerless::Workgroup":               true,
	"AWS::RoboMaker::RobotApplication":                 true,
	"AWS::RoboMaker::SimulationApplication":            true,
	"AWS::Route53Resolver::ResolverRule":               true,
	"AWS::Route53Resolver::ResolverRuleAssociation":    true,
	"AWS::S3::AccessPoint":                             true,
	"AWS::SageMaker::DataQualityJobDefinition":         true,
	"AWS::SageMaker::ModelBiasJobDefinition":           true,
	"AWS::SageMaker::ModelExplainabilityJobDefinition": true,
	"AWS::SageMaker::ModelQualityJobDefinition":        true,
	"AWS::SageMaker::MonitoringSchedule":               true,
	"AWS::SNS::Topic":                                  true,
	"AWS::SQS::Queue":                                  true,
	"AWS::SSM::Parameter":                              true,
	"AWS::StepFunctions::Activity":                     true,
}

// Writable properties these types also return through GetAtt.
var attributeExceptions = map[string][]string{
	"AWS::AppMesh::GatewayRoute":                      {"GatewayRouteName", "MeshName", "MeshOwner", "VirtualGatewayName"},
	"AWS::AppMesh::Mesh":                              {"MeshName"},
	"AWS::AppMesh::Route":                             {"MeshName", "MeshOwner", "RouteName", "VirtualRouterName"},
	"AWS::AppMesh::VirtualGateway":                    {"MeshName", "MeshOwner", "VirtualGatewayName"},
	"AWS::AppMesh::VirtualNode":                       {"MeshName", "MeshOwner", "VirtualNodeName"},
	"AWS::AppMesh::VirtualRouter":                     {"MeshName", "MeshOwner", "VirtualRouterName"},
	"AWS::AppMesh::VirtualService":                    {"MeshName", "MeshOwner", "VirtualServiceName"},
	"AWS::AppSync::DataSource":                        {"Name"},
	"AWS::Cloud9::EnvironmentEC2":                     {"Name"},
	"AWS::CloudWatch::InsightRule":                    {"RuleName"},
	"AWS::DocDB::DBCluster":                           {"Port"},
	"AWS::Greengrass::ConnectorDefinition":            {"Name"},
	"AWS::Greengrass::CoreDefinition":                 {"Name"},
	"AWS::Greengrass::DeviceDefinition":               {"Name"},
	"AWS::Greengrass::FunctionDefinition":             {"Name"},
	"AWS::Greengrass::Group":                          {"Name", "RoleArn"},
	"AWS::Greengrass::LoggerDefinition":               {"Name"},
	"AWS::Greengrass::ResourceDefinition":             {"Name"},
	"AWS::Greengrass::SubscriptionDefinition":         {"Name"},
	"AWS::IoT1Click::Device":                          {"Enabled"},
	"AWS::IoT1Click::Placement":                       {"PlacementName", "ProjectName"},
	"AWS::IoT1Click::Project":                         {"ProjectName"},
	"AWS::Kinesis::StreamConsumer":                    {"ConsumerName", "StreamARN"},
	"AWS::ManagedBlockchain::Member":                  {"NetworkId"},
	"AWS::MediaConvert::JobTemplate":                  {"Name"},
	"AWS::MediaConvert::Preset":                       {"Name"},
	"AWS::MediaConvert::Queue":                        {"Name"},
	"AWS::MediaLive::Input":                           {"Destinations", "Sources"},
	"AWS::OpsWorks::Instance":                         {"AvailabilityZone"},
	"AWS::OpsWorks::UserProfile":                      {"SshUsername"},
	"AWS::Route53Resolver::ResolverEndpoint":          {"Direction", "Name"},
	"AWS::SageMaker::CodeRepository":                  {"CodeRepositoryName"},
	"AWS::SageMaker::Endpoint":                        {"EndpointName"},
	"AWS::SageMaker::EndpointConfig":                  {"EndpointConfigName"},
	"AWS::SageMaker::Model":                           {"ModelName"},
	"AWS::SageMaker::NotebookInstance":                {"NotebookInstanceName"},
	"AWS::SageMaker::NotebookInstanceLifecycleConfig": {"NotebookInstanceLifecycleConfigName"},
	"AWS::SageMaker::Workteam":                        {"WorkteamName"},
	"AWS::ServiceDiscovery::Service":                  {"Name"},
	"AWS::Transfer::User":                             {"ServerId", "UserName"},
}

// Attributes lists the Fn::GetAtt names of a provider schema, sorted.
func Attributes(root map[string]any) []string {
	typ, _ := root["typeName"].(string)
	names := map[string]bool{}
	pointer := func(p string) {
		parts := strings.Split(p, "/")
		if len(parts) > 2 && parts[1] == "properties" {
			names[strings.Join(parts[2:], ".")] = true
		}
	}
	switch {
	case attributeAllPropertyTypes[typ]:
		properties, _ := root["properties"].(map[string]any)
		for _, name := range keys(properties) {
			attributePaths(root, properties[name], name, names, 0)
		}
	case typ == "AWS::CloudFormation::Stack" || typ == "AWS::Serverless::Application":
		names["Outputs.*"] = true
	case strings.HasPrefix(typ, "Custom::") || typ == "AWS::CloudFormation::CustomResource":
		names["*"] = true
	default:
		for _, name := range attributeExceptions[typ] {
			names[name] = true
		}
		for _, v := range asArray(root["readOnlyProperties"]) {
			p, _ := v.(string)
			if typ == "AWS::ServiceCatalog::CloudFormationProvisionedProduct" && p == "/properties/Outputs" {
				names["Outputs.*"] = true
			} else {
				pointer(p)
			}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// attributePaths follows cfn-lint's walk of every property: objects recurse,
// arrays of scalars are leaves, and arrays of objects or arrays stop.
func attributePaths(root map[string]any, v any, path string, names map[string]bool, depth int) {
	s, _ := v.(map[string]any)
	if s == nil || depth > 32 {
		return
	}
	types := map[string]bool{}
	switch t := s["type"].(type) {
	case string:
		types[t] = true
	case []any:
		for _, x := range t {
			if name, ok := x.(string); ok {
				types[name] = true
			}
		}
	}
	switch {
	case types["object"]:
		properties, _ := s["properties"].(map[string]any)
		for _, name := range keys(properties) {
			attributePaths(root, properties[name], path+"."+name, names, depth+1)
		}
	case types["array"]:
		items, _ := s["items"].(map[string]any)
		if t, _ := items["type"].(string); t == "object" || t == "array" {
			return
		}
		names[path] = true
	case s["$ref"] != nil:
		ref, _ := s["$ref"].(string)
		if target, ok := resolvePointer(root, ref); ok {
			attributePaths(root, target, path, names, depth+1)
		}
	default:
		names[path] = true
	}
}
