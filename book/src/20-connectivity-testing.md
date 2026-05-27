# Connectivity testing

`roksbnkctl test connectivity` answers one question: *can my workspace reach the HTTP/HTTPS endpoints I care about right now?*

It's the simplest of the three test suites — no cluster fixtures, no remote vantage, no JSON parsing harness. Each configured URL gets one HTTP `GET`, the suite reports pass/fail, and the runner exits `0` if every probe passed.

Use it as the first sanity check after `roksbnkctl up`, as a CI smoke step against a known-good fixture set, or as the "is it me or is it the network" baseline before reaching for `curl -v` or `openssl s_client`.

## What the connectivity suite does

For each configured URL the runner:

1. Adds an `https://` scheme if you didn't write one.
2. Issues a single `GET` with a 10-second timeout and the user-agent `roksbnkctl/test`.
3. Records the HTTP status code, the wall-clock duration, and (for HTTPS) the negotiated TLS version.
4. Marks the probe **pass** if the status code is in `[200, 400)` (any 2xx or 3xx); **fail** for anything else, any TLS error, any DNS error, any timeout.
5. Aggregates the per-URL results into a suite result; the suite passes only when every URL passed.

That's it. No retries, no expected-body matching, no configurable status assertions, no L4 reachability — those are deliberate non-goals (see [§ When connectivity is the wrong tool](#when-connectivity-is-the-wrong-tool) below).

## Configuring `extra_hosts`

The list of URLs to probe lives in your workspace config under `test.connectivity.extra_hosts`:

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
test:
  connectivity:
    extra_hosts:
      - https://my-bnk-cis-controller.example.com
      - https://bigip-next-admin.example.com:8443
      - https://gslb.example.com
      - my-bare-host.example.com    # scheme defaults to https://
```

The schema is intentionally minimal — `extra_hosts` is a `[]string` of URLs (or bare hostnames; `https://` is added when no scheme is present). One entry per line. The order in the file is the order the runner probes.

You don't have to hand-edit the YAML to populate this list — `roksbnkctl test hosts {list,add,remove,clear}` reads and writes the same slice. See [§ Managing test hosts via the CLI](#managing-test-hosts-via-the-cli) below.

There's no per-host method, no per-host expected-status, and no per-host TLS-trust override today. If you need to assert something more specific than "does HTTP work" — a particular status code, a custom header, a body match — `curl` is the right tool, not `roksbnkctl test connectivity`. A richer per-host schema is queued for v1.x; the v1.0 surface holds the YAML simple on purpose.

