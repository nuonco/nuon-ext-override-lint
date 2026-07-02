# nuon-ext-override-lint

A Nuon CLI extension that catches the "silent no-op override" footgun: an
install-level Helm `helm_values` override that writes to a key path the chart
never reads, so the deploy succeeds but nothing changes.

It renders the component's chart twice — once with the app-config values alone,
once with the override deep-merged on top — by calling the runner's own
`github.com/nuonco/nuon/pkg/helm` `ChartValues` (merge) and `TemplateChart`
(`ClientOnly` install dry-run) **directly**, so the linter is guaranteed to
render exactly what the runner deploys. If the two renders are identical, the
override had no effect.

It is **strictly read-only**: it fetches install inputs and component config
from the Nuon public API and renders locally. It never updates an install or
triggers a deploy.

## Usage

```sh
# Lint the overrides currently live on the selected install (server-side inputs)
nuon override-lint --chart ./charts/ctl-api

# Author-time (offline): lint the [components.<name>] helm_values in a local
# install config (nuon.toml) before you `nuon installs sync` it. --component is
# optional when the config has exactly one helm_values override.
nuon override-lint --chart ./charts/ctl-api --install-config ./nuon.toml

# Add --diff to any invocation to see the unified diff of the rendered manifest
# (base vs base+override). A no-op override produces an empty diff.
nuon override-lint --chart ./charts/ctl-api --install-config ./nuon.toml --diff
```

Exit code `2` means at least one override is a no-op.

### Example

Given an install config with this `[components.ctl_api]` `helm_values`:

```yaml
worker:
  workers:            # wrong key — the chart reads worker.instances
    - namespace: runners
      minReplicas: 6
```

```
[WARN] ctl_api
       override keys: worker
       override changed values but produced NO change in the rendered manifest — it has no effect (unrecognized key path(s): worker.workers)
       unrecognized key path(s): worker.workers
```

## How it decides

1. **Render diff (authority):** `render(base)` vs `render(base + override)`.
   Identical ⇒ no-op. This also surfaces the Helm array-replacement trap
   (a correct-key list override that wipes sibling entries shows up as a diff
   that removes objects).
2. **Key-path hint:** override key paths whose parent exists in the merged
   values/chart schema but whose leaf key does not (e.g. `worker.workers` when
   the chart declares `worker.instances`).

## Limitations

- `--chart` must point at a local chart directory. Pulling the built OCI chart
  from the management registry is not yet implemented (needs registry creds).
- App-config values files are Go templates resolved server-side; when they are
  not standalone-valid YAML the linter falls back to the chart's own
  `values.yaml` schema (Helm merges chart defaults at render time regardless).
- Secret-derived values are always redacted by the API; a structural render
  uses placeholders. This does not affect no-op / wrong-key detection.
- The extension imports `github.com/nuonco/nuon/pkg/helm` (render/merge) and
  `pkg/config` (install-config parsing) directly from the public `nuonco/nuon`
  module, pinned in `go.mod`. Because `pkg/config` transitively needs
  `nuonco/nuon/sdks/nuon-go`, both are pinned to matching versions; bump them
  together (`go get github.com/nuonco/nuon@<ref> github.com/nuonco/nuon/sdks/nuon-go@<ref>`)
  when picking up upstream changes.
