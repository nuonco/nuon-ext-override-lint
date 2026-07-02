// Package lint renders a component's Helm chart with and without an
// install-level helm_values override and reports when the override changed the
// values map but had no effect on the rendered manifest — the classic
// worker.workers-vs-instances footgun where an override writes to a key path
// the chart never reads.
//
// It is strictly read-only: it fetches install inputs and component config from
// the Nuon public API (or reads a local install config) and renders locally
// with a ClientOnly dry-run. It never updates an install or triggers a deploy.
package lint

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	nuonhelm "github.com/nuonco/nuon/pkg/helm"
	"github.com/pmezard/go-difflib/difflib"
)

// overridePrefix is the reserved synthetic-input prefix for install-level Helm
// values overrides (see pkg/config.ComponentOverrideInputPrefix +
// ComponentOverrideKindHelmValues). The suffix is hex(componentName).
const overridePrefix = "nuon_component_override_v1_helm_values_"

// ExitNoOp is the process exit code used when at least one override is a no-op.
const ExitNoOp = 2

// Config holds the resolved inputs for a single lint run.
type Config struct {
	APIURL        string
	Token         string
	OrgID         string
	InstallID     string
	ChartPath     string
	Component     string
	InstallConfig string
	ShowDiff      bool
	AsJSON        bool
}

type finding struct {
	Component    string   `json:"component"`
	OverrideKeys []string `json:"override_keys"`
	NoOp         bool     `json:"no_op"`
	UnknownPaths []string `json:"unknown_paths,omitempty"`
	RenderError  string   `json:"render_error,omitempty"`
	Note         string   `json:"note,omitempty"`
	Message      string   `json:"message"`
	Diff         string   `json:"diff,omitempty"`
}

// Run lints the install-level helm_values overrides described by cfg. It exits
// the process with ExitNoOp when any override is a no-op.
func Run(cfg Config) error {
	if cfg.ChartPath == "" {
		return fmt.Errorf("--chart is required: point it at the component's chart dir (OCI pull is not yet implemented)")
	}

	// Author-time mode: lint the helm_values overrides declared in a local
	// install config (nuon.toml), no API.
	if cfg.InstallConfig != "" {
		return runInstallConfig(cfg)
	}

	if cfg.Token == "" || cfg.OrgID == "" {
		return fmt.Errorf("missing NUON_API_TOKEN / NUON_ORG_ID (run via `nuon override-lint`, not the bare binary)")
	}
	if cfg.InstallID == "" {
		return fmt.Errorf("no install selected: pass --install-id or select one with `nuon installs select`")
	}

	api := &client{base: strings.TrimRight(cfg.APIURL, "/"), token: cfg.Token, orgID: cfg.OrgID}

	overrides, err := api.helmOverrides(cfg.InstallID)
	if err != nil {
		return fmt.Errorf("fetching install inputs: %w", err)
	}
	if len(overrides) == 0 {
		fmt.Println("no helm_values overrides set on this install")
		return nil
	}

	comps, err := api.installComponents(cfg.InstallID)
	if err != nil {
		return fmt.Errorf("listing install components: %w", err)
	}

	chrt, err := nuonhelm.GetChartByPath(cfg.ChartPath)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	sort.Strings(names)

	var findings []finding
	for _, name := range names {
		if cfg.Component != "" && cfg.Component != name {
			continue
		}
		compID, ok := comps[name]
		if !ok {
			findings = append(findings, finding{Component: name, Message: "override targets a component not found on this install"})
			continue
		}
		valuesFiles, err := api.componentValuesFiles(compID)
		if err != nil {
			return fmt.Errorf("fetching config for %s: %w", name, err)
		}
		findings = append(findings, lintOverride(name, valuesFiles, overrides[name], chrt, cfg.ShowDiff))
	}

	return report(findings, cfg.AsJSON)
}