[Chapter 12 — Workspace config](./12-workspace-config.md#test) covers the full `test:` block; this chapter expands the `connectivity` slice.

### What `extra_hosts` typically holds

Three classes of URL show up most often in a real workspace:

- **The BNK CIS controller** — confirms the data-plane front-end is reachable and is returning a sane status code.
- **The F5 BIG-IP Next admin endpoint** — confirms the management plane is reachable from your seat (often `:8443` rather than `:443`).
- **The GSLB VIP that fronts the application** — confirms the routed name actually serves a 2xx; pair with [`roksbnkctl test dns`](./21-dns-testing-gslb.md) for the GSLB-aware DNS-side validation.

What doesn't belong in `extra_hosts`: anything you only care about on a specific TLS error, anything that needs a request body, anything where pass/fail is more nuanced than "got a 2xx or 3xx". Those are `curl` jobs, not connectivity-suite jobs.

## Managing test hosts via the CLI

The YAML in [§ Configuring `extra_hosts`](#configuring-extra_hosts) is the persistent backing store, but you don't have to hand-edit `config.yaml` to populate it. `roksbnkctl test hosts` mirrors the `roksbnkctl targets` ergonomic (see [Chapter 15 — SSH targets](./15-ssh-targets.md#roksbnkctl-targets--full-reference)) and gives you four subcommands that read and write the `test.connectivity.extra_hosts` slice directly:

```
roksbnkctl test hosts list                       # print current extra_hosts, one per line
roksbnkctl test hosts add <url> [<url>...]       # append; idempotent (no-op if already present)
roksbnkctl test hosts remove <url> [<url>...]    # remove; tolerant of absent (no error)
roksbnkctl test hosts clear                      # remove ALL (confirmation prompt, --auto skip)
```

All four route through the same workspace marshaller `targets add` uses, so a `test hosts add` write lands in `~/.roksbnkctl/<workspace>/config.yaml` under `test.connectivity.extra_hosts` — operators who already know the YAML field see the connection. Run `cat ~/.roksbnkctl/<workspace>/config.yaml` after any mutation to confirm.

Argv discipline matches the rest of the v1.7.x surface: `list` and `clear` take `cobra.NoArgs` (a stray positional is rejected at parse time, not silently consumed); `add` and `remove` take `cobra.MinimumNArgs(1)`. A typo like `roksbnkctl test hosts list extra` fails before any RunE runs.

### Worked example

Start from an empty workspace, add a URL, list, then run the connectivity probe — the exact sequence an operator hits the first time the `no hosts configured to probe; add via roksbnkctl test hosts add <url>` error fires from `roksbnkctl test connectivity` or no-flag `roksbnkctl test dns`:

```bash
$ roksbnkctl test hosts list
$ echo $?
0

$ roksbnkctl test hosts add https://docs.f5.com
added https://docs.f5.com

$ roksbnkctl test hosts add https://docs.f5.com
already present: https://docs.f5.com

$ roksbnkctl test hosts list
https://docs.f5.com

$ roksbnkctl test connectivity
running connectivity ...
  PASS  https://docs.f5.com  200 OK in 187ms
connectivity PASS (1/1 passed)
```

> **Note:** the byte-for-byte output above is illustrative — it reflects the surface the Sprint 24 CLI ships and is what tech-writer's drift sweep will re-capture against the built binary before the `v1.7.1` cut. The shape is stable (`list` empty → zero bytes + exit 0; `add` against already-present → "already present:" log + exit 0); minor wording deltas in the log lines may surface in the GREEN-verdict re-capture.

`--output json` is supported on `list` for scripted callers — empty list emits `[]`, populated list emits a JSON array preserving insertion order:

```bash
$ roksbnkctl test hosts list -o json
["https://docs.f5.com","https://bigip-next-admin.example.com:8443"]
```

### `clear`

`clear` requires confirmation (defaults to No) because it wipes the entire slice — the same pattern `roksbnkctl down` and `roksbnkctl cluster down` use, not the per-target `targets remove` pattern:

```bash
$ roksbnkctl test hosts clear
This will remove ALL configured test hosts (2 entries). Continue? [y/N]: y
cleared 2 entries

$ roksbnkctl test hosts clear --auto
cleared 0 entries
```

`--auto` skips the prompt for automation contexts. Mirrors the `--auto` semantics elsewhere in the binary.

### Scope of the CLI

The CLI manages **only** `test.connectivity.extra_hosts` — the slice shared by `roksbnkctl test connectivity` and no-flag `roksbnkctl test dns`. The `test.dns.{default_target,resolvers}` and `test.throughput.*` fields have working flag-driven equivalents (`--target`, `--server`, `--duration`, …) and aren't covered by `test hosts`; populate those by hand-editing `config.yaml` for now. A broader `test config` surface is a candidate for a future sprint.

YAML-comment preservation through the marshaller is best-effort and depends on the workspace round-trip — if you keep hand-written comments in `config.yaml`, prefer the YAML path for additions that need to land alongside specific comments; use the CLI for the routine add/remove/list/clear flow.

## The `--insecure` flag

Self-signed certs are common in pre-production BNK deployments — the F5 BIG-IP Next admin endpoint, the CIS controller, an internal GSLB VIP that hasn't yet been re-fronted with a public CA cert. By default Go's TLS stack rejects them and the probe fails with `x509: certificate signed by unknown authority`.

Pass `--insecure` to skip certificate verification for the run:

```bash
roksbnkctl test connectivity --insecure
```

What `--insecure` does:

- Sets `tls.Config.InsecureSkipVerify = true` on the HTTP client used by the connectivity suite.
- Applies for the duration of one invocation only.
- Affects every URL probed in that run.

What `--insecure` does **not** do:

- It does not change L4 / DNS behaviour. A name that won't resolve still fails; a host that drops TCP still fails.
- It is not per-host — there's no `--insecure-only=foo.example.com`. Once set, the run skips verification for everything in `extra_hosts`.
- It is not persisted. Setting it in one invocation does not affect the next.
- It is not the same as a config-level `insecure_tls: true` per host. The v1.0 schema doesn't have that knob; the only way to skip cert verification today is the session-wide flag.

If you need different TLS-trust posture per endpoint (one URL strict, another lenient), run two invocations with two different `extra_hosts` lists in two workspaces — that's the workaround until per-host trust lands.

## Reading the output

Default output is human-readable on stderr; pass `-o json` for machine-readable on stdout.

### Human-readable

```bash
$ roksbnkctl test connectivity
running connectivity ...
  PASS  https://my-bnk-cis-controller.example.com  200 OK in 142ms
  PASS  https://bigip-next-admin.example.com:8443  302 Found in 88ms
  FAIL  https://gslb.example.com                   Get "...": dial tcp: i/o timeout
connectivity FAIL (2/3 passed)
$ echo $?
1
```

A 3xx redirect counts as pass — the runner doesn't follow redirects, but the redirect itself is a successful HTTP response, which is what the suite measures. If you specifically need the final 200 after a redirect chain, `curl -L` is the tool.

### JSON

```bash
$ roksbnkctl test connectivity -o json
```

```json
{
  "schema": "roksbnkctl.v1",
  "command": "test",
  "suite": "connectivity",
  "timestamp": "2026-05-10T14:32:01.123Z",
  "duration_ms": 235,
  "overall": "fail",
  "results": [
    {
      "suite": "connectivity",
      "name": "https://my-bnk-cis-controller.example.com",
      "status": "pass",
      "detail": "200 OK in 142ms",
      "duration_ms": 142,
      "extra": { "status_code": 200, "tls_version": "TLS 1.3" }
    },
    {
      "suite": "connectivity",
      "name": "https://gslb.example.com",
      "status": "fail",
      "detail": "Get \"https://gslb.example.com\": dial tcp: i/o timeout",
      "duration_ms": 10003
    }
  ]
}
```

Exit code follows the same rules as the human-readable form: `0` on `overall: pass`, `1` on `overall: fail`. CI runners can branch on the exit code; richer assertions (e.g., "I tolerate one fail out of five") need to consume the JSON.

## Running connectivity inside `roksbnkctl test all`

Connectivity is one of the suites the bare `roksbnkctl test` (or `roksbnkctl test all`) command dispatches. The runner walks every configured suite, prints per-suite summaries on stderr, and exits non-zero if any suite failed:

```bash
$ roksbnkctl test
running connectivity ...
  PASS  https://bnk-cis.dev-tor.example.com  200 OK in 174ms
running dns ...
  PASS  bnk-cis.dev-tor.example.com  resolved 1 address(es)
connectivity PASS (1/1 passed)
dns          PASS (1/1 passed)

PASS overall (2/2 suites passed)
```

In `-o json` mode, `roksbnkctl test all` emits an `all`-shape envelope with one `suites[]` entry per suite. CI assertions can pin to either the suite-level overall or to a specific probe's status:

```bash
roksbnkctl test all -o json | jq -e '.suites[] | select(.suite=="connectivity") | .overall == "pass"'
```

The bare `roksbnkctl test` defaults to the `all` suite. To run connectivity in isolation:

```bash
roksbnkctl test connectivity            # explicit suite
roksbnkctl test connectivity --insecure # session-wide TLS skip
```

## Exit codes and CI integration

```
exit 0  →  every probe passed (every URL returned 2xx or 3xx)
exit 1  →  any probe failed (non-2xx/3xx, TLS error, DNS error, timeout)
```

There's no third "infra error" exit code from the connectivity suite specifically — the suite is straight Go HTTP, no external tooling, no backend dispatch. If `roksbnkctl test connectivity` exits non-zero, the cause is in the response from one of your configured URLs.

For a CI step that tolerates a known-flaky endpoint while still failing on the others, consume the JSON instead of relying on the exit code:

```bash
roksbnkctl test connectivity -o json \
  | jq -e '[.results[] | select(.name | test("flaky-staging.example") | not) | .status] | all(. == "pass")'
```

## Worked example: probing a BNK deployment

A typical post-`up` config for a BNK trial — cover the data-plane VIP, the admin endpoint, and the GSLB front:

```yaml
# ~/.roksbnkctl/dev-tor/config.yaml
test:
  connectivity:
    extra_hosts:
      - https://bnk-cis.dev-tor.bnkfun.example.com         # BNK CIS controller (data plane)
      - https://bigip-next-admin.dev-tor.bnkfun.example.com:8443  # F5 BIG-IP Next admin
      - https://gslb-vip.dev-tor.bnkfun.example.com        # the GSLB front
```

Then:

```bash
$ roksbnkctl test connectivity --insecure
running connectivity ...
  PASS  https://bnk-cis.dev-tor.bnkfun.example.com               200 OK in 174ms
  PASS  https://bigip-next-admin.dev-tor.bnkfun.example.com:8443 302 Found in 91ms
  PASS  https://gslb-vip.dev-tor.bnkfun.example.com              200 OK in 211ms
connectivity PASS (3/3 passed)
```

`--insecure` is needed here because BNK's admin endpoint and the GSLB VIP are fronted by a self-signed cert in dev. Once the trial moves to a production cert chain, drop the flag — the strict path is what you want for staging and prod.

## When connectivity is the wrong tool

`roksbnkctl test connectivity` is "does HTTP work". For anything finer-grained, reach for the right tool:

| Scenario | Use this instead |
|---|---|
| You want to see the full TLS handshake, the cert chain, the SNI resolution, the negotiated cipher | `openssl s_client -connect host:port -servername host` |
| You want headers, redirect-following, body matching, a specific status assertion | `curl -v -L --fail-with-body <url>` |
| You want to confirm L4 reachability on a specific port, no HTTP layer | `nc -vz host port` (or `bash -c 'echo > /dev/tcp/host/port'`) |
| You want to confirm DNS resolution from a specific resolver, especially across vantages for GSLB | [`roksbnkctl test dns`](./21-dns-testing-gslb.md) |
| You want to see what answer a name returns from inside the cluster vs from your laptop | [`roksbnkctl test dns --gslb-compare`](./21-dns-testing-gslb.md#the---gslb-compare-workflow) |
| You want to measure bandwidth between two endpoints | [`roksbnkctl test throughput`](./22-throughput-testing.md) |

The connectivity suite is intentionally a thin probe. When the answer to "is it broken" is "yes" and you need to know why, the suite has done its job — it's flagged the URL — and the next step is one of the tools above.

## Cross-references

- [Chapter 12 — Workspace config](./12-workspace-config.md#test) — full `test:` block schema, including `connectivity.extra_hosts`.
- [Chapter 15 — SSH targets](./15-ssh-targets.md#roksbnkctl-targets--full-reference) — the `roksbnkctl targets` ergonomic precedent that `roksbnkctl test hosts` mirrors.
- [Chapter 21 — DNS testing for GSLB](./21-dns-testing-gslb.md) — when "the URL fails" actually means "the name doesn't resolve from this vantage".
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — the bandwidth-measurement companion suite.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — common patterns for diagnosing connectivity failures across BNK / ROKS deployments.
