# Unattended setup: seeding a workspace from a file, URL, or environment

The [quick start](./07-quick-start.md) drives `init` interactively. A CI pipeline
or a fleet operator wants the opposite: hand `init` a finished config, fetch it
from a URL, and inject secrets from the environment so a single committed
template stands up many workspaces without ever baking an API key into version
control.

Three `init` options make that work:

| Option | What it does |
|---|---|
| `--config-file <path\|url>` | Seed the workspace `config.yaml` directly (no interview) |
| `--var-file <path\|url>` | Seed `terraform.tfvars` (sibling; pre-existing, now URL-aware) |
| `--override-from-env` | Overlay specific `config.yaml` fields from environment variables |

## The pattern

```mermaid
flowchart LR
    T["config.yaml.tmpl<br/>(committed, no secrets)"] -->|--config-file| I["roksbnkctl init"]
    E["$IBMCLOUD_API_KEY<br/>$ROKSBNKCTL_*"] -->|--override-from-env| I
    I --> W["~/.roksbnkctl/&lt;ws&gt;/config.yaml<br/>(complete, secrets filled)"]
```

Commit a template with the secret fields blank, and let the pipeline supply the
real values from its environment:

```yaml
# config.yaml.tmpl — committed to git, no secrets
ibmcloud:
  region: ""              # ← ROKSBNKCTL_REGION
  resource_group: ""      # ← ROKSBNKCTL_RESOURCE_GROUP
  api_key_b64: ""         # ← IBMCLOUD_API_KEY (raw, base64-encoded)
prefix: ""                # ← ROKSBNKCTL_PREFIX
cluster: { create: true, name: "" }
tf_source: { type: embedded }
resources:
  bnk: { create: true }
  tgw_jumphost: { create: true }
  client_vpc: { create: true }
```

```bash
export IBMCLOUD_API_KEY="$(vault read -field=key secret/ibmcloud)"
export ROKSBNKCTL_REGION=eu-de
export ROKSBNKCTL_RESOURCE_GROUP=default
export ROKSBNKCTL_PREFIX=ci-run-42

roksbnkctl init -w ci-run-42 \
  --config-file https://raw.githubusercontent.com/acme/infra/main/config.yaml.tmpl \
  --override-from-env
```

No secret ever lands in git; the same template seeds every account.

## `--config-file`

`--config-file` parses the supplied YAML strictly — **unknown fields are
rejected**, not silently dropped — into the workspace `config.yaml`, and skips
the interview entirely. It is **non-interactive when the config is complete**.
A config is complete when it has all of:

- `ibmcloud.region`
- `ibmcloud.resource_group`
- `prefix`
- `tf_source.type`

If any are missing (after `--override-from-env` runs), `init` exits with a clear
message naming exactly which — supply them in the file, set them via the
environment, or run `init` interactively. The API key is **not** required in the
file; it resolves from the environment, keychain, or config at run time as usual
(see [Credentials](./14-credentials-resolver.md)).

`--config-file` and `--var-file` are independent and may be combined: the former
seeds `config.yaml`, the latter is copied verbatim to `terraform.tfvars.user`.

## URL inputs

Both `--config-file` and `--var-file` accept a local path **or** an `http(s)`
URL. A URL is fetched (30 s timeout, 10 MB cap) and treated identically to a
local file — a raw-git URL, a presigned IBM COS URL, or any reachable endpoint.
There is no fetch authentication in this release; use a presigned or public URL.

## `--override-from-env`

`--override-from-env` overlays a **fixed set** of `config.yaml` fields from
environment variables that are set and non-empty, after the file/interview has
produced the config. **The environment wins** over the seeded value — it is the
explicit late-binding step. This is a fixed field map, not arbitrary templating.

| Environment variable | `config.yaml` field | Encoding |
|---|---|---|
| `IBMCLOUD_API_KEY` | `ibmcloud.api_key_b64` | raw key, base64-encoded |
| `ROKSBNKCTL_API_KEY_B64` | `ibmcloud.api_key_b64` | verbatim (pre-encoded) |
| `ROKSBNKCTL_PREFIX` | `prefix` | verbatim |
| `ROKSBNKCTL_REGION` | `ibmcloud.region` | verbatim |
| `ROKSBNKCTL_RESOURCE_GROUP` | `ibmcloud.resource_group` | verbatim |
| `ROKSBNKCTL_TESTING_SSH_KEY_NAME` | `resources.testing_ssh_key_name` | verbatim |
| `ROKSBNKCTL_GENERIC_PASSWORD` | `registry.generic_password_b64` | raw, base64-encoded |

Notes:

- For the API key, `IBMCLOUD_API_KEY` holds the **raw** key and `init`
  base64-encodes it into `api_key_b64` (matching how the rest of the tool reads
  `IBMCLOUD_API_KEY`). If you already have the base64 form, set
  `ROKSBNKCTL_API_KEY_B64` instead — it takes precedence and is stored verbatim.
- `init` logs how many overrides were applied and which fields — **never the
  values**, so secrets stay out of CI logs.
- `--override-from-env` also applies on the interactive path, so you can answer
  the interview and still pull the API key from the environment.