// runInstallConfig lints the helm_values overrides declared in a local install
// config (nuon.toml) against the chart, fully offline. Instead of resolving the
// server-side install inputs, it reads the same override the CLI would flatten
// and apply.
func runInstallConfig(cfg Config) error {
	inst, err := parseInstallConfig(cfg.InstallConfig)
	if err != nil {
		return fmt.Errorf("parsing install config %s: %w", cfg.InstallConfig, err)
	}

	overrides := helmOverridesFromConfig(inst)
	if len(overrides) == 0 {
		fmt.Println("no helm_values overrides set in install config")
		return nil
	}

	name := cfg.Component
	if name == "" {
		if len(overrides) != 1 {
			names := make([]string, 0, len(overrides))
			for n := range overrides {
				names = append(names, n)
			}
			sort.Strings(names)
			return fmt.Errorf("install config has helm_values overrides for %d components (%s); pass --component to pick one", len(names), strings.Join(names, ", "))
		}
		for n := range overrides {
			name = n
		}
	}

	override, ok := overrides[name]
	if !ok {
		return fmt.Errorf("component %q has no helm_values override in %s", name, cfg.InstallConfig)
	}

	chrt, err := nuonhelm.GetChartByPath(cfg.ChartPath)
	if err != nil {
		return err
	}

	f := lintOverride(name, nil, override, chrt, cfg.ShowDiff)
	return report([]finding{f}, cfg.AsJSON)
}

// lintOverride renders base vs base+override and decides whether the override is
// a no-op. It also flags override key paths whose parent exists in the merged
// values but the leaf key does not (a likely typo, e.g. worker.workers).
func lintOverride(name string, valuesFiles []string, override string, chrt *chartT, withDiff bool) finding {
	f := finding{Component: name, OverrideKeys: topLevelKeys(override)}

	// App-config values files are Go templates resolved server-side, so they are
	// not always standalone-valid YAML. Use them for fidelity when they parse,
	// otherwise fall back to an empty base — Helm still merges the chart's own
	// values.yaml at render time, so worker.instances (the real schema) is present
	// either way. Both base and base+override use the runner's own
	// helm.ChartValues merge so precedence matches deploy time exactly.
	base, err := nuonhelm.ChartValues(valuesFiles, nil, "")
	if err != nil {
		valuesFiles = nil
		base = map[string]interface{}{}
		f.Note = "app-config values unresolved; linting against chart defaults only"
	}
	withOverride, err := nuonhelm.ChartValues(valuesFiles, nil, override)
	if err != nil {
		f.RenderError = err.Error()
		f.Message = "override is not valid YAML"
		return f
	}

	// Check override key paths against the chart's declared schema (defaults
	// merged with any parsed app-config values), so worker.workers is flagged
	// because the chart only declares worker.instances.
	f.UnknownPaths = unknownLeafPaths(override, coalesce(base, chrt.Values))

	renderBase, errB := templateChart(chrt, base)
	renderOver, errO := templateChart(chrt, withOverride)
	if errB != nil || errO != nil {
		msg := errB
		if msg == nil {
			msg = errO
		}
		f.RenderError = msg.Error()
		f.Message = "chart failed to render (values may need server-side resolution)"
		return f
	}

	if withDiff {
		f.Diff = unifiedDiff(renderBase, renderOver)
	}

	if renderBase == renderOver {
		f.NoOp = true
		f.Message = "override changed values but produced NO change in the rendered manifest — it has no effect"
		if len(f.UnknownPaths) > 0 {
			f.Message += fmt.Sprintf(" (unrecognized key path(s): %s)", strings.Join(f.UnknownPaths, ", "))
		}
		return f
	}

	f.Message = "override changes the rendered manifest"
	return f
}

// unifiedDiff returns a unified diff of the rendered manifest before and after
// the override. Empty when the two renders are identical (a no-op override).
func unifiedDiff(base, over string) string {
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(base),
		B:        difflib.SplitLines(over),
		FromFile: "rendered (base)",
		ToFile:   "rendered (base+override)",
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return out
}

