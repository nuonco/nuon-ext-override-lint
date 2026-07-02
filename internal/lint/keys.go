package lint

import (
	"sort"

	chartcommon "helm.sh/helm/v4/pkg/chart/common"
)

// topLevelKeys returns the sorted top-level keys of an override YAML document.
func topLevelKeys(override string) []string {
	m, err := parseYAML(override)
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unknownLeafPaths walks the override map and returns dotted key paths whose key
// is absent from the correspondingly-nested base map. This catches the case
// where the override writes to a sibling key the chart never reads — e.g. the
// override sets worker.workers while the chart (and base values) only define
// worker.instances, so "worker.workers" is reported as unknown.
func unknownLeafPaths(override string, base map[string]interface{}) []string {
	m, err := parseYAML(override)
	if err != nil {
		return nil
	}
	var out []string
	walkUnknown(m, base, "", &out)
	sort.Strings(out)
	return out
}

func walkUnknown(over, base map[string]interface{}, prefix string, out *[]string) {
	for k, ov := range over {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		bv, present := base[k]
		if !present {
			*out = append(*out, path)
			continue
		}
		// Both sides are maps: descend to find the divergent key deeper down.
		om, ook := ov.(map[string]interface{})
		bm, bok := bv.(map[string]interface{})
		if ook && bok {
			walkUnknown(om, bm, path, out)
		}
	}
}

func parseYAML(doc string) (map[string]interface{}, error) {
	vals, err := chartcommon.ReadValues([]byte(doc))
	if err != nil {
		return nil, err
	}
	return vals.AsMap(), nil
}
