// Package bundle describes the generated module layout that generation writes
// and validation reads: the manifest, per-region indexes, and package paths.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Generator marks output written by this generator. Generation only replaces
// directories whose manifest carries it.
const Generator = "cfn-cue/v2"

const ManifestFile = "manifest.json"

// ManifestFormat is the version of the manifest's shape. Format 2 adds the
// provider lifecycle keywords to each schema (see Lifecycle) and replaces
// format 1's per-region type maps with a region list and one Types entry per
// resource type (see TypeInfo).
const ManifestFormat = 2

type SchemaInfo struct {
	Type               string `json:"type"`
	SHA256             string `json:"sha256"`
	PropertiesRequired bool   `json:"propertiesRequired"`
	// Attributes lists the Fn::GetAtt names (see generate.Attributes).
	// Manifests from earlier generators omit it, which a reader must treat as
	// unknown.
	Attributes []string `json:"attributes"`
	// IdentifierProperties lists the writable top-level properties that make
	// up the resource's primary identifier (see generate.PrimaryIdentifier).
	// It is omitted when the template cannot set the whole identifier.
	IdentifierProperties []string `json:"identifierProperties,omitempty"`
	Lifecycle
}

// Lifecycle holds provider schema keywords copied verbatim, in provider
// order. Property paths stay JSON pointers. A keyword the schema omits is
// omitted here too.
type Lifecycle struct {
	PrimaryIdentifier               []string           `json:"primaryIdentifier,omitempty"`
	AdditionalIdentifiers           [][]string         `json:"additionalIdentifiers,omitempty"`
	CreateOnlyProperties            []string           `json:"createOnlyProperties,omitempty"`
	ConditionalCreateOnlyProperties []string           `json:"conditionalCreateOnlyProperties,omitempty"`
	WriteOnlyProperties             []string           `json:"writeOnlyProperties,omitempty"`
	ReadOnlyProperties              []string           `json:"readOnlyProperties,omitempty"`
	DeprecatedProperties            []string           `json:"deprecatedProperties,omitempty"`
	ReplacementStrategy             string             `json:"replacementStrategy,omitempty"`
	PropertyTransform               map[string]string  `json:"propertyTransform,omitempty"`
	Tagging                         *Tagging           `json:"tagging,omitempty"`
	Handlers                        map[string]Handler `json:"handlers,omitempty"`
}

// Tagging flags are pointers because the provider defaults an absent flag to
// true, so false must survive and absent must stay absent.
type Tagging struct {
	Taggable                 *bool    `json:"taggable,omitempty"`
	TagOnCreate              *bool    `json:"tagOnCreate,omitempty"`
	TagUpdatable             *bool    `json:"tagUpdatable,omitempty"`
	CloudFormationSystemTags *bool    `json:"cloudFormationSystemTags,omitempty"`
	TagProperty              string   `json:"tagProperty,omitempty"`
	Permissions              []string `json:"permissions,omitempty"`
}

// Handler omits the provider's handlerSchema.
type Handler struct {
	Permissions      []string `json:"permissions,omitempty"`
	TimeoutInMinutes int      `json:"timeoutInMinutes,omitempty"`
}

type Diagnostic struct {
	Resource string `json:"resource"`
	Path     string `json:"path"`
	Keyword  string `json:"keyword"`
	Detail   string `json:"detail"`
}

// Manifest describes a generated module. Format is ManifestFormat; a manifest
// without it is format 1, where the Lifecycle fields are unknown and regions
// maps each region to its type-to-schema map. Consumers should refuse a newer
// format.
type Manifest struct {
	Generator          string `json:"generator"`
	Format             int    `json:"format"`
	Source             string `json:"source"`
	SourceSHA256       string `json:"sourceSHA256"`
	Module             string `json:"module"`
	LanguageExtensions bool   `json:"languageExtensions"`
	IncludeSAM         bool   `json:"includeSAM"`
	IAMPolicies        bool   `json:"iamPolicies"`
	CfnLintRules       bool   `json:"cfnLintRules"`
	// Regions lists every generated region, sorted.
	Regions []string `json:"regions"`
	// Types maps each resource type to its schema and regional availability.
	// RegionTypes and SchemaHash read it.
	Types           map[string]TypeInfo   `json:"types"`
	Schemas         map[string]SchemaInfo `json:"schemas"`
	Diagnostics     []Diagnostic          `json:"diagnostics"`
	ValidationNotes []string              `json:"validationNotes"`
}

// TypeInfo says where a resource type is available and which schema it uses.
// Regions is "*" when the type is in every region of Manifest.Regions.
// Otherwise exactly one of Regions (the sorted regions that have the type) and
// ExceptRegions (the sorted regions that lack it) is set, whichever is
// shorter, preferring Regions on a tie. Hash is the schema in every one of
// those regions: generation requires one schema per type because the
// per-type packages are shared by every region.
type TypeInfo struct {
	Hash          string        `json:"hash"`
	Regions       *Availability `json:"regions,omitempty"`
	ExceptRegions []string      `json:"exceptRegions,omitempty"`
}

