package lint

// This file no longer reimplements the runner's merge/render semantics — it
// imports them directly from github.com/nuonco/nuon/pkg/helm (via a local
// replace in go.mod) so the linter renders exactly what the runner deploys.
// The only things left here are (a) a thin stdout-silencing wrapper around
// helm.TemplateChart and (b) a generic CoalesceTables helper used purely by the
// linter's own unknown-key analysis (not a copy of pkg/helm logic).

import (
	"encoding/json"
	"os"

	nuonhelm "github.com/nuonco/nuon/pkg/helm"
	chartutil "helm.sh/helm/v4/pkg/chart/common/util"
	chart "helm.sh/helm/v4/pkg/chart/v2"
)

// chartT aliases the Helm chart type so the rest of the package can refer to it
// without importing the versioned chart path in every file.
type chartT = chart.Chart

// templateChart renders a chart via the runner's pkg/helm.TemplateChart, with
// stdout silenced across the call so our own output (notably --json) stays
// clean — some chart/engine paths write diagnostics to stdout during render.
func templateChart(chrt *chart.Chart, values map[string]interface{}) (string, error) {
	restore := silenceStdout()
	defer restore()
	return nuonhelm.TemplateChart(chrt, values)
}

// coalesce deep-merges over onto base (over authoritative) without mutating
// either input. Used only to build the merged base+chart-defaults map that the
// unknown-key check compares override paths against.
func coalesce(over, base map[string]interface{}) map[string]interface{} {
	return chartutil.CoalesceTables(deepCopyMap(over), deepCopyMap(base))
}

// deepCopyMap returns a JSON round-trip deep copy of a values map. Values come
// from YAML/helm and are JSON-serialisable.
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

// silenceStdout redirects os.Stdout to /dev/null and returns a function that
// restores it.
func silenceStdout() func() {
	saved := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return func() {}
	}
	os.Stdout = devnull
	return func() {
		os.Stdout = saved
		_ = devnull.Close()
	}
}
