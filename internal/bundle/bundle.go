// Package bundle describes the generated module layout that generation writes
// and validation reads: the manifest, per-region indexes, and package paths.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Generator marks output written by this generator. Generation only replaces
// directories whose manifest carries it.
const Generator = "cfn-cue/v2"

const ManifestFile = "manifest.json"

type SchemaInfo struct {
	Type               string `json:"type"`
	SHA256             string `json:"sha256"`
	PropertiesRequired bool   `json:"propertiesRequired"`
}

type Diagnostic struct {
	Resource string `json:"resource"`
	Path     string `json:"path"`
	Keyword  string `json:"keyword"`
	Detail   string `json:"detail"`
}

type Manifest struct {
	Generator          string                       `json:"generator"`
	Source             string                       `json:"source"`
	SourceSHA256       string                       `json:"sourceSHA256"`
	Module             string                       `json:"module"`
	LanguageExtensions bool                         `json:"languageExtensions"`
	IncludeSAM         bool                         `json:"includeSAM"`
	IAMPolicies        bool                         `json:"iamPolicies"`
	Regions            map[string]map[string]string `json:"regions"`
	Schemas            map[string]SchemaInfo        `json:"schemas"`
	Diagnostics        []Diagnostic                 `json:"diagnostics"`
	ValidationNotes    []string                     `json:"validationNotes"`
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
	if index.Generator != Generator || index.Region != region || index.Module == "" {
		return nil, fmt.Errorf("%s: not a %s region index", RegionIndexFile(region), Generator)
	}
	for _, hash := range index.Resources {
		if !ValidSchemaID(hash) {
			return nil, fmt.Errorf("%s: unsafe schema identifier %q", RegionIndexFile(region), hash)
		}
	}
	return &index, nil
}