// Availability is "*" in JSON when All is set and a sorted array of region
// names otherwise. TypeInfo holds a pointer to it so that an unset Regions is
// omitted.
type Availability struct {
	All  bool
	List []string
}

func (a Availability) MarshalJSON() ([]byte, error) {
	if a.All {
		return []byte(`"*"`), nil
	}
	if a.List == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(a.List)
}

func (a *Availability) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s != "*" {
			return fmt.Errorf("regions: want \"*\" or a list, got %q", s)
		}
		*a = Availability{All: true}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("regions: want \"*\" or a list: %w", err)
	}
	*a = Availability{List: list}
	return nil
}

// RegionTypes returns the type-to-schema map for region, the same map its
// region index holds. It is empty for a region the manifest lacks.
func (m *Manifest) RegionTypes(region string) map[string]string {
	types := map[string]string{}
	for typ := range m.Types {
		if hash, ok := m.SchemaHash(typ, region); ok {
			types[typ] = hash
		}
	}
	return types
}

// SchemaHash returns the schema typ uses in region, if typ is available there.
func (m *Manifest) SchemaHash(typ, region string) (string, bool) {
	info, ok := m.Types[typ]
	if !ok || !slices.Contains(m.Regions, region) {
		return "", false
	}
	switch {
	case info.Regions != nil && info.Regions.All:
	case info.Regions != nil:
		if !slices.Contains(info.Regions.List, region) {
			return "", false
		}
	default:
		if slices.Contains(info.ExceptRegions, region) {
			return "", false
		}
	}
	return info.Hash, true
}

// RegionIndex is the part of the manifest needed to check templates for one
// region. It is small enough to read on every validation.
type RegionIndex struct {
	Generator          string            `json:"generator"`
	Module             string            `json:"module"`
	Region             string            `json:"region"`
	LanguageExtensions bool              `json:"languageExtensions"`
	Resources          map[string]string `json:"resources"`
}

var regionName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)+-[0-9]+$`)
var schemaID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidRegion reports whether name has the shape of an AWS region, which also
// makes it safe to use as a path element.
func ValidRegion(name string) bool { return regionName.MatchString(name) }

// ValidSchemaID reports whether a schema hash is safe to use in a path.
func ValidSchemaID(hash string) bool { return schemaID.MatchString(hash) }

// Paths are slash-separated and relative to the module root.
func RegionDir(region string) string       { return "regions/" + region }
func RegionIndexFile(region string) string { return RegionDir(region) + "/index.json" }
func ResourceDir(hash string) string       { return "resources/h" + hash }
func LiteralDir(hash string) string        { return "literal/h" + hash }

// TypeDir holds the stable per-type package for typ: AWS::S3::Bucket is
// AWS/S3/Bucket, package Bucket. Custom:: names have no package of their
// own; they use AWS::CloudFormation::CustomResource.
func TypeDir(typ string) string { return strings.ReplaceAll(typ, "::", "/") }

// TopLevelDirs are the generated module's lowercase root directories. A
// per-type package's first segment must differ from each, ignoring case.
var TopLevelDirs = []string{"cue.mod", "fn", "iam", "literal", "regions", "resources", "template"}

// TypePackage is the package name of TypeDir: the type's last segment, so an
// import path without a qualifier selects it.
func TypePackage(typ string) string { return typ[strings.LastIndex(typ, "::")+2:] }

// ResourceImport is the import path of a shared template-input schema package.
func ResourceImport(module, hash string) string { return module + "/" + ResourceDir(hash) + ":schema" }

// ReadRegionIndex reads and checks the index for region in the module at root.
// The error wraps os.ErrNotExist when the bundle has no such index.
func ReadRegionIndex(root, region string) (*RegionIndex, error) {
	if !ValidRegion(region) {
		return nil, fmt.Errorf("invalid region name %q: %w", region, os.ErrNotExist)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(RegionIndexFile(region))))
	if err != nil {
		return nil, err
	}
	var index RegionIndex
	if err := json.Unmarshal(b, &index); err != nil {
		return nil, fmt.Errorf("%s: %w", RegionIndexFile(region), err)
	}
	if index.Generator != Generator || index.Region != region || index.Module == "" || len(index.Resources) == 0 {
		return nil, fmt.Errorf("%s: not a %s region index", RegionIndexFile(region), Generator)
	}
	for _, hash := range index.Resources {
		if !ValidSchemaID(hash) {
			return nil, fmt.Errorf("%s: unsafe schema identifier %q", RegionIndexFile(region), hash)
		}
	}
	return &index, nil
}