func report(findings []finding, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(findings); err != nil {
			return err
		}
	} else {
		for _, f := range findings {
			mark := "ok  "
			if f.NoOp || f.RenderError != "" || strings.HasPrefix(f.Message, "override targets") {
				mark = "WARN"
			}
			fmt.Printf("[%s] %s\n", mark, f.Component)
			fmt.Printf("       override keys: %s\n", strings.Join(f.OverrideKeys, ", "))
			fmt.Printf("       %s\n", f.Message)
			if f.Note != "" {
				fmt.Printf("       note: %s\n", f.Note)
			}
			if len(f.UnknownPaths) > 0 {
				fmt.Printf("       unrecognized key path(s): %s\n", strings.Join(f.UnknownPaths, ", "))
			}
			if f.RenderError != "" {
				fmt.Printf("       detail: %s\n", f.RenderError)
			}
			if f.Diff != "" {
				fmt.Printf("       rendered manifest diff:\n")
				for _, line := range strings.Split(strings.TrimRight(f.Diff, "\n"), "\n") {
					fmt.Printf("       | %s\n", line)
				}
			}
		}
	}
	for _, f := range findings {
		if f.NoOp {
			os.Exit(ExitNoOp)
		}
	}
	return nil
}

// --- Nuon public API client (read-only) ---

type client struct {
	base  string
	token string
	orgID string
}

func (c *client) get(path string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Nuon-Org-ID", c.orgID)
	req.Header.Set("Accept", "application/json")

	httpc := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s -> %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

type installInputs struct {
	RedactedValues map[string]string `json:"redacted_values"`
	Values         map[string]string `json:"values"`
}

// helmOverrides returns componentName -> override YAML for every non-empty
// helm_values override on the install's current inputs revision.
func (c *client) helmOverrides(installID string) (map[string]string, error) {
	var inputs installInputs
	if err := c.get("/v1/installs/"+installID+"/inputs/current", &inputs); err != nil {
		return nil, err
	}

	// Overrides are non-secret, so their full value is present in redacted_values,
	// which is a superset of values (values omits synthetic override inputs).
	vals := inputs.RedactedValues
	if len(vals) == 0 {
		vals = inputs.Values
	}

	out := map[string]string{}
	for k, v := range vals {
		if !strings.HasPrefix(k, overridePrefix) || strings.TrimSpace(v) == "" {
			continue
		}
		nameBytes, err := hex.DecodeString(strings.TrimPrefix(k, overridePrefix))
		if err != nil {
			continue
		}
		out[string(nameBytes)] = v
	}
	return out, nil
}

type installComponentsResp []struct {
	Component struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		ResolvedVarName string `json:"resolved_var_name"`
	} `json:"component"`
}

// installComponents maps a component's resolved name (and raw name) to its ID.
func (c *client) installComponents(installID string) (map[string]string, error) {
	out := map[string]string{}
	for offset := 0; ; offset += 50 {
		var page installComponentsResp
		if err := c.get(fmt.Sprintf("/v1/installs/%s/components?limit=50&offset=%d", installID, offset), &page); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if e.Component.ResolvedVarName != "" {
				out[e.Component.ResolvedVarName] = e.Component.ID
			}
			if e.Component.Name != "" {
				out[e.Component.Name] = e.Component.ID
			}
		}
		if len(page) < 50 {
			break
		}
	}
	return out, nil
}

type componentConfigResp struct {
	Helm struct {
		HelmConfigJSON struct {
			ValuesFiles []string `json:"values_files"`
		} `json:"helm_config_json"`
	} `json:"helm"`
}

func (c *client) componentValuesFiles(componentID string) ([]string, error) {
	var cfg componentConfigResp
	if err := c.get("/v1/components/"+componentID+"/configs/latest", &cfg); err != nil {
		return nil, err
	}
	return cfg.Helm.HelmConfigJSON.ValuesFiles, nil
}
