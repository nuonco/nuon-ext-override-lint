// Package cmd wires the override-lint extension's command-line interface.
package cmd

import (
	"os"

	"github.com/nuonco/nuon-ext-override-lint/internal/lint"
	"github.com/spf13/cobra"
)

// BuildVersion is overridden at release time via -ldflags.
var BuildVersion = "dev"

// NewRootCmd builds the root cobra command for the override-lint extension.
func NewRootCmd() *cobra.Command {
	cfg := lint.Config{
		APIURL:    getenv("NUON_API_URL", "https://api.nuon.co"),
		Token:     os.Getenv("NUON_API_TOKEN"),
		OrgID:     os.Getenv("NUON_ORG_ID"),
		InstallID: os.Getenv("NUON_INSTALL_ID"),
	}
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "nuon-ext-override-lint",
		Short: "Lint install-level Helm values overrides",
		Long: `Render a component's Helm chart with and without its install-level
helm_values override and warn when the override changed the values map but
produced no change in the rendered manifest — the classic worker.workers vs
worker.instances footgun where an override writes to a key path the chart never
reads.

Strictly read-only: it fetches install inputs/config from the Nuon public API
(or a local install config) and renders locally. It never updates an install or
triggers a deploy.`,
		Example: `  nuon override-lint --chart ./charts/ctl-api
  nuon override-lint --chart ./charts/ctl-api --install-config ./nuon.toml
  nuon override-lint --chart ./charts/ctl-api --install-config ./nuon.toml --diff`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if showVersion {
				os.Stdout.WriteString(BuildVersion + "\n")
				return nil
			}
			return lint.Run(cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.InstallID, "install-id", cfg.InstallID, "install ID (defaults to $NUON_INSTALL_ID)")
	cmd.Flags().StringVar(&cfg.ChartPath, "chart", "", "path to the component's local Helm chart directory (required)")
	cmd.Flags().StringVar(&cfg.Component, "component", "", "optional: pick/label one component (resolved name), e.g. ctl_api. Only needed to disambiguate when multiple components carry overrides")
	cmd.Flags().StringVar(&cfg.InstallConfig, "install-config", "", "author-time mode: lint the [components.<name>] helm_values in a local install config (nuon.toml) against the chart (offline, no API calls)")
	cmd.Flags().BoolVar(&cfg.ShowDiff, "diff", false, "print a unified diff of the rendered manifest (base vs base+override); empty for a no-op")
	cmd.Flags().BoolVar(&cfg.AsJSON, "json", false, "emit findings as JSON")
	cmd.Flags().BoolVarP(&showVersion, "version", "V", false, "print the extension version")

	return cmd
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
