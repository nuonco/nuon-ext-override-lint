package lint

import (
	"bytes"
	"fmt"
	"os"

	"github.com/mitchellh/mapstructure"
	nuoncfg "github.com/nuonco/nuon/pkg/config"
	"github.com/pelletier/go-toml/v2"
)

// parseInstallConfig decodes a local install config file (nuon.toml) into
// config.Install using the exact same TOML+mapstructure pipeline the CLI's
// `installs sync` uses, so what we lint matches what `nuon installs sync` would
// flatten into synthetic override inputs and apply.
func parseInstallConfig(path string) (*nuoncfg.Install, error) {
	byts, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	obj := map[string]interface{}{}
	if err := toml.NewDecoder(bytes.NewReader(byts)).Decode(&obj); err != nil {
		return nil, fmt.Errorf("decoding TOML: %w", err)
	}

	var cfg nuoncfg.Install
	dc := nuoncfg.DecoderConfig()
	dc.Result = &cfg
	dec, err := mapstructure.NewDecoder(dc)
	if err != nil {
		return nil, err
	}
	if err := dec.Decode(obj); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	if err := cfg.Parse(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// helmOverridesFromConfig returns componentName -> helm_values YAML for every
// component that carries a non-empty helm_values override in the config file.
func helmOverridesFromConfig(cfg *nuoncfg.Install) map[string]string {
	out := map[string]string{}
	for name, o := range cfg.Components {
		if o.HelmValues != "" {
			out[name] = o.HelmValues
		}
	}
	return out
}
