package generate

import (
	"sort"

	"github.com/cueformation/cfn-cue/internal/bundle"
)

// buildTypes folds per-region type-to-schema maps into the manifest's sorted
// region list and per-type entries (see bundle.TypeInfo). The caller has
// already checked that each type uses one schema in every region.
func buildTypes(regions map[string]map[string]string) ([]string, map[string]bundle.TypeInfo) {
	names := sortedRegionKeys(regions)
	present := map[string][]string{}
	for _, region := range names {
		for typ := range regions[region] {
			present[typ] = append(present[typ], region)
		}
	}
	types := map[string]bundle.TypeInfo{}
	for typ, in := range present {
		info := bundle.TypeInfo{Hash: regions[in[0]][typ]}
		switch except := absent(names, in); {
		case len(except) == 0:
			info.Regions = &bundle.Availability{All: true}
		case len(in) <= len(except):
			info.Regions = &bundle.Availability{List: in}
		default:
			info.ExceptRegions = except
		}
		types[typ] = info
	}
	return names, types
}

// absent returns the names not in in. Both are sorted.
func absent(names, in []string) []string {
	out := []string{}
	for _, name := range names {
		if i := sort.SearchStrings(in, name); i == len(in) || in[i] != name {
			out = append(out, name)
		}
	}
	return out
}
