# Changelog

All notable changes to `roksbnkctl` are documented in this file. Format follows the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) convention; the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html) starting at `v0.9.0`.

Per-sprint design rationale lives in [`docs/PLAN.md`](docs/PLAN.md); per-PRD design specs live under [`docs/prd/`](docs/prd/). This file is the user-facing summary of what changed between releases.

## Unreleased

### Removed

- **Four more settings that configured nothing** (#228). The audit #227 prompted,
  now a repeatable one: `make audit-env` pulls each consumer — the controller
  image, the chart — and reports any environment variable or helm key roksbnkctl
  emits that does not appear in it.

  | Emitted | Consumer | |
  |---|---|---|
  | `VPC_NAME` | f5ingress (both lines) | dead — `CLOUD_VPC` beside it is real |
  | `IBM_TRUSTED_PROFILE_ID` | f5ingress (both lines) | dead — `CLOUD_TRUSTED_PROFILE` is real |
  | `versionValidator.image.repository` ×2 | `f5-spk-crds-common` / `-service-proxy` | dead — those charts declare three keys and this is not one |

  The first two are the same "emit it under two names, one must be right" habit
  that produced the GTM pair. The code even named the pattern —
  *"CLOUD_VPC sits beside VPC_NAME: the real names are F5's contract"* — while
  getting the conclusion backwards.

  **Air-gap note:** the `versionValidator` override means the CRD charts' images
  were never redirected to a mirror *by that setting*. Removing it does not change
  that; it stops the configuration claiming otherwise.

  The audit isolates its FAR credential from both docker *and* helm, and removes
  it on every exit path — an earlier version isolated docker only, so a run
  silently added `repo.f5.com` to the operator's persistent helm credentials.

  The audit reports a **control** per section and refuses to report findings when
  a control fails, so a broken extraction shows up as "not reporting it" rather
  than as a clean bill of health. That caught a false positive during this work:
  `fullnameOverride` was checked against the wrong chart and looked dead, when it
  is real in `f5-ipam-controller`. Checking a name against the wrong consumer is
  how an audit deletes working configuration, so helm keys are now scoped to the
  chart that owns them and nested sub-chart values are reported as *unaudited*
  rather than silently assumed.

- **`bnk.gtm.*` is gone — it configured nothing** (#227). The block carried the
  GTM / BIG-IP DNS `url`, `username` and `password_b64`, rendered them as
  `cneinstance_gtm_*` tfvars, and terraform emitted them onto the CNE controller
  as `GSLB_GTM_URL/USERNAME/PASSWORD` **and** a bare `GTM_URL/USERNAME/PASSWORD`
  pair. The code's own note said `VERIFY against BNK 2.3 and drop the pair that
  is not real`, and had shipped that way.

  The answer is that **neither pair is real, on either line**. The f5ingress
  controller binary contains **zero** occurrences of all six names in both
  2.3 `v14.59.1-0.0.70` and 2.4 `v14.91.12-0.1.66`, while the controls behave
  exactly as expected — `GSLB_DATACENTER_NAME` and `CLOUD_PROVIDER` present in
  both, `USE_GATEWAY_SETTINGS` only on 2.4. F5's approved 2.4 reference
  `cneController.env` carries no GTM connection variables either.

  So the whole surface — config field, `ROKSBNKCTL_GTM_*` overrides, tfvars,
  terraform variables, book entries, and the rendered environment — expressed
  nothing, and it put a password into a pod environment twice to do it.

  **`bnk.gslb_datacenter_name` is real and stays.** A workspace or blueprint
  still carrying `bnk.gtm` needs no edit: an unrecognised key is ignored, and it
  was expressing nothing before.

  This is the fourth setting removed for being inert (#175, #186, #210), and the
  first with a credential in it. #228 tracks the audit that found two more dead
  environment variables and two dead helm values, plus the surfaces still
  unchecked.

### Added

- **A generated `config.yaml` cheatsheet**, published at
  [jgruberf5.github.io/roksbnkctl/config-cheatsheet.html](https://jgruberf5.github.io/roksbnkctl/config-cheatsheet.html)
  and checked in at `scripts/demos/config-cheatsheet.html`. One white page listing
  every field the workspace schema accepts, with the **full dotted path** an
  operator actually types (`bnk.flp.vsi.subnet_cidr`, not `SubnetCIDR` on
  `FLPVSICfg`) and the `ROKSBNKCTL_*` override that sets it. Filter-as-you-type,
  and toggles for required / has-an-override / BNK 2.4.

  Generated, not written. It walks the same `Workspace` struct the loader uses
  and takes the override mapping from the derived probe rather than a parallel
  table, so it cannot list a field that does not exist or miss one that does.
  `make generate` refreshes it and `make release` runs that first, so the version
  badge rolls forward with the release; `TestConfigCheatsheetIsCurrent` fails the
  build if the checked-in copy drifts.

  The **Req** column comes from `config.RequiredConfigFields`, the one list
  `init` itself checks — not from `omitempty`, which is a marshalling directive
  and says nothing about whether a value must be supplied. Deriving from it
  marked 25 fields required when four are, and missed `prefix`.

  It also documents what is **deliberately not** in `config.yaml` —
  `IBMCLOUD_API_KEY`, `BNK_FORGE_PASSWORD`, `BNK_FORGE_USER`, `BNK_FORGE_URL` —
  because a fields-only page answers "every field" and not "how do I set the
  password", and the two are easy to confuse for *unsupported*.

- **`bnkforge.password_b64`**, so every machine credential this tool takes has
  the same shape. It was the only password with no `_b64` field, which meant
  remembering that one exception. Precedence is `BNK_FORGE_PASSWORD`, then
  `--password`, then the field, then the prompt — the environment still wins, so
  a stored value never stops a runner overriding it. Seedable via
  `ROKSBNKCTL_BNKFORGE_PASSWORD`.

  base64 is obfuscation, not encryption. Storing it trades a long-lived secret on
  disk for not being asked, which is right for an unattended runner and wrong for
  a laptop.

- **`scripts/generate_b64_password.sh`** — prompts twice without echoing and
  prints the base64 for any `*_b64` field. It exists because `echo secret | base64`
  puts the password in shell history, appends a newline that some consumers
  reject as a wrong password, and wraps at 76 columns on GNU. It also states the
  rule the variable names encode: `ROKSBNKCTL_*_B64` take the base64,
  `ROKSBNKCTL_*_PASSWORD` take the RAW value and encode it themselves — feeding
  one the other double-encodes and the credential silently does not work.

## v1.56.0 — 2026-08-26

**Three things BNK Forge and the registry mirror were handed that they could not
use.** A credential template that matched nothing, no way to give Forge an
appliance's SSH key, and an `adopt` that read its own source unauthenticated.

### Added

- **`roksbnkctl bnkforge ssh-credential`** gives BNK Forge the SSH private key
  for an appliance this workspace built, and attaches it to the project (#222).
  Without it a healthy F5 License Proxy reports itself unreachable:

  ```text
  infrastructure_private_key_available: false
  infrastructure_access_status:         recovery_required
  ```

  Nothing could be recovered — the credential was never created. `bnk.flp.vsi.ssh_key`
  names an IBM Cloud VPC key, which puts the **public** half on the VSI; that is
  operator access. Forge separately needs the **private** half, and nothing
  supplied it.

  It lives here because it cannot live in a Forge module step: Forge requires
  container steps to be an argv vector and refuses a shell, and this is three
  calls in sequence where the bearer token from the first has to reach the
  second's header.

  The appliance's SSH user is `--ssh-username`, not `--username` — every other
  `bnkforge` subcommand uses `--username` for the Forge login, and overloading it
  meant `--username admin` silently set the appliance user while the operator
  believed they had authenticated to Forge.

  Two things it refuses to get wrong. `--host` must be the address **Forge** can
  reach — for an FLP the floating IP, not the services-VPC endpoint `flp status`
  prints — and passing a URL is rejected with an explanation. And the key's
  SHA256 fingerprint can be checked against `--expect-fingerprint` before
  anything is stored: a credential that cannot log in is worse than none, because
  Forge then reports access as configured and every later failure points
  elsewhere.

  It configures infrastructure access through the endpoint that owns it —
  `POST /api/cloud-auth/ssh/configure` — which also **tests the SSH connection
  before storing anything**, so a key that cannot open the box is refused at the
  source rather than stored and discovered later. It still reads the project back
  and names anything that did not stick.

### Fixed

- **`registry adopt --verify-contents` resolved source digests unauthenticated**
  (#224). Against a fully populated 94-artifact mirror it reported:

  ```text
  ✗ charts/coremond: resolve source: ... DENIED: Unauthenticated request
  adopt --verify-contents: 87 of 94 artifacts are missing or digest-mismatched
  ```

  `registryEngine` copies the FAR credential at construction, and `buildBOM` is
  what resolves it from COS when the workspace does not set
  `registry.source_service_account_b64` — so the engine was built holding an
  empty credential. The 7 artifacts that passed were the non-F5 dependencies
  whose sources are public, which is what made it look like a mirror problem
  rather than a credential one. `replicate` and `verify` never hit it because
  they build the BOM first. The engine is now constructed after the BOM.

- **`registry adopt` no longer refuses a populated Artifactory mirror** (#224).
  Artifactory's repository-path access method answers the registry-wide
  `/v2/_catalog` with an empty body; the repositories live under the
  per-repository catalogue at `/v2/<repo>/_catalog`. The probe saw zero and
  `adopt` refused without `--force`. It now retries against the scoped
  catalogue — but only believes an answer that differs from the root's, because
  many registries ignore the path and echo the whole catalogue, which would turn
  a typo'd `generic_repo_prefix` into a healthy-looking mirror and defeat the
  one check the probe exists to perform.

- **The IBM credential template `bnkforge register` creates is no longer inert**
  (#223). It was written with `provider: "IBM"`, and Forge compares
  `provider == "ibm"` case-sensitively in at least seven places without
  lowercasing first. The API accepted the value and then matched nothing:
  credentials were never injected into a deployment, blueprint inputs sourced
  from `credential_template` never resolved, the IBM lookup found nothing, and
  the "IBM templates must carry an API key" validation never fired. Nothing
  errored — the template simply did nothing, in both directions.

  `region` and `ibm_cos_instance_name` are now populated from the workspace too,
  so blueprint inputs declaring `source: credential_template` have something to
  inherit instead of null. Unset values are omitted rather than sent as `""`,
  which would overwrite a hand-set value with an empty one.

  The corrected `provider` is sent on the **update** path as well as create, so a
  template a previous roksbnkctl wrote as `"IBM"` is repaired rather than left
  broken forever — the already-created templates are the reported symptom.

## v1.55.1 — 2026-08-26

**Two `bnk down` defects, both of which could leave an operator stuck.** One cost
five minutes and an alarming `Error:` on every teardown; the other made teardown
impossible without hand-editing a file the tool says not to edit.

### Fixed

- **`bnk down` no longer stalls for five minutes on the BNK namespace** (#217).
  terraform destroyed the `CNEInstance` and FLO as two independent graph nodes:
  alekc/kubectl's delete returns as soon as the API server accepts it, so the
  CNEInstance was merely *marked* for deletion while its controller-owned
  children still carried F5 finalizers. Three seconds later `helm_release.flo`
  was destroyed and the only thing that could clear those finalizers was gone,
  so the namespace delete blocked until the kubernetes provider's timeout:

  ```text
  kubectl_manifest.cneinstance[0]: Destruction complete after 0s   <- no wait
  helm_release.flo[0]:             Destruction complete after 3s   <- finalizer gone
  kubernetes_namespace_v1.flo[0]:  Still destroying... [4m0s elapsed]
  Error: context deadline exceeded
  ```

  Observed on 3 of 3 teardowns — deterministic, not a race. Every `bnk down`
  cost ~5 minutes and printed an `Error:` that operators had to learn to ignore,
  which is the expensive part: a log that cries wolf on every successful run
  stops being read.

  `bnk down` now drains BNK's custom resources BEFORE terraform starts, while
  FLO is still running to finalize them. The order is the 2.4 guide's own
  (`UninstallOrder`): every other F5 CR first, **confirmed gone**, and only then
  the CNEInstance — the guide is explicit that IPAM and IPAMRange must be
  verified absent first, "to avoid any leftover state that might cause issues
  during product reinstallation", because they are controller-generated and
  removing the CNEInstance takes away what would have cleaned them up. If the
  leaf phase does not drain, the CNEInstance is deliberately left alone rather
  than deleted anyway, which would reproduce the bug inside the fix for it.

  The kinds are DISCOVERED from `BNKCRDGroups`, not listed: a hardcoded list had
  three entries while the live 2.4 capture shows sixteen finalizer-bearing F5
  CRs, so a list would sweep three and report success.

  `freeStuckBNKNamespace` stays as the safety net — its own comment already
  called itself "a REPAIR, not a substitute for the ordering fix that would stop
  the CNEInstance being orphaned". This is that ordering fix.

  Runs on the containerised backend too. The destroy ordering lives in the
  terraform graph, which is identical whichever process runs it.

  A failing API server is no longer read as an empty namespace. Counting every
  list error as zero objects meant an unreachable cluster reported a successful
  drain, having verified nothing — the same misreporting this change exists to
  remove, in the other direction. A deleted CRD is still not waited for, since
  that errors on list too and waiting would add the timeout to every teardown.
- **`bnk down` could not destroy a workspace whose applied snapshot held an
  unterminated block** (#219). Reported by an end user:

  ```text
  Error: Missing item separator
    on .../.applied-replay.tfvars line 6:
     5: cneinstance_network_zones = [
     6: cneinstance_tmm_k8s_routes = "172.17.0.0/18,172.21.0.0/16,192.168.100.0/24"
  ```

  `terraform.applied.tfvars` is only rewritten on apply, so every retry
  regenerated the same unparseable replay file and the workspace could not be
  torn down at all. The only escape was hand-editing a file the tool says not to
  edit.

  Two defects, and the second was silent. The parser kept the opening fragment
  as the value — its own comment claimed that "at least round-trips as something
  parseable", and `[` does not — and its continuation scan then consumed the
  rest of the file hunting for a closing bracket, so **every assignment after
  the bad block was dropped**. A four-variable snapshot parsed to two, quietly;
  had terraform accepted the file, the destroy would have run with less than the
  apply used.

  An unterminated block is now dropped and parsing resumes at the following
  line. Dropping is safe exactly where it happens: the replay is the
  lowest-precedence var-file in the chain, so the key falls through to the config
  render layered after it, and `cneinstance_network_zones` is `default = []`.
  The replay writer also refuses to emit any value whose brackets do not balance,
  naming it in a comment rather than dropping it silently.

  Fixing this exposed a latent bug in the bracket counter: it stopped at the
  first `#`, which is right for one line and wrong for a joined multi-line value,
  so a well-formed zone list containing a comment would have been reported
  unbalanced and skipped. Comments now skip to end-of-line.

  A block that closes *too* hard (`over = [` / `]]`) is treated the same way as
  a truncated one — malformed input, dropped, parsing resumes after the opener.
  It was previously reported as closed while its value was still unbalanced. And
  `terraform.applied.tfvars` itself gets the same balance guard as the replay
  file: guarding only the file terraform reads left the file everything is
  derived *from* able to persist invalid HCL, which is the self-perpetuating
  half of this bug.

## v1.55.0 — 2026-08-26

**`roksbnkctl config` writes the configuration you were transcribing by hand.**
Two things configure this tool — a human editing `config.yaml` and a CI runner
passing `ROKSBNKCTL_*` variables — and moving between them meant copying a
hundred names across by hand, which is how a `.env` ends up naming a variable
that does not exist.

### Added

- **`config yaml` and `config env` print the workspace input, in either form**
  (#215). Both take `--from-yaml` or `--from-env` to convert a populated file
  instead of printing an annotated template, so the output is pipeable:

  ```
  roksbnkctl config yaml > config.yaml            # annotated template
  roksbnkctl config env  > .env                   # the same, as overrides
  roksbnkctl config env  --from-yaml config.yaml  # populated, no comments
  roksbnkctl config yaml --from-env  .env         # the round trip
  ```

  The conversion runs the SAME override machinery `--override-from-env` uses, so
  a value lands where it would in a real run because it goes through the rows
  that put it there. A second parser would have been a second thing to keep true.

  The name-to-path mapping is **derived, not declared**: each override is applied
  to an empty workspace on its own and the path that moves is the answer. A
  parallel table is precisely the defect this codebase keeps finding — a list
  every grep confirms and nothing keeps true — and a probe cannot drift, because
  the thing it interrogates is the machinery itself. All 124 overrides resolve to
  a config path, and the test asserts there are no exceptions: an override that
  sets nothing a marshal can see is the inert-setting defect, not a probe
  limitation.

  Secrets are **named but not printed**. `config env --from-yaml > .env` would
  otherwise write `IBMCLOUD_API_KEY`, `ROKSBNKCTL_API_KEY_B64` and
  `ROKSBNKCTL_BIGIP_PASSWORD` values to disk — from a command whose own template
  tells the reader to keep secrets out of the file. The variable stays
  discoverable with an empty value and a comment. The filter keys on the config
  path's naming convention (`*_b64`, `password`, `secret`, `token`, `api_key`)
  rather than a list of variable names, so a new secret field is covered on
  arrival. `bnkforge.ca_b64` is deliberately exempt: it is a public certificate,
  encoded only for single-line YAML safety, and withholding it dropped a working
  setting to protect nothing.

  Values are shell-quoted, lists survive the round trip, and fields left at their
  zero value are skipped rather than emitted as an assertion the user never made.

- **`internal/cli/env.example` is generated** from the same probe plus the config
  struct's own doc comments, so a new override appears in the template without
  anyone adding it, and `TestEnvExampleIsCurrent` fails when it does not.

### Fixed

- **Chapter 27 is generated and was hand-edited, with nothing checking it**
  (#215). It had drifted, and adding a command left the command reference
  silently missing it. Chapters 28 and 29 were already guarded; 27 now is too.

## v1.54.0 — 2026-08-25

**BNK 2.4 on ROKS is verified at four cluster sizes, and `deploymentSize` is not
the knob you scale with.** All four shapes in Appendix C — 3, 6, 6 and 9 workers
— run `deploymentSize: Tiny` with `tmmReplicas` of 1, 3, 3 and 9. Capacity comes
from pod count and node size. Sizes above `Tiny` request hugepages that ROKS has
no supported way to allocate, which is why `bnk.hugepages` now refuses with an
explanation instead of applying a `Tuned` CR the platform deletes.

Two silent installs are fixed: FLO was skipping the CSRC component entirely
under `containerPlatform: OCP`, and `bnk down` was leaving the namespace stuck
in `Terminating` behind a webhook whose service it had already deleted.


### Removed

- **`bnk.flp.vsi.reach` did nothing, and is gone** (#210). It had a config
  field, a `ROKSBNKCTL_FLP_VSI_REACH` override, a book entry and a tfvars
  render — and no terraform read it. Setting it changed the generated tfvars
  and nothing else.

  It is not superseded, it is obsolete. The FLP VSI module decided the question
  the setting was asking: `local.reach_ip` is *always* the private VPC address,
  with the comment "the consuming cluster reaches the proxy privately (same VPC
  or over a Transit Gateway)". The floating IP that `reach: floating` would have
  selected is a **management** path — remote `flp status` and the `:80` web UI —
  explicitly "NOT the CWC endpoint". `bnk.flp.vsi.floating_ip` still controls
  that, and is unaffected.

  A workspace or blueprint carrying `bnk.flp.vsi.reach` needs no edit: an
  unrecognised key is ignored, and it was expressing nothing before. Removing
  it is visible only in that the tool no longer advertises a setting it does not
  honour.

  Third of this shape after `cneinstance_advanced_env` (#175) and
  `install_cert_manager` (#186), and the first found by the guard added for
  #204 rather than by an operator hitting it.

### Fixed

- **FLO installed BNK without CSRC, and said nothing** (found while
  investigating #197; fixed in #198, guarded in #201). `containerPlatform` was
  set to `OCP` on the reasoning that ROKS is OpenShift. That reasoning applies
  to the *cluster* distribution; the field selects F5's *platform integration*,
  and under `OCP` the lifecycle operator creates its sixteen component CRs and
  **silently skips CSRC** — `csrcs=0`, no `f5-spk-csrc` pods, and therefore no
  `macvlan-internal` NetworkAttachmentDefinition, which CSRC creates at
  runtime. Nothing is logged. Every other signal reports healthy.

  The value is now hardcoded to `IBM`, which is what F5's own reference cluster
  runs and what produces `csrcs=1`, a six-pod CSRC DaemonSet and the NAD.
  `Generic` is not an alternative: the controller aborts looking for a
  `kubeadm-config` ConfigMap that no ROKS cluster has.

  It is deliberately **not** configurable — no variable, no tfvars field, no
  `ROKSBNKCTL_*` override — because the failure is silent, so an operator who
  could change it would have no signal that they had broken the install. The
  guard asserts the literal, that there is exactly one of them, and that it is
  not reachable through `var.`/`local.`/`coalesce`/`try`/`lookup`.

  **#197 remains open.** The commit subject for #198 carries `(#197)` because
  the investigation started there, not because it fixes the shared
  `ReadWriteOnce` volume that issue describes.


- **`bnk up` waited fifteen minutes to say nothing useful** (#189). `tfx wait`
  built its condition description from the status alone — `Available=False` —
  and dropped the condition's `reason` and `message`. The scheduler's actual
  verdict lives in that message, so `terminalWaitDiagnosis` had nothing to read
  and a **permanently** unschedulable pod was indistinguishable from one still
  starting. Every such install burned the full timeout and then failed with a
  terraform local-exec error naming neither the pod nor the cause.

  The description now carries reason and message, and the diagnosis recognises
  the case 2.4 actually hits: the TMM replicas share one `ReadWriteOnce` volume
  while F5's own reference placement pins them to different nodes, so no
  placement satisfies both and no node will ever gain the ability to fix it. It
  fails immediately, naming the constraint and the two settings worth trying.

  Deliberately still narrow — it requires **0** available nodes. A pod that is
  Pending for an unbound PVC, an image pull, or a node still joining may
  genuinely resolve by waiting, and failing those early would trade a slow
  success for a fast wrong answer.

### Changed

- **Chapter 12 is no longer a second copy of the configuration schema** (#184).
  It carried eight field-spec tables — `prefix:`, `ibmcloud:`, `cluster:`,
  `resources:`, `bnk:`, `bnk.network:`, `test:` and `cos:` — restating 68
  descriptions that Chapter 28 already generates from the `Workspace` struct.
  Two copies of a schema is one copy plus a thing that will disagree with
  itself, and it already had: `resources.tgw_jumphost` was documented as
  defaulting to `create: true` when the testing client has been opt-in
  (defaulting to *off*) since it moved behind the "Add a testing client?"
  prompt, and `cluster.create` was documented as defaulting to `true` when the
  Go field is a plain `bool` whose zero value is `false`.

  Every one of those tables is gone, replaced by a link to the exact section of
  Chapter 28 that lists the block's fields. Chapter 12 keeps everything a
  generated table cannot carry: the worked `init` transcript, the YAML
  examples, "When it gets written", "Behaviour when fields are missing",
  "Editing by hand vs helpers", and the `tf_source:` / `exec:` tables, which
  enumerate *values* of one field rather than fields.

  Chapter 28 publishes only the **first sentence** of each doc comment, so ten
  fields carried real guidance in Chapter 12 that deleting the row would have
  destroyed. That guidance was rewritten as prose rather than dropped —
  `cluster.public_gateway` (what a disconnected cluster obliges you to provide),
  `cluster.vpc_cidr` (why IBM's `auto` prefixes stop two clusters sharing a
  Transit Gateway, issue #46), `cluster.existing_subnet_ids` (zone order is read
  from the subnets, so a reordered list silently places the cluster
  differently), `cluster.network_mode` (create-time only and refused on
  disagreement), `bnk.trusted_profile.service_account` (an IAM **matcher**, not
  a pointer: a wrong name makes the profile unassumable and reports nothing),
  `bnk.gtm.*` and `bnk.gslb_datacenter_name` (a datacenter name without a GTM
  URL is a label pointing at nothing), `bnk.cneinstance_size` (unvalidated on
  purpose), `bnk.network.vlan_prefixlen` (independent of the VLAN CIDRs and
  never derived from them), `prefix` (the 35-char ROKS cluster-name cap and the
  label charset), and `ibmcloud.api_key_b64` (obfuscation, not encryption).

### Fixed

- **2.4's TMM has never actually reconciled** (#189). `containerPlatform` was
  hard-coded to `"Generic"` in the FLO helm values. ROKS **is** OpenShift — this
  tool builds and adopts nothing else — and F5's chart notes these platforms "may
  have specific installation logic in the component controllers". Under `Generic`
  the CNE controller looks for the `kubeadm-config` ConfigMap only kubeadm-built
  clusters have, aborts at `Reconciled=False`, and never creates TMM's internal
  macvlan NAD.

  It is now `"OCP"`, and **not** a setting: there is no cluster this tool targets
  where anything else is true. An earlier revision of this change shipped it as a
  knob defaulting to `Generic`, which was a way of avoiding the decision while
  continuing to ship a broken `F5Tmm`.

  Two clean installs differing only in that value: `Generic` →
  `Reconciled=False`, `OCP` → `Reconciled=True`.

  This also reframes what looked healthy. `F5Tmm.spec.persistence` is true on
  **both**, so the 3/3 TMM pods every 2.4 install has reported were a *side
  effect of the bug* — the controller never got far enough to ask for TMM's
  volume.

- **`bnk.storage_class_name`**, because a reconciling TMM needs one. Its replicas
  are pinned to separate nodes across separate zones by the placement F5's own
  reference prescribes, while their volume is shared — so the stock ROKS default
  (`ibmc-vpc-block-*`, ReadWriteOnce, zonal) binds one replica and the rest stay
  `Pending`. A ReadWriteMany class from the `vpc-file-csi-driver` addon serves all
  three; `ibmc-vpc-file-regional` also spans zones. Emitted only when set, so
  unset leaves the CR's own default.

- **Twenty-three `bnk.*` settings did nothing on either release line** — found
  while establishing the line applicability of every config field (#182). The
  root module declared `cneinstance_tmm_replicas` and twenty-two siblings (the
  whole TMM placement set, `external_bigip*`, `cluster_identifier`,
  `gateway_api_version`, `demo_mode`, `whole_cluster`, `tcp_settings*` and the
  five `hugepages` keys); the Go renderer wrote each into `terraform.tfvars`; the
  config reference documented them; and `module "cne_instance"` in
  `terraform/main.tf` passed none of them down. The wrapping module declares
  variables of the same names with the same defaults, so `var.cneinstance_*`
  inside it resolved to the default and the operator's value went nowhere.

  This is the `cneinstance_advanced_env` defect with one extra layer of
  camouflage: `TestEveryRootVariableIsRead` greps the tree for `var.<name>`,
  finds the hit inside the child module, and pronounces the root variable read.
  A grep proves a name exists, never that its two halves are joined.
  `TestEveryRootVariableReachesTheModuleThatReadsIt` now checks the join at every
  module call in the tree.

  The defaults are identical at all three levels, so an install that sets none of
  these renders exactly what it rendered before — what changed is that setting
  one now does something. Anyone who had set `bnk.hugepages.enabled: true` or a
  non-default `bnk.tmm_replicas` and saw no effect was seeing this.

- **`bnk.flo_namespace` never reached the licensing module**, so the 2.4
  post-licensing health check polled the CNEInstance in `f5-bnk` regardless of
  the configured namespace — a fifteen-minute wait on an object in a namespace
  that does not exist. Same defect class as #65.

### Changed

- **The `line` column in the configuration reference now says something** (#182).
  It used to report "2.3 + 2.4" for 189 fields because that is the unset default,
  not because anyone had checked. Nine fields are now tagged from terraform
  evidence rather than left to the default:

  - **2.3 only** — `bnk.network.vlan_prefixlen`, `vlan_prefixlen_external` and
    `vlan_prefixlen_internal` (their only consumer is the `F5SPKVlan` pair, gated
    to `line_pre_24`); `bnk.network.zones[].int_vlan_cidr`, `external_selfip` and
    `internal_selfip` (the `cloud-network-mapping` ConfigMap and the same
    `F5SPKVlan` pair — 2.4's `Infra` CR has no internal VLAN and allocates TMM's
    addresses from an IPAM pool instead of naming them per zone).
  - **2.4 only, now true rather than aspirational** — the fourteen fields already
    tagged 2.4 were verified, and were inert on both lines until the wiring fix
    above.

  What did NOT get tagged matters as much. `zones[].ext_vlan_cidr`,
  `int_snat_cidr` and `int_vip_cidr` feed the 2.3-only `cloud-network-mapping`
  ConfigMap and look 2.3-only for it — but `terraform/modules/gateway/infra_24.tf`
  builds the 2.4 `Infra` CR's three IPAM pools out of the same three values.
  Tagging them 2.3 would have hidden the addressing a 2.4 operator most needs to
  get right, which is worse than the under-promising default it replaced. They
  are pinned as "both" by the same test that pins the tags.

  `TestConfigLineTagsMatchWhatTerraformRenders` asks terraform the question
  directly: for each line, does perturbing the variable change the set of objects
  the apply persists? The surface is assembled from each resource's own
  `count`/`for_each` expression, lifted out of the HCL and evaluated by
  `terraform console`, so an inverted or commented-out gate changes the answer.
  `TestEveryLineTagHasEvidence` refuses a new tag that arrives without one.

- **`test.throughput.image` was documented as the wrong image, for the wrong
  end of the test.** Chapter 12 said the `k8s` backend substitutes
  `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3` for it. It does not: that field is
  the in-cluster iperf3 **server** fixture and is used whatever the backend, and
  the GHCR tool images are the iperf3 **client** the `docker`/`k8s` exec
  backends run. The chapter now says so, and records the reason you would
  actually override it — the default `networkstatic/iperf3:latest` runs as root,
  which OpenShift's SCC admission papers over but a plain Kubernetes cluster
  with the `restricted` Pod Security standard rejects.

- **The orchestration COS defaults are now generated rather than asserted**
  (#184). `cos.instance` / `cos.bucket` / `cos.region` published no default in
  Chapter 28 while Chapter 12 claimed `bnk-supply-chain` / `bnk-artifacts` /
  `us-south` in prose. They carry `default:"..."` struct tags now, so the
  reference publishes them and they are checked against
  `terraform/variables.tf` like every other default. Chapter 12 keeps the part
  a tag cannot express: `init` provisions
  `bnk-artifacts-<first 12 of the account id>`, because COS bucket names are
  globally unique and keying the suffix off the account makes the name both
  collision-free and discoverable by a second workspace from the same account.

- **Every config field in the reference now has a description** (#181). 75 of
  215 rows shipped blank; the count is now **zero**, and the ratchet's ceiling is
  zero — a new field without a doc comment on the struct fails the build.

  They were filled in order of what a wrong answer costs, not what was quickest.
  `bnk.manifest_version` is the single field selecting the 2.3 or 2.4 model and
  had no description at all; `resources.cert_manager` now says why you would set
  `create: false` and what fails if you do not;
  `gateway.client_subnet_remote` records that getting it wrong does not fail the
  apply — traffic simply never returns; `tf_source.ref` records that pinning a
  branch lets two applies days apart deploy different infrastructure from
  identical config.

  Several were blank for a reason worth noting: a shared comment describing two
  fields attaches to only one of them, orphaning the rest. `MinWorkerVCPUCount`,
  `SubscriptionJWTLocalFile`, `FLOUtilsNamespace`, `VLANPrefixLenInternal` and
  the jumphost sizing pair were all documented in prose that the generator could
  not see.

- **The last 19 blank descriptions in the configuration reference are filled**
  (#181). v1.53.0 took 75 to 19; the count is now **zero** and the ratchet's
  ceiling is zero, so a new config field without a doc comment on the struct
  fails the build rather than shipping an empty cell.

  Several were blank for a reason worth recording: a shared comment describing
  two fields attaches to only one of them, orphaning the rest.
  `MinWorkerVCPUCount`, `SubscriptionJWTLocalFile`, `FLOUtilsNamespace`,
  `VLANPrefixLenInternal` and the jumphost sizing pair were all documented in
  prose the generator could not see.

- **`bnk up` installs the Gateway API 1.5 bundle, from the mirror** (#185). BNK
  2.4's `crd-installer` no longer forces its own Gateway API CRDs — it logs a
  graceful skip and leaves the cluster on whatever bundle OpenShift ships, which
  is right for a base install and wrong for mTLS. Nothing on the cluster
  installed the standard channel the CNE controller was being told to expect, so
  `gateway_api_mtls: true` swept a platform admission policy out of the way and
  then put nothing in the gap.

  With `gateway_api_mtls` on and the line at 2.4, the bundle now enters the BOM
  and is applied to the cluster: 8 CRDs, a `ValidatingAdmissionPolicy` and its
  binding, no container images. `registry bom` lists it, `registry replicate`
  copies it, `registry verify` proves it arrived — the same three guarantees
  every chart and image already gets, and the reason it is carried *in* the
  mirror rather than beside it: a disconnected cluster can only reach the mirror,
  and in the CI path roksbnkctl runs as a pod inside that cluster, so its egress
  is the cluster's and `github.com` is unreachable for exactly the estates that
  want mTLS. Off the mirror path it is fetched from
  `bnk.gateway_api_bundle_url`, or from the upstream release.

  The version is not a new setting: it is `bnk.gateway_api_version`, the value
  already rendered as the controller's `GATEWAY_API_VERSION`. One accessor
  answers both, so the bundle on the cluster and the release the controller was
  configured for cannot be resolved from different places and disagree — a test
  pins the Go default against the terraform one.

  The bytes are checked against a **sha256 that ships in roksbnkctl**, on the way
  into the mirror and again on the way out. A release with no pin in the build is
  refused rather than fetched unverified: this is a megabyte of cluster-scoped
  CRDs applied with `--force-conflicts`, and a pin an operator can retype is a
  pin an attacker upstream can talk them out of. A separate test fetches the real
  release and re-establishes the pin against it, so the pin cannot quietly become
  a description of nothing.

- **`bnk.gateway_api_bundle_url`** (#185) — where that bundle is fetched from
  when no mirror is recorded, for an estate that blocks `github.com` but proxies
  it internally. `ROKSBNKCTL_GATEWAY_API_BUNDLE_URL` in the environment, in the
  demo `.env.example` allowlist, and rendered as the `gateway_api_bundle_url`
  tfvar — terraform installs nothing from it, but its `validation` block is what
  rejects a malformed URL at plan time instead of letting it surface as a fetch
  failure once the apply is under way. It moves only *where* the bytes come from;
  the sha256 pin for the configured version still applies.

### Fixed

- **The gateway-api admission sweep could have deleted what the bundle installs**
  (#185). The bundle ships a `ValidatingAdmissionPolicy` of its own,
  `safe-upgrades.gateway.networking.k8s.io`, and it is applied while the sweep is
  running and deleting OpenShift's
  `openshift-ingress-operator-gatewayapi-crd-admission`. Two different objects —
  but nothing checked that, and a sweep widened to a prefix, a label or a
  delete-collection would have removed the bundle seconds after it landed. That
  failure is silent: no error, no denied write, just an absent policy and an
  install that looks as though the bundle never applied.

  `bnk up` now refuses to install an object the running sweep would delete, and
  a test drives the real sweep loop against a cluster holding both policies and
  checks which one is standing afterwards. Broadening the sweep by one name fails
  it.

  The 2.4 sweep gate and the bundle are also now the same question asked once,
  rather than two copies of it — a build that sweeps without installing anything,
  or installs the bundle into a window nothing is holding open, is a build where
  one half does nothing.

- **A 2.4 install was still rendering the 2.3 network surface** (#187). The
  `cloud-network-mapping` ConfigMap, both `F5SPKVlan` CRs, and the
  `CLOUD_NETWORK_CONFIGMAP` env pointing at them were gated on
  `local.use_kubectl` alone, with no line gate — so a 2.4 cluster carried both
  network models at once, which the 2.4 guide warns causes device IP conflicts.

  The consequence was invisible from every signal we were checking: `F5Tmm` sat
  at `Reconciled=False` ("failed to get kubeadm-config"), so the internal
  `macvlan-internal` NAD it owns was never created — while the CNEInstance
  reported 18/18 conditions True, all 34 F5 pods Running and the licence Active.
  Reproduced independently on two installs, a Forge blueprint and a
  disconnected-adopt CLI run.

  The env var is spliced back **in position** on 2.3 rather than appended, so a
  shipping 2.3 install sees no CNEInstance diff from this change.

  The conformance suite could not have caught it: every check asserted what the
  spec *contains* and none what it must not. `TestTwoFourDoesNotCarryTheTwoThreeNetworkEnv` now evaluates the controller env through `terraform console` on both
  lines, so a gate that exists but is wired to the wrong local still fails.

- **The CLI and CI demos did not say what their variables default to.** Eight of
  seventeen and eight of fourteen configurable variables carried no comment, so
  a reader had to infer `REGION`, `OCP_VERSION`, `FAR_REPO_URL`, `PREFIX` and
  the rest from the `${VAR:-default}` expansion. Every one now states its
  meaning and its default, in both the script and the `.env.example`.

  One of the comments that *did* exist was wrong: `FORGE_URL` was marked
  **required**, which stopped being true when Forge became optional. It is all
  three `FORGE_*` or none — a partial configuration dies deliberately, because
  someone who set two of three meant to use it.

- **The configuration reference had the same gap, from the other direction.**
  Its `default` column is filled from a `default:"..."` struct tag, so any field
  whose real default lives in terraform published an em-dash — including
  `gateway.class_name`, `gateway.vxlan_port`, `bnk.far_repo_url` and the
  testing-jumphost sizing. Eighteen keys were affected; the renderer omits each
  of those tfvars when unset, so terraform's default *is* what an operator gets.
  Thirteen are now published (34 → 47 rows with a default).

  Two are deliberately still blank — `bnk.cis.bigip_url` and `bigip_username`,
  whose terraform values are placeholders (`192.168.1.245`, `admin`) rather than
  anything a user receives. Publishing those would imply a BIG-IP is configured.
  `TestPublishedDefaultsCoverTheTerraformDefaults` fails for any other key in
  that state, and also fails if an exemption goes stale.

## v1.53.1 — 2026-08-23

### Fixed

- **Fourteen reference descriptions were being cut off mid-phrase.** The
  generator ended the published sentence at the first `". "`, so any
  abbreviation truncated it — `bnk.manifest_version` shipped as "pins the BNK
  release, e.g.", stopping immediately before the clause explaining that it is
  the field selecting the 2.3 or 2.4 model. Six more broke on the parenthesised
  `(e.g.` form, including `registry.icr_host` and `cluster.openshift_version`,
  which had been wrong since long before this release.

  The undocumented-field ratchet cannot catch these: the rows are not blank,
  only cut short. `firstSentence` now recognises abbreviations and single-letter
  initials, with a unit test covering each form.

## v1.53.0 — 2026-08-23

### Added

- **56 of the reference's blank descriptions filled** (#181): every top-level
  config section, the seven `resources.*` create/adopt toggles, the six per-zone
  network-mapping fields, and the `cluster`, `bnk`, `ibmcloud`, `tf_source`,
  `test` and `gateway` blocks, the registry credentials and the COS block. 75 → 19, and the ratchet ceiling moves down with
  each step.

  These were chosen by what a wrong answer costs, not by what was quickest. Some
  examples: `bnk.manifest_version` is the single field that selects the 2.3 or
  2.4 model, and had no description at all; `resources.cert_manager` now says
  why you would set `create: false`, what fails if you do not, and that adopting
  also protects the customer's cert-manager from `bnk down`;
  `gateway.client_subnet_remote` records that getting it wrong does not fail the
  apply — traffic simply never returns; and `tf_source.ref` records that pinning
  a branch lets two applies days apart deploy different infrastructure from
  identical config.

- **The undocumented-field ratchet was grading the wrong column** (#181). It
  hard-coded column index 5 and read `required`, which is never blank — so it
  reported **0 undocumented fields out of 215** and had been passing vacuously
  ever since the table gained a `default` column. The true figure is **75 of
  215**. It now finds `description` from the header row and refuses to run at
  all if it cannot, so the next column added cannot blind it again.

- **A mirror we could never authenticate to is refused before the apply, not
  fifteen minutes into it.** The chart pull picks its credential by falling
  through a chain whose last arm uses the literal username `unused` with the
  cluster's kube token. That arm is right for the in-cluster OpenShift registry
  and never right for an external one, so a `generic` registry configured with a
  username but no password reached Artifactory as `unused` and was answered
  `401: Bad Credentials` — after flo was installed and IAM trusted profiles were
  created, naming neither the missing setting nor the file it lives in.

  `bnk up` now refuses at the start, naming both
  `registry.generic_password_b64` and `ROKSBNKCTL_GENERIC_PASSWORD`. The rule is
  narrow — a username with no password — so anonymous mirrors and the in-cluster
  registry path, which set neither, are untouched.

- **`resources.cert_manager: {create: false}` now actually skips cert-manager.**
  It never did. `install_cert_manager` was declared in `variables.tf`, rendered
  into `terraform.tfvars` by the Go side, and carried in
  `terraform.tfvars.example` — and **no terraform anywhere read it**. The module
  was gated on `deploy_cert_manager`, an unrelated internal bnk-phase override,
  so the operator's setting was inert and `bnk up` created the namespace
  regardless. On an adopted cluster that already ran cert-manager the apply died
  on `namespaces "cert-manager" already exists` with no way to proceed.

  Verified by evaluated plan rather than `validate`, in both directions: 35
  resources with `install_cert_manager=true` including all three cert-manager
  resources, 32 with `false` and none of them.

  Gating it also means the inner module's destroy provisioner —
  `kubectl delete namespace cert-manager` — is `count=0` when adopting, so
  `bnk down` can no longer delete a cert-manager we did not install, or the
  certificates it had issued.

  This is the second setting found inert end to end, after
  `cneinstance_advanced_env`. `TestEveryRootVariableIsRead` now fails the build
  for any root variable no terraform reads, which catches the class on the commit
  that introduces it. It found exactly one deliberate exception —
  `cluster_network_mode`, whose HCL copy exists only for its validation block —
  which is recorded with its reason.

- **Adopting a cluster that already has cert-manager is now expressible from CI**
  (#186). `resources.cert_manager`, `resources.registry_cos` and
  `resources.cluster_jumphosts` had no environment override, so a container-driven
  install — which takes its entire configuration from `-e` variables, having no
  shell in which to write a `config.yaml` — could say "adopt this cluster" but not
  "adopt what is already *on* it". The install stopped at
  `namespaces "cert-manager" already exists`, a message that names the collision
  but not the setting that clears it.

  This is the customer-shaped path: an existing transit gateway, VPC and ROKS
  cluster, with OpenShift's cert-manager add-on already present. Adds
  `ROKSBNKCTL_CERT_MANAGER_CREATE`, `ROKSBNKCTL_REGISTRY_COS_CREATE` and
  `ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE` across all five layers.

  Two defects were found while testing the change rather than after it. The new
  overrides dereferenced `ws.Resources` with no nil guard, which is a **SIGSEGV**
  on exactly the env-only path they exist for, where nothing has populated it.
  And `TestRunInitFromEnv_AdvancedFields` read the ambient environment: an
  operator with any `ROKSBNKCTL_*` exported in their own shell — which is the
  normal state while driving an install — got a failure unrelated to their
  change. Both are covered by tests that reproduce them.

- **BNK 2.4 now conforms to F5's reference CNEInstance.** Comparing our rendered
  spec against F5's own live 2.4 capture found four keys we never emitted at all:
  `tmmReplicas`, `watchNamespaces`, `placement` and `externalBigip`.

  `placement` is the important one. 2.4 removed the node-labeler and pod
  placement is the mechanism that replaced it — but nothing added the
  replacement, so 2.4 shipped with **neither**. TMM landing one-per-node and
  spread across zones was the scheduler's discretion rather than a requirement;
  verification passed because the scheduler happened to spread them.

  Also now emitted on 2.4: the three TMM settings F5 sets and we did not
  (`TMM_IGNORE_GATEWAYS`, `DISABLE_HT` to keep hyperthread siblings off TMM's
  pinned cores, `ENABLE_K8S_ROUTES`), the TMM rolling-update policy
  (`maxSurge 0 / maxUnavailable 1` — the same shape as the cwc Multi-Attach
  deadlock), and `GATEWAY_API_VERSION`, which was set **nowhere**.

  A caveat on that last one, verified on a live 2.4 cluster rather than assumed:
  the value now propagates correctly through both layers we can reach — the
  CNEInstance carries `1.5.0` (byte-identical to F5's reference capture) and FLO
  copies it onto the CNEController CR — but the controller **Deployment** F5's
  operator then generates from that CR runs `v1.3.0` regardless. The last hop is
  F5's to make. We emit what the reference emits; the running controller does not
  yet reflect it, and no setting on our side changes that. Tracked in #185.

  Every setting is a `config.yaml` field, a `ROKSBNKCTL_*` override, and a row in
  the generated configuration reference with its line and default. The placement
  topology keys are IBM ROKS node labels and are **settings, not constants** —
  hard-coding them is the assumption the node-labeler baked in.

  Defaults are F5's reference values, not invented ones, and a committed copy of
  their reference spec is now a test fixture: drift from it fails.

- **`cluster.worker_flavor`** now reaches terraform. The inner cluster module has
  always honoured an explicit `worker_flavor`, but nothing surfaced it — no root
  variable, no config field, no override — so it was unreachable, the same
  declared-but-unwired shape as `cneinstance_advanced_env`.

  It matters because the auto-select filters on `^bx2-[0-9]+x[0-9]+$`: **only the
  bx2 family**. F5's approved reference cluster runs `cx3d.8x20`, which no
  combination of vCPU and memory minimums can ever produce.

- **`deploymentSize` defaults to `Tiny` on 2.4**, matching F5's reference and this
  tool's own variable description — which said *"Tiny is what the BNK 2.4 install
  guide uses"* while the code defaulted to `Small` on both lines.

  Not cosmetic: `Small` makes TMM request 4Gi of `hugepages-2Mi`, and a stock
  ROKS worker reports `hugepages-2Mi=0` — including F5's reference cluster, whose
  TMM pods request none and run fine on `Tiny`. Demo mode had been hiding it by
  dropping the hugepage request; turning demo mode off to conform exposed three
  TMM pods Pending on *"Insufficient hugepages-2Mi"*. Two settings that each
  looked right alone were wrong together. 2.3 keeps `Small`, and an explicit size
  is honoured on either line.

- **`bnk.whole_cluster`.** Conforms to the reference on 2.4 (`false`, paired with
  `watchNamespaces: ["All"]`) and stays `true` on 2.3. The two move together
  because the product validates them together: `wholeCluster: true` alongside
  `watchNamespaces: ["All"]` is rejected outright as an invalid product
  configuration, since it says "watch everything" twice in contradictory ways.

- **`bnk.demo_mode`.** Demo mode was being enabled on every install, on both
  lines. It now defaults to **false on 2.4**, matching the reference. It stays
  true on 2.3 — that is what has shipped and been exercised there, and changing a
  working line without a run to back it would be a guess.

  2.3 rendering is asserted byte-identical to the previous release, verified by
  rendering the pre-change module from git and diffing.

### Fixed

- **`bnk up` no longer panics on a mirror-only disconnected install.**
  `registry_trust.go` read `ws.BNK.FLP.External` behind a `ws != nil` guard, but
  `BNK.FLP` is itself a pointer — so a workspace that mirrors images to a private
  registry while licensing still goes direct to F5 (the ordinary staged
  bring-up) died with a SIGSEGV before applying anything. Every other site in the
  tree checks `FLP == nil` or allocates first; this one read through it.

- **A BNK namespace the 2.4 teardown strands is now freed.** On 2.4 the
  CNEInstance deletion times out, its operator is removed anyway, and nothing is
  left that could clear the finalizer — so the namespace sits in `Terminating`
  forever and every later `bnk up` fails with *"unable to create new content in
  namespace f5-bnk because it is being terminated"*. The cluster is permanently
  unusable for a reinstall. `bnk down` now clears F5's finalizers after a
  successful destroy, scoped to this workspace's own cluster, to namespaces
  already terminating, to F5's own kinds — and it says what it did, because a
  silent repair teaches nobody the bug is there. Seen twice on live 2.4
  clusters.

- **The lifecycle demos no longer report success over a failed phase.** They are
  `set -uo pipefail` with no `-e`; `run` returned each command's status and no
  call site looked at it. A `bnk up` that failed on every object was followed by
  `✓ BNK removed and reinstalled` and `DEMO COMPLETE`, exit 0. A new `must`
  helper dies on failure and is used for the commands that change the world;
  informational commands keep plain `run`, where a non-zero is often just "not
  there yet".

- **The demos no longer execute their own comments.** Their `config.yaml`
  heredocs are unquoted so `${REGION}` expands — which also made backticked
  command names in the comments run. `` `init` `` is a real binary on some
  hosts and it ran; the words vanished from the generated file, replaced by the
  commands' empty output.

- **Five stale runner-image pins**, up to twenty releases behind, including one
  in a `.env.example` that overrides the script default the guard did check. The
  guard now discovers every demo that pins a runner instead of naming one.

## v1.52.0 — 2026-08-21

Groundwork for driving **BNK 2.4** from the same build that drives 2.3, plus the
one-namespace defects that work uncovered.

### Added

- **`bnk_line`, a derived terraform variable** carrying the BNK release line
  (`2.3` / `2.4`) so modules can gate per-release resources with `count` rather
  than a forked copy of the tree. It comes from `bnk.manifest_version` and
  nothing else — there is deliberately **no** `bnk.line` config field and no
  `ROKSBNKCTL_BNK_LINE` override, because a second way to say which release this
  is, is a second thing that can disagree with the manifest being installed.
  Rendered on both the prefix-driven and legacy render paths: covering only the
  first would let a legacy workspace on a 2.4 manifest silently plan 2.3.

- **RFC 1123 validation on `flo_namespace` / `flo_utils_namespace`**, which the
  configuration reference has claimed for some time and nothing enforced. An
  invalid name previously surfaced as a Kubernetes admission error partway
  through an apply.

### Fixed

- **The 2.4 gateway phase works.** Running `gateway up` against a live 2.4
  cluster for the first time turned up three defects stacked behind each other.
  The plan failed outright: the cluster data source was gated to 2.3 while the
  security-group lookup that indexes it was not, so `cluster[0]` indexed an
  empty tuple. VXLAN is the dataplane on both lines — 2.4 configures it through
  `GatewaySettings`' `egress-vxlan-*` pools instead of `F5SPKEgress`, but the UDP
  ingress still has to be permitted — so the gate was the error, not the
  consumers. With that fixed the CRs applied and nothing reconciled them: the
  controller reads `Infra` and `GatewaySettings` only under
  `USE_GATEWAY_SETTINGS=true`, and nothing set it, so they sat at
  `Accepted=Unknown` / "Waiting for controller" indefinitely. That flag is now a
  2.4 default rather than an opt-in, because on 2.4 there is no working
  configuration without it. `Infra` and `GatewaySettings` now reach `Accepted`,
  `ResolvedRefs` and `Programmed`.

- **`cneinstance_advanced_env` reaches the CNEInstance.** The per-component
  `advanced.<component>.env` override was declared at the root, rendered as a
  tfvar, documented and tested on the Go side — and read by no terraform
  anywhere, so setting it did nothing. It is now wired through both module
  layers. A user entry *replaces* a default of the same name rather than
  appending a duplicate, since the spec is read by the lifecycle operator rather
  than kubelet; and a component named only in the override arrives on its own, so
  components F5 adds between releases need no code change here.
  ([#175](https://github.com/jgruberf5/roksbnkctl/issues/175))

- **The lifecycle demos no longer require a BNK Forge account to run at all.**
  Both `cluster-lifecycle-cli-demo.sh` and its CI twin died in their *preflight*
  without `FORGE_URL` / `FORGE_USER` / `FORGE_PASS`, while Forge is used by one
  phase — `bnkforge register` — and nothing else: no Forge variable reaches
  terraform, and two sibling demos already run `bnk up` without it. The gate was
  holding the entire terraform half of the demo behind a credential it never
  uses. All three set runs the phase, none skips it and runs the rest, and a
  partial set still fails loudly — a typo in one variable must not quietly cost
  the registration step. The `BNK_FORGE_*` names `roksbnkctl` itself reads are
  accepted too. ([#164](https://github.com/jgruberf5/roksbnkctl/issues/164))

- **`bnk up` refuses a namespace change that would delete the utils namespace.**
  Collapsing `flo_utils_namespace` into `flo_namespace` on a workspace that has
  already installed takes `kubernetes_namespace_v1.f5_utils` from count 1 to 0,
  so terraform **deletes** it — along with CWC, RabbitMQ, Coremond,
  CRDConversion, Observer, Fluentd, OTEL, IPAM and the `bnk-license` CR, none of
  which terraform manages and none of which come back. In a plan it reads as one
  namespace being removed. One namespace ([#66](https://github.com/jgruberf5/roksbnkctl/issues/66))
  is a **create-time** choice: safe on a new install, destructive on an existing
  one, and nothing said so. Renames are refused for the same reason.

- **One-namespace installs now work outside the FLO module.** #66 collapsed the
  namespaces and verified it; four things downstream still assumed two. The
  gateway phase's application namespace was created unconditionally, so pointing
  it at the BNK namespace failed with `already exists` (its own state dir has no
  record of the BNK namespace). `roksbnkctl logs` and `bnk status` probed a
  hardcoded `f5-bnk` and reported a healthy deployment as `0 pods` — for **any**
  workspace that set `bnk.flo_namespace`, collapsed or not. The `bnk down`
  teardown guard named a namespace the deployment may not use. And the SCC
  policy breakdown reported all 19 bindings under *both* namespaces when they
  were equal, disagreeing with its own total.

- **The CWC Multi-Attach guards in the demo tooling** watched `f5-utils`
  unconditionally. On a one-namespace install that namespace does not exist, so
  the guard silently never patched and the deadlock it exists to break simply
  hung `bnk up`. They follow `ROKSBNKCTL_FLO_UTILS_NAMESPACE` now.

- **A warning fired on the read path that belonged to the write path.**
  `readTFVarsAssignments` is shared between rendering an applied-tfvars snapshot
  and reading one back, and printed `skipping in applied snapshot` whenever a
  file was absent — so reading a missing snapshot, the normal state before a
  first install, warned about a snapshot nobody was writing.

## v1.51.0 — 2026-08-21

A second adversarial sweep, six issues filed and fixed. The reason this is a
release rather than a patch is `terraform/.terraform.lock.hcl`: it was committed
and carefully maintained, and it **never reached users**. `//go:embed terraform`
skips dotfiles, so every released binary extracted 86 files without it and
resolved providers from the `>=` constraints in `versions.tf` at deploy time —
five of six unbounded, nothing verifying what the registry served, and a
different provider set from the one CI tested. Two operators running the same
release a week apart could get different providers with nothing in the repo
having changed.

The lockfile now ships, seeded into a workspace's terraform directory when
absent and **never overwriting one that already exists** — a workspace that has
run `terraform init` keeps the versions it recorded, because clobbering it would
downgrade providers on the first run after an upgrade and `init` runs with
`-upgrade=false`, so it could not self-heal. It carries checksums for all five
release platforms rather than the one it had. Provider constraints are bounded
with `~>`, which preserves every floor except `kubernetes`, whose floor rises
from 2.25 to the 3.x already pinned.

**One new exit code.** `upgrade` / `self update` exit **125** when an upgrade
removed the old binary and could not put anything back — there is no
`roksbnkctl` at the install path and the `.old` sidecar beside it is the only
copy. Distinct from `1` because the two need opposite responses: an ordinary
failed upgrade is safe to retry, and this one cannot be retried at all. It
overlaps the range a wrapped tool's status passes through on, which is bounded
by the fact that these two commands spawn no child process. Documented in
[chapter 7a](book/src/07a-unattended-setup.md).

**One behaviour change worth knowing before you upgrade.** `bnk up` now refuses
an air-gap mirror record whose replicate did not finish. A partial `registry
replicate` still writes its record — that is what lets a re-run resume — but the
record could not previously say it was incomplete, so the install pointed every
image and chart at a mirror missing some of them and failed minutes later on a
node as `ImagePullBackOff`, nowhere near its cause. The refusal names `registry
diff`, `registry replicate` and `registry adopt`. It is on the **up path only**:
teardown never reads the mirror and is never gated on it.

**Security.** The credential redactor knew only the raw form of each secret. In
this system credentials move base64-encoded as a matter of routine — every
`*_b64` config field, every Kubernetes Secret — so the layer that exists to stop
a wrapped tool leaking a credential covered half the shapes that credential
takes. It now registers the encoded forms too, in both alphabets and including
the alignment-shifted forms a secret takes inside a larger blob. Redaction is
also **faster** than before despite matching ~6x more patterns, because secrets
are now indexed by first byte.

### Added
- `exitcode.SelfUpdateStranded` (**125**) — an upgrade that left no binary at
  the install path. Documented in chapters 7a and 17. (#154)
- `RegistryMirror.MissingCount` — a mirror record can now say its replicate did
  not finish. `omitempty`, so records written before this field read as
  complete. (#150)
- A demo preflight that prints the `roksbnkctl` binary and version a run will
  actually use, and warns when it is not the newest release. During the v1.50.0
  validation the CLI demos ran against **v1.43.0** — eighteen releases old — for
  two full passes. (#143)

### Fixed
- The provider lockfile is embedded and seeded rather than absent. (#147)
- The credential redactor covers base64 forms, standard and URL alphabets,
  standalone and embedded. (#145)
- `installByMoveAside` reports a failed rollback instead of discarding it. The
  message names the sidecar and the platform-correct command to rename it back
  — `Move-Item` on Windows, which is the only platform this path runs on. (#146)
- A failed owner-ref on the k8s backend's per-Job Secret warns instead of
  silently orphaning credential material. The patch no longer sets
  `blockOwnerDeletion`, which OpenShift's `OwnerReferencesPermissionEnforcement`
  rejects without `jobs/finalizers` access — that was the cause, not the
  symptom. (#149)
- `bnk up` refuses an incomplete mirror record. (#150)

### Changed
- Provider constraints bounded with `~>`; `tls`, `time`, `local` and `external`
  are now declared at the root, where a bound governs the whole tree. They were
  declared only in submodules with bare `>=`, so nothing bounded them. (#147)
- Dependencies: `golang.org/x/crypto` 0.55.0, `terraform-exec` 0.25.3,
  `platform-services-go-sdk` 0.103.0. (#138)

### Testing
- The argv subprocess test built a 112MB binary into a fresh tempdir on every
  run and never removed it; 94 runs filled a 16GB `/tmp` and surfaced as a
  linker error in an unrelated package. Now built to a fixed, self-truncating
  path. (#157)
- The sshd integration image is pinned by digest and its start retried, after a
  registry rate limit failed five tests on an unrelated PR. (#161)

## v1.50.0 — 2026-08-20

The output of an adversarial sweep of the codebase: twelve issues, filed first
and fixed one PR at a time. Three are real security findings, and one of them —
`config.yaml` written world-readable while holding an IBM Cloud API key — is
worth acting on rather than just reading. **If a workspace on a shared host has
held `ibmcloud.api_key_b64`, rotate that key**: this release tightens the mode
and repairs existing workspaces on read, but it cannot un-expose what was
already readable.

Two behaviour changes for anything scripting this tool. Ctrl-C now exits **130**
from any command that surfaces the interrupt rather than only `init`, and a
malformed invocation exits **2** rather than 1 — so a script can tell "the
operator stopped it" and "the command never ran" from "it broke". The testing
client VPC's default security group also narrows from `0.0.0.0/0` to the RFC-1918
ranges.

A recurring theme is worth flagging, because it shaped what did *not* change:
four of the twelve issues rested on figures from the sweep that did not survive
measurement — `DefaultResources` is 12 lines and not 565, the comment ratio is
25% and not 37%, `Sprint N`/`PRD N` citations resolve to in-repo documents, and
"43 near-identical blocks" was 21. Each is called out where it applies. Nothing
was churned to match a bad number.

### Security

- **`bnkforge --insecure` sent the API token over an unauthenticated connection, silently.** The flag is genuinely opt-in and its purpose — self-signed lab installs — is legitimate. What it did not account for is what travels over the connection: the Forge session token goes on **every** request, so disabling verification leaves it encrypted but *unauthenticated*. Anyone positioned on the path can present a certificate for the Forge host, terminate TLS, and read the token. Nothing at use time said so, and `bnkforge.insecure: true` persists in `config.yaml` — typically set once for a lab and then forgotten, including when the same workspace is later pointed at a production Forge.

  Every request made with verification disabled now warns, naming the host, once per client — attached to the requests that actually carry the credential rather than to construction, since a client that is built and never used has disclosed nothing. When the host is an IP literal that is routable on the public internet the warning says that too; a hostname is left unclassified rather than guessed at.

  **`--forge-ca <file>` / `bnkforge.ca_b64` / `ROKSBNKCTL_BNKFORGE_CA_B64` is the replacement.** For a self-signed Forge you generated the CA, so you already hold it, and pinning it *authenticates* the connection instead of abandoning authentication — the same shape the registry path already uses for self-signed mirrors. A pinned CA wins over `insecure`, `bnkforge enable --forge-ca` clears the flag rather than leaving it stale, and the PEM is parsed where it is supplied so a wrong file fails there instead of at the next connection. `bnkforge status` now reports which of the three modes is in force. (#113)

- **Security-group sources were hard-coded to `0.0.0.0/0` with no way to narrow them.** Five rules across the testing and cluster modules took their source from a literal. The starkest was `tgw_vpc_default_sg_inbound_all`, which carried **no protocol and no port** — every port, every protocol, from anywhere — on a VPC default security group. IBM ships those denying inbound, so the rule inverted a safe default for any resource later placed in that VPC. Nothing is currently attached to it, which makes it a fail-open default rather than an open door, but it is one floating IP away from being one. The cluster VPC's default SG carried the same all-protocol rule, and the jumphosts' `:22` was open with no allowlist.

  All five are now driven by list variables, surfaced through the five layers (module + root tfvars, `config.yaml`, `ROKSBNKCTL_*` + doc table, `.env.example`, book) — the pattern the `flp_vsi` module already had and these did not.

  **Defaults differ per plane, deliberately.** Jumphost SSH stays open (you connect from wherever you are, and access is key-only); the testing client VPC's default SG becomes RFC-1918 (in-fabric test traffic arrives over the Transit Gateway and needs no public source); cluster `:80` stays open (the ingress/ALB path is meant to be publicly reachable). The **cluster VPC's** default SG also stays open, which is the one worth narrowing — it is left at the historical default only because that security group governs the VPC the cluster's own data path runs in, and changing it on every existing deployment is not something to do without a live-cluster validation. `resources.cluster_vpc_default_sg_inbound_cidrs` is there when you have one. (#122)

- **`config.yaml` was written world-readable while holding credentials.** `SaveWorkspace` wrote mode `0644` into a `0755` directory. That file can carry `ibmcloud.api_key_b64`, `registry.generic_password_b64`, and the BIG-IP/GTM passwords — all base64, which the field documentation is explicit is obfuscation and not encryption, so the file mode was the only protection they had. Any other account on the host could read an IBM Cloud API key that can create and destroy infrastructure. The requirement was already written down one line above the affected field (*"chmod 600, never commit"*) and the writer did not implement it.

  `config.yaml`, `registry-mirror.json` and `cluster-outputs.json` are now written `0600`, and the workspace directory `0700`. Writing the right mode is not enough on its own — every workspace created by an earlier release is on disk at `0644` right now, and nothing rewrites a finished workspace — so **reading** a workspace tightens it in place and says so once, pointing at rotation.

  The repair covers the workspace directory and each of its immediate children, enumerated rather than matched against a list of known names, so every `state-*` tree (and any a future phase adds) is covered without a deep walk of `tf-source/` and `scratch/` on each load. It masks the group and other bits rather than forcing a flat `0600`/`0700`, so an executable that lands in the tree keeps working. Repair is best-effort: a filesystem that cannot hold `0600` must not make `init` fail outright.

  `roksbnkctl doctor` gains a read-only `workspace permissions` check — read-only because loading has already repaired what it could, so anything still loose is something the repair could **not** fix, which is the part worth reporting. The row is absent on Windows, where Go's `Chmod` only toggles the read-only bit.

  If a workspace on a shared host has held `api_key_b64`, **rotate that API key**: tightening the mode now does not un-expose what was readable before. (#121)

- **The BNK-phase paths trusted `registry-mirror.json` without checking it described the configured mirror.** #109 fixed this for the registry subcommands; the paths that act on the same record without asking kept believing it — the tfvars render (which redirects every chart and image reference in the install), the phase guard, and the node CA-trust installer (which pushes the recorded CA onto every worker). Repointing `registry.generic_repo_prefix` at another repository, or switching `registry.target`, leaves a record naming the old mirror, and nothing re-probes on read.

  All three now refuse a record that does not describe the configured mirror, naming both repositories and how to re-record. The identity check moved into `internal/config` as a single implementation shared with the registry subcommands, resolved from configuration alone — no client, no credentials — so it runs on the paths that have neither. That also removed a network call and an API-key resolution from the subcommand path, which previously answered "cannot tell" whenever the credential was unavailable.

  Two cases are deliberately **not** treated as mismatches. When the configured mirror cannot be resolved from config at all (an unset `generic_host`, an ICR region with no known registry host), the record is still trusted — knowing less than the record is not grounds for discarding it. And when no target is stated anywhere, the `icr` default is a fallback rather than a claim: `registry replicate --target generic` is a supported way to mirror without putting `target: generic` in `config.yaml`, so the record's host and repository are checked through its own kind instead. The tfvars check runs before the file is created, so a refusal leaves the previous render intact rather than truncated. (#112)

### Changed

- **The `ROKSBNKCTL_*` env surface is a table, and enumerable.** `OverrideFromEnv` spelled each variable's name three times — to read it, to assign it, and to report it in the applied-overrides list — so the three could drift independently. The 21 genuinely uniform overrides now come from one table where the name is written once and the report string is derived, and `OverrideFromEnv` drops from 445 lines to 371 — 21 four-line blocks replaced by a seven-line loop.

  The larger win is that the surface can now be **enumerated**. `SupportedOverrideNames()` is the authoritative list — the uniform tables (here and in `envoverride_flp.go`), the bespoke blocks, and the computed `ROKSBNKCTL_ZONE<n>_*` family, all summed — and the guards consume it instead of regex-scraping the source over two hard-coded filenames, a drift guard that was itself drifting. Switching over immediately surfaced `IBMCLOUD_API_KEY`, which the old `ROKSBNKCTL_*`-bound regex had excluded by construction. The first cut of the list repeated the scrape's mistake one level up — it summed only the main table and the bespoke list, reporting **19 fewer names than the code honoured** (the FLP-VSI and COS tables, the security-group CIDR lists, the per-VLAN prefix lengths), so trimming `ROKSBNKCTL_COS_BUCKET` from the demo `.env.example` passed the parity guard while every Argo blueprint silently lost the variable. The inline tables are now package-level and enumerated, the zone family derives from the same declaration the reader ranges over, and the surface guard compares the list **bidirectionally against every `ROKSBNKCTL_*` literal in the override code**, so an override added in any shape — bespoke block, table row, computed family — cannot drop out of the surface silently.

  Overrides that decode base64, parse an int or a bool, or validate keep their own blocks — forcing those through a table would trade one kind of repetition for a worse kind of indirection. They are declared in `bespokeOverrideNames`.

  Two guards are new rather than migrated: the env-to-field tables in the package doc — the biggest in-repo documentation of this surface — are now checked **bidirectionally** against the list (every supported name documented, every documented name supported), and the demo parity guard covers the per-zone family instead of skipping it. The isolation helpers in the package's own tests range the list too, instead of keeping 16-name hand copies that failed spuriously under an ambient `ROKSBNKCTL_GTM_URL`.

  Also fixed while making the surface honest: `ROKSBNKCTL_FLP_VSI_NAME_PREFIX` only took effect when `ROKSBNKCTL_FLP_VSI_CREATE_VPC` was also set — the block was misnested inside the create-VPC check, so a Forge blueprint **adopting** an existing VPC silently got the legacy unprefixed VSI names (#88 regressed for the adopt path). The prefix is now an ordinary table row, applied on both paths, with a regression test on the adopt shape.

  Verified behaviour-preserving by running the pre- and post-refactor code over the full surface (98 variables including all three zones) and diffing the resulting workspace and applied list: identical, except the single intended change — `name_prefix` now applies without `CREATE_VPC`. (#114)

- **Exit codes are one contract instead of eighteen decisions.** `os.Exit` was called from 18 places across five packages, each with its own policy. `roksbnkctl init` exited `130` on Ctrl-C while every other command turned the same interrupt into `1` — indistinguishable from a real failure, which matters because every demo and CI path in this repo branches on `$?`. `internal/remote` defined a meaningful `126`/`127` scheme that nothing else participated in. And because `os.Exit` terminates the test binary, none of it could be asserted: the inconsistencies went unnoticed because there was no way to notice.

  Commands now **return** a coded error (`internal/exitcode`) and one place maps it to a status. Three `os.Exit` calls remain, each with a stated reason: the root's mapping, the argv preflight (which runs before cobra exists to return an error to), and `init`'s SIGINT handler (which fires from a goroutine while a terminal read blocks, where there is no error to return from anywhere). All three take their value from the contract.

  Two behaviours change for callers: **Ctrl-C now yields `130` from any command that surfaces the interrupt**, not just `init`; and a malformed invocation yields `2` rather than `1`, so a script can tell "the command never ran" from "the command ran and failed". The interrupt outranks every other classification: a failure that happens *because* of the Ctrl-C — a connect aborted mid-handshake, a test suite cut off mid-run, a child process killed by the signal (`128+signum`, so SIGINT propagates `130` rather than the `255` a raw `-1` produced) — reports `130`, not the code it landed in. `126` means what it says: a connect the target answered and *refused* (publickey rejected, host-key mismatch); an unreachable target is `127`, and a mid-command transport failure is a plain `1` rather than a spurious "credential refused". An interactive `--on <target> shell` now runs the local terminal raw, so `^C` reaches the remote foreground command instead of tearing the session down, and the remote shell's own exit status propagates instead of collapsing to `0`. Codes documented in [chapter 7a](book/src/07a-unattended-setup.md).

  Returning rather than exiting also fixes a quieter bug: `os.Exit` skips deferred cleanup, so the SSH error paths in `internal/cli/remote.go` were leaving `client.Close()` unrun. (#118)

- **`renderBNKFields` and `registry.go` split along the seams they already had.** `renderBNKFields` was one 298-line function emitting eleven unrelated groups of terraform variables; it is now a twenty-line dispatcher plus one renderer per group, so a change to the GTM fields touches `renderBNKGTM` and nothing else. `internal/cli/registry.go` was 1,311 lines holding nine subcommands that share only helpers; each subcommand now has its own file and the shared helpers stay in `registry.go` (690 lines).

  Pure moves — no behaviour change, and a **golden test** proves it: the whole rendered tfvars body is pinned byte-for-byte against a workspace that populates every section. That assertion exists because the per-line tests could not have caught this class of mistake. Verified by breaking it three ways — dropping a section from the dispatcher, reordering two, and emitting one twice — each of which the existing tests pass and the golden fails. A companion test checks the fixture still exercises every section, so the golden cannot quietly pin an empty render.

  Two claims in the issue did **not** survive measurement and are not acted on: `DefaultResources` is 12 lines, not 565 — the sweep that filed it measured to the next `func`, sweeping in the type declarations that follow — and `internal/config/workspace.go` is 32 type declarations with doc comments against 10 functions, which is what a config schema file should look like. Splitting either would have been churn justified by a bad number. (#117)

### Fixed

- **`cluster up` reported a failed `cluster-outputs.json` write on one exit path and discarded it on the other.** The changed path warned and named the recovery command; the no-change path fifteen lines away did `_ = persistClusterOutputs(...)`. The silent one is the likelier to be hit — re-running `cluster up` against an already-converged cluster is routine, and it is exactly the run where nothing else on screen would hint that the record had not been refreshed. What it costs is not hypothetical: `cluster-outputs.json` is where `cluster_id` lives, and without it the admission-policy sweep falls back to resolving the cluster by **name**, which `admission_sweep.go` documents as how a sweep once misdirected every delete at the wrong cluster and landed zero. Both paths now go through one reporting helper, so they cannot diverge again. (#119)

- **Ctrl-C did not cancel credential and `terraform output` calls.** `root.go` builds the process context with `signal.NotifyContext`, so every command receives something that cancels on interrupt. Eight call sites discarded it and started a fresh `context.Background()`, including credential resolution — which can block on the OS keychain — and a `terraform output` shell-out. While one was blocked, Ctrl-C was accepted by the signal handler and then ignored, because the work was running on a context that could not hear it. `ibmcloud login` on the passthrough path was worse still: `exec.Command` with no context at all, so a hang on a wedged IAM endpoint was uninterruptible and unbounded.

  The context is now threaded through `openIBMClient`, `WorkspaceEnv`/`WorkspaceEnvCore`, the SSH backend's target resolver, `init`'s interview and per-call bounds, `ensureIBMCloudLoggedIn`, and doctor's binary probes. `init`'s interview context keeps its deliberate absence of a wall-clock deadline — it derives via `WithCancel`, which adds no deadline while restoring cancellation, so slow human answers still never expire a call.

  A guard test walks `internal/` and fails on any new `context.Background()` outside an allowlist, since the defect is invisible in review — a detached context looks identical whether or not the caller had one to thread. The allowlist holds only the root context and cleanup that runs *because* the parent is already cancelled, and a second test drops entries that stop applying. (#116)

- **The perf matrix's teardown still named a CRD BNK never installs.** #99 corrected the fixture that emitted `gateway.networking.k8s.io/v1alpha2 TCPRoute` and left `tcproutes.gateway.networking.k8s.io` in the matrix teardown's delete list, one package away, for two more releases. The emitter and the delete list were written apart with nothing tying them together.

  The route contract now lives in one place (`internal/test/gatewayapi.go`): which kinds BNK's pinned Gateway API 1.4.1 **standard** channel provides, which upstream kinds it does **not**, and the CRD names for each. Fixtures render from it, teardown deletes from it, and the tests assert against it — so a change to the pinned channel moves all of them together instead of leaving one behind.

  Teardown also issues **one delete per type group** rather than one comma-joined delete. cli-runtime's builder resolves every requested type before removing anything and returns on the first unknown one, so a single call naming a CRD the cluster lacks deleted nothing at all — leaking the deployments, services and secrets alongside it, on exactly the cluster where cleanup matters most: one where the BNK install failed or was removed. The pre-existing `tcproutes` entry meant this aborted on *every* BNK cluster.

  Three comments in `internal/test/matrix.go` still described the fixtures as rendering `TCPRoute`; a repo-wide guard test found them. That guard fails on any reference to a route kind outside the standard channel, across Go, YAML, Terraform and shell, with an exemption list where each entry states why the mention is prose rather than a manifest — and a companion test that drops entries which stop applying. It is a repo scan rather than a package test on purpose: #99's own fix missed a file one package away, and a package-scoped test would not have looked there. (#115)

- **`remoteHealCommand` carried a dead positional counter.** The `sh -c` self-heal command numbers its parameters so the IBM Cloud API key travels as a positional value rather than interpolated into the script text. The counter tracking those numbers ran alongside the args slice it described, and its final increment was never read — staticcheck v0.8.0 flags it (SA4006). The index now derives from `len(args)`, so there is no second thing to keep in step, and a new test pins the property the counter existed to maintain: every `"$N"` in the script resolves to the value actually at that argv position, and the key never appears in the literal.

- **`registry diff` reported "in sync" against an empty registry.** `diff` compares the bill of materials against `registry-mirror.json` and **never contacts the registry**, so a record describing a mirror that has since been rebuilt, emptied or swapped still listed its artifacts as present. Observed with a record naming one repository while the workspace was configured for another, on a host that had been destroyed and rebuilt: `diff` said *"mirror is in sync"* while `verify` correctly reported all 89 artifacts missing. Skipping replication on that basis leaves an air-gapped install to fail much later on images that were never copied.

  A record is now trusted only when it describes the **configured** target — same target kind, repository and host. Otherwise it is discarded with the reason printed, and everything reads as missing: the safe direction, since it prompts a replicate and replicate skips artifacts already present at the right digest. When the target cannot be resolved at all (missing credentials, an unreachable region) the record is still trusted, because knowing less than the record is not grounds for discarding it.

  **`registry delete` and `registry prune` had the same flaw, destructively.** Both take their artifact list from the record and act on the *configured* target, so a record describing another mirror deletes one registry's contents out of a different one — and `delete`'s confirmation prompt names the **record's** host, so it would state the wrong destination while doing it. Both now **refuse** rather than discard: a read-only `diff` can afford to shrug and report everything missing, an unrecoverable delete cannot.

  `diff` also now states what its answer is based on. It reads the record, so "in sync" means *nothing left to replicate according to what was last replicated* — not *every artifact is present*. Only `verify` establishes the latter, and the output now says so. (#109)

### Documentation

- **Comments citing documents that are not in this repo.** Two families, and both were dead ends where the reasoning should be. `prompts/sprintNN/README.md` was removed at v1.12.0 — four citations survived it, one of them the package comment on the binary's entrypoint, the first thing a reader opens. `issues/issue_sprintNN_<role>.md` is worse: those per-sprint review trackers **never shipped at all**, and were cited from 33 places across source and tests.

  Every citation is gone and the reasoning each pointed at is stated inline. `TestNoCommentCitesAnAbsentDocument` fails on both families, and found a fifth `prompts/` citation on its first run that the manual sweep had missed by looking only at non-test source. Also removed: `pre-Sprint-N behaviour` (15 sites, asking a reader to compare against a state no document records), `<role> Issue N carry-over` (7), and bare `Sprint N:` prefixes on sentences that stand without them (12).

  **Two of the issue's premises did not survive measurement, and are deliberately not acted on.** The comment ratio is **25%**, not 37% — healthy for Go, so there was no volume problem to solve. And `Sprint N` / `PRD N` on their own **resolve**: `docs/PLAN.md` carries 37 per-sprint sections and `docs/prd/` holds 19 specs, both cited from this file's header. Stripping 250 working links on the strength of a bad number would have been the damage, not the fix. CONTRIBUTING records the distinction: the test is not "is this about the past?" but "can the reader act on it?" — a comment explaining why a past bug must not be reintroduced is the most valuable kind here, not the least. (#111, #120)

## v1.49.1 — 2026-08-20

A single fix, and a correction to what v1.49.0 actually contained.

### Fixed

- **v1.49.0 shipped WITHOUT the #100 adopt-guard fix, which had been silently reverted.** The fix and its regression test were both undone by the Artifactory-demo commit and released missing, so `bnk up` again refused a workspace that owned its install — observed on a workspace holding **94 resources**: `cluster "bnk-ci" already has BNK installed … but workspace "bnkconn" has no terraform state for it`. Anyone on v1.49.0 adopting or converging a cluster from the workspace that built it will hit that false refusal; v1.48.0 and v1.49.1 are unaffected.

  The mechanism is worth recording, because the diff is the least interesting part. The branch was squashed with `git reset --soft main` while local `main` was behind origin, so the resulting tree was "stale main + the branch's changes" and its diff undid everything that had landed in between. The same commit also reverted the v1.48.0 CHANGELOG section; that half was caught and restored, and the code half was missed by assuming it was the only casualty rather than reading the rest of the diff.

  CI stayed green throughout because the regression test was reverted **alongside** the code it guarded — the worst shape this failure can take, since nothing remains to detect it. Restoring both is verified in the opposite direction: re-applying the reverted implementation makes the test fail on exactly the case it was written for.

- **The blueprint demo's `.env.example` pinned a runner image seven releases old**, and that pin *overrides* the demo script's default (the script reads `${RUNNER_TAG:-…}`), so every run exercised v1.42.0 however new the installed binary was. `TestDemoRunnerTagMatchesTheCurrentRelease` guarded only the script, not the file that beats it — the same drift its own comment records happening once before, arriving through the one file it does not read. Pin bumped, and a second test now covers `.env.example`.

- **The two cluster-creating blueprints collided on every name.** `new-cluster` and `new-cluster-disconnected` use different workspaces but inherited the same `ROKSBNKCTL_PREFIX`, `ROKSBNKCTL_CLUSTER_NAME` and `ROKSBNKCTL_CLUSTER_VPC_CIDR`, so running them in sequence failed with `Provided Name (bnk-ci-cluster-vpc) is not unique` — while the README documents `all` as running every workflow in order and teardown removes both workspaces as though both exist. The disconnected pair now has its own identity (`bnk-cid`, `10.244.0.0/16`). The CIDR half mattered more than the name: both VPCs attach to the shared transit gateway, where overlapping prefixes are blackholed silently rather than refused.

## v1.49.0 — 2026-08-19

Everything here came out of the v1.48.0 demo cycle. Two of the three fixes were **tests asserting the bug rather than catching it**: the perf matrix *required* the exact rendering that could never work, and the arbitrary-uid gate passes on the platform it runs on (kind) while the platform it ships to (OpenShift) fails.

### Added

- **Working examples of every route kind BNK 2.3 supports.** The gateway phase created exactly one route kind, and its listener's `allowedRoutes.kinds` was hard-coded to `HTTPRoute` — so a `GRPCRoute` could be created and would **never attach**: the Gateway refused it and nothing in the apply failed. `gateway.route_examples` now adds working `GRPCRoute` and `L4Route` examples, with the listener's allowed kinds derived from what is enabled. Requesting `L4Route` also adds a **TCP listener**, because an L4Route cannot attach to an HTTP one. The kinds live in a catalogue keyed by kind, carrying each one's API group and which listener it attaches to, so BNK 2.4's expected move to the experimental channel is a row rather than a rewrite. Off by default, and byte-identical when unset. (#98)
- **A standalone demo mirroring FAR into a self-hosted JFrog Artifactory on one IBM Cloud VSI**, with a step-by-step customer guide covering the Artifactory UI prerequisites — including that Artifactory **OSS cannot be a BNK mirror**, Docker repositories being a licensed feature — and an Argo workflow running the same mirror as an unattended container step.
- **Book appendix B — replicating FAR into an existing registry**, covering both IBM Cloud Container Registry and JFrog Artifactory, for readers whose registry already exists.

### Fixed

- **The perf matrix's L4 leg targeted a CRD BNK never installs.** The fixture rendered `gateway.networking.k8s.io/v1alpha2 TCPRoute`, but BNK 2.3 requires Gateway API **1.4.1 standard**, whose channel contains no `TCPRoute` at all — so the object could never be created and the iperf3 L4 leg has **never** run against a VIP. It failed silently, because the fixture apply is best-effort: no L4 result rather than an error. It now renders BNK's own `L4Route` (`gateway.k8s.f5net.com/v1`) with `spec.protocol: TCP`. The existing test *required* the broken shape, pinning exactly what could not work; it now asserts the `L4Route` shape **and** that `TCPRoute` cannot come back. `TLSRouteName` is renamed `HTTPSRouteName` — it has always been rendered by `renderHTTPRoute` and named a kind it has never been. (#99)
- **The `tools-ibmcloud` image could not run under OpenShift's arbitrary uid.** Its `$HOME` was created `0755` owned by uid 1000, so only uid 1000 could write it — but `runAsJob` deliberately leaves `RunAsUser` unset so a cluster's own admission may assign one, and OpenShift's SCC (which is what ROKS uses) assigns an arbitrary high uid from the namespace range, in gid 0. The `ibmcloud` CLI then died on its first config write with `mkdir /home/runner/.bluemix: permission denied`, regardless of subcommand. Now `0775` with gid 0 — the OpenShift image convention, and what `tools-runner` already did; this image had simply not been brought in line. Every image was audited and empirically run under an arbitrary uid: `runner`, `iperf3` and `h2load` pass unchanged, and `flp-status` runs under podman on a VSI rather than under an SCC. (#104)

## v1.48.0 — 2026-08-19

Two bugs found by **running the v1.47.0 demo**, both of which cost a full demo cycle before they were understood. Neither was reachable from CI: one needs a real OpenShift ingress operator racing a real FLO, the other needs a terraform state large enough to contain an IBM resource-group object.

### Fixed

- **`bnk up` now recovers when FLO's crd-installer loses the gateway-api admission-policy race, instead of failing the whole apply.** The sweep goroutine deletes OpenShift's `openshift-ingress-operator-gatewayapi-crd-admission` policy every 5s for the duration of the apply, but it is a **race**, not a deterministic block: the ingress operator can recreate the policy in the window between a sweep tick and FLO's CRD create, and a single denied `backendtlspolicies` create leaves the crd-installer Job failed and `CRDInstallerAvailable=False` **permanently** — FLO does not retry it. The apply then burned the full CNEInstance timeout and failed, and the only fix was by hand: delete the policy, delete the failed Job, `rollout restart` the operator. That sequence now runs automatically, while `tfx wait` is still waiting, so the apply converges rather than needing a re-run.

  Deliberately narrow in three ways, because the repair restarts FLO and a false positive would bounce the operator mid-install: it matches the condition **message** (`admission policy`) and not merely `CRDInstallerAvailable=False` — which is the normal state for much of an install — so an unrelated installer failure (an `ImagePullBackOff`, say) is left alone to report its own error; it fires **once** per apply, since if one restart does not clear it the cause is not the race; and it is best-effort throughout, so a partial repair still improves the odds and the apply's own error remains the source of truth. (#96)
- **`bnk up` refused a workspace that DID own its install.** The adopt guard (#53) decided whether a workspace's terraform state held resources by scanning the file for `"resources": []`. Terraform state is not the only thing in that file with a key called `resources` — IBM Cloud resource-group and IAM objects carry one as an *attribute*, routinely empty — so a state holding **221** resources read as empty, and `bnk up` refused with advice ("use the workspace that installed it") naming the very workspace it was refusing. It now decodes the single top-level `resources` key instead of scanning; the entries stay `json.RawMessage` and are never interpreted, so nothing about their contents can confuse it again. The old rationale — that a parse would have to track state-format changes — had it backwards: a scan reads every attribute of every resource, and so is the version maximally exposed to unrelated content. (#100)


## v1.47.0 — 2026-08-19

Everything here was found by **running the two demos end to end** against v1.46.0, in an account where every prior resource had been cleaned up. Nothing in this release was found by reading code or by CI — each bug needed a real cluster, a real Transit Gateway, or a genuinely clean slate to appear at all.

### Fixed

- **`cleanup` could not delete a Transit Gateway whose connection was still attaching** ([#87](https://github.com/jgruberf5/roksbnkctl/issues/87)). IBM accepts a connection `DELETE` only from a settled state, and `pending` is not one — so a sweep run right after an interrupted `up`, which is exactly when you reach for `cleanup`, failed with `409 invalid_state` and advised a re-run that failed identically until IBM finished attaching. The sweep now does that wait itself.

  The two transient states turned out to mean opposite things, and collapsing them hid a second bug: `deleting` is **departing** — going away whoever started it, so never a reason to refuse a gateway — while `pending` is **arriving**, where ownership decides. A *foreign* connection that happened to be `pending` had its foreign-ness masked by its status. Ownership is now settled before status, and a gateway attached to someone else's network is refused on the first listing instead of after a five-minute wait for a verdict that could not change.

- **`cluster down` and `down` failed on a workspace with nothing left to destroy** ([#89](https://github.com/jgruberf5/roksbnkctl/issues/89)), while `bnk down` and `tgw disconnect` treated the identical state as success. An orchestrated teardown runs every phase unconditionally — BNK Forge as reverse-order module steps, the demos as one workflow per phase — so a non-zero exit for *having nothing to do* failed a teardown that had in fact worked. All four now agree.

  With one distinction the old code did not draw: an **uninitialised** workspace is still an error. `DetectPresence` reports all-false for "no such workspace" exactly as it does for "empty", and they mean opposite things — `-w prdo down` (a typo for `prod`) reporting a completed teardown is how someone concludes a cluster came down while it is still running.

- **The standalone FLP VSI ignored the workspace prefix** ([#88](https://github.com/jgruberf5/roksbnkctl/issues/88)). Every resource was named with a literal (`flp-vsi`, `flp-vsi-subnet`, …), so an account could hold exactly **one** standalone proxy — which rules out the topology the FLP exists for, one per environment. It also meant `cleanup`, which sweeps `<prefix>-*`, could never see the proxy's VSI, floating IP, subnet, security group or boot volume, so a failed `flp down` stranded precisely the resources most worth sweeping.

  New `bnk.flp.vsi.name_prefix` / `ROKSBNKCTL_FLP_VSI_NAME_PREFIX`. **Empty is the default and stays that way** — renaming a terraform resource *replaces* it, so defaulting to the workspace prefix would destroy and rebuild every running proxy on upgrade.

- **The License CR raced the ResourceQuota controller on every fresh install** ([#90](https://github.com/jgruberf5/roksbnkctl/issues/90)). Kubernetes refuses admission while quota status is uncomputed, and the quota controller only discovers a newly-installed CRD on its resync — so the window is open on exactly the run where the CRD is newest. `applyWithRetry` always recovered it, so nothing was broken; this replaces the retry with a wait, because 90 seconds of red `Error:` output on every first install trains readers to skim past errors and makes a genuinely broken install look identical to the benign one.

### Fixed — the demo harness

Three failures that only the clean-slate path could hit, which is the path a recorded demo always takes:

- `bootstrap-services.sh` died outright on IBM's eventual consistency: `vpc-create` returns an id before the VPC is readable, the next line read it back, and `set -e` ended the run with the VPC already built.
- The blueprint demo pinned its runner image to **`v1.42.0`**, four releases stale. Every run exercised an old binary regardless of what was installed, and reported success doing it. A test now fails when that pin drifts from the newest release, so it cannot go stale again quietly.
- The CLI demo did a bare `chmod 600` on the SSH key. `lib/ssh-key.sh` exists because that silently fails on DrvFs (`/mnt/c`, `/mnt/d` — the normal case under WSL) and OpenSSH then refuses the key, surfacing as `Permission denied (publickey)`. The bootstrap used the helper; this demo never did.

### Verified

Both demos completed on v1.46.0 with these fixes: the **Argo blueprint demo 6/6** and the **disconnected-cluster CLI demo 5/5**, each checked against the live cluster rather than by exit status. The disconnected paths pulled **45 of 45 container images from Harbor's private IP** over the Transit Gateway and licensed through the FLP — no image or licensing traffic left the VPC.

### Docs

The teardown chapter's dispatch tables still described `bnk down` refusing an empty workspace, which had not been true for some time, and PRD 06's table specified the refusals this release removes. Both corrected, with PRD 06 recording the supersession and its reasoning rather than being quietly overwritten.

## v1.46.0 — 2026-08-18

Three bugs that failed **silently** — no error, no failed apply, just a thing that quietly did not work — plus the evaluation of BNK 2.4 against every phase of the tool.

### Added

- **`gateway.class_name` / `gateway.controller_name`** (and `ROKSBNKCTL_GATEWAY_CLASS_NAME` / `ROKSBNKCTL_GATEWAY_CONTROLLER_NAME`). `GatewayClass` is cluster-scoped, so two BNK installs sharing a cluster cannot share the name — which a CI matrix has to set per job, not per committed `config.yaml`. Leave `controller_name` empty: it derives the only value the CNE controller answers to.

- **`Tiny` is a valid `bnk.cneinstance_size`** — what the BNK 2.4 install guide uses. Deliberately still unvalidated: which sizes exist is a property of the manifest, not of this tool, so an unknown one is rejected by the operator rather than at plan time.

- **[PRD 18](docs/prd/18-BNK-2-4-SUPPORT.md) — BNK 2.4 support.** The full evaluation of the 2.4 EA install guide against `cluster`, `flp`, `bnk`, `testing` and `gateway`, and the plan for making `bnk.manifest_version: 2.4.*` select it. The headline finding: 2.4 does not extend the data-plane configuration model, it **replaces** it — the `cloud-network-mapping` ConfigMap, both `F5SPKVlan` CRs and the per-zone `F5SPKStaticRoute`s collapse into one `Infra` CR; `F5BnkGateway` + `F5SPKSnatpool` become `GatewaySettings`; `F5SPKEgress` becomes `EgressGateway`. Addressing inverts with it: 2.3 states self-IPs and VIP ranges, 2.4 states pools and the controller allocates. Nothing beyond the corrections below is implemented.

### Fixed

- **`cleanup` could not delete a Transit Gateway** ([#85](https://github.com/jgruberf5/roksbnkctl/issues/85)). It already deleted the connections first; what it did not do was **wait**. IBM detaches asynchronously, so the gateway `DELETE` fired while the connections were still `deleting` and came back `412 Precondition Failed`. A re-run then appeared to fix it — but only because the connections had finished clearing in the meantime, which made an ordinary race look like the async transient the "re-run `cleanup`" advice covers. It is not: a gateway attached to something the sweep never touches fails that way forever.

  Which connections may be detached is now a decision rather than an accident. A connection to a VPC this same sweep is deleting is yours. **Anything else — a VPC under another prefix, a Direct Link, a GRE tunnel — is refused, naming what is attached**, rather than silently disconnecting a shared gateway's other tenants. That topology is the norm, not the exception: the disconnected blueprints adopt one gateway across a mirror, a proxy and several clusters. A refusal is reported differently from a failure, because re-running cannot clear it — though **widening** the sweep can, and the message says so: the commonest reason a VPC looks foreign is that it is yours and simply was not scanned.

- **A non-default `bnk.flo_namespace` produced a `GatewayClass` no controller accepts.** `gateway_controller_name` defaulted to the literal `f5.com/f5-bnk-f5-cne-controller` — the default namespace baked in as a constant — while the CNEInstance it must match is named `<flo_namespace>-f5-cne-controller`. The `GatewayClass` was never `Accepted`, the `Gateway` never programmed, and `terraform apply` reported success. It is now derived, and because the failure is invisible the fix ships with two ways to see it: the resolved `controllerName` is a terraform output, and `gateway status` probes the `GatewayClass` `Accepted` condition next to the name that produced it. That probe works on deployments built before this release, without re-applying anything.

- **The standalone FLP VSI ignored `bnk.far_repo_url`.** It spelled FAR as the literal `repo.f5.com` in both chart pulls and defaulted its image host to `repo.f5.com/images`, with no variable for either. The setting reached `flo`, `cne_instance` and `flp` and silently missed the VSI path, which would keep pulling from production FAR and fail on a chart that was never published there. The image host now derives from the chart host, so the two cannot be aimed at different registries.

- **The support matrix claimed BNK 2.4 does multi-NIC. It does not** — on the evidence of its EA install guide, which is single-NIC throughout: one NetworkAttachmentDefinition, one `external-vlan` network, no second data-plane interface anywhere. The row was written as an *expectation* before 2.4 shipped, and a cell that passes on an expectation is worse than no cell: it let the plan-time check approve a deployment nobody had grounds for. Withdrawn until a release shows otherwise, with the tests inverted so restoring it costs a deliberate act rather than a quiet edit to a data file.

- **Every error printed twice**, once as cobra's `Error:` and once as `roksbnkctl:`. `SilenceErrors` was set on the `tfx` subcommands individually but never on the root, so it held only for the handful that remembered.

- **The OpenShift gateway-API admission sweep now covers `validatingwebhookconfigurations`** as well as the policy and its binding. Which of the three the ingress operator uses to express the same block is a function of the cluster's OCP version — 4.19 uses a webhook configuration where 4.18 uses a policy — so sweeping all three is the only version-independent answer.

### Docs

- **The workspace `gateway:` block was never documented.** Not just the new fields — all nine. Found while documenting the two additions above.
- [Chapter 11](https://jgruberf5.github.io/roksbnkctl/book/11-tearing-down.html) gains *How the Transit Gateway is handled*: the 412, the detach policy, and why a refusal is not a re-run case.
- The support-matrix table, its troubleshooting entry and the glossary all said 2.4 brings multi-NIC. Corrected in step with the matrix itself.

### Note on the exit code in [#85](https://github.com/jgruberf5/roksbnkctl/issues/85)

A partial-failure sweep **does** exit 1 — verified across five error paths. The reported `exit: 0` did not reproduce and is most likely a pipeline in the calling wrapper taking the last command's status (`cmd | head` returns `head`'s). Use `${PIPESTATUS[0]}` or `set -o pipefail`.

## v1.45.0 — 2026-08-17

Closes the reachability gaps in v1.44.0's own features, and promotes the shared-namespace install from *permitted* to *verified*.

### Added

- **`ROKSBNKCTL_FLO_NAMESPACE` / `ROKSBNKCTL_FLO_UTILS_NAMESPACE`.** v1.44.0 made a single shared namespace possible ([#66](https://github.com/jgruberf5/roksbnkctl/issues/66)) but left it unreachable from any environment-driven runner — `init --non-interactive` builds `config.yaml` from the environment alone, and every BNK Forge module configures the tool that way. Set both to the same value for one namespace instead of two.

### Fixed

- **A standalone FLP VSI can now build its own VPC** ([#76](https://github.com/jgruberf5/roksbnkctl/issues/76)). `create_vpc` and the cluster-less gate could not both be satisfied: the gate keyed on `vsi.vpc`, which is mutually exclusive with `create_vpc`. The proxy is the component that needs egress to F5, which makes it the natural *first* deployment in an air-gapped estate — and that was exactly the case it could not serve.

  The fix has a second half worth knowing: `use_existing_cluster_vpc = false` does not mean "do not adopt", it means **create**. The cluster VPC resource is gated on that flag alone — not on `create_cluster`, not on `cluster_absent` — and the module carries no `count`, so it instantiates in the FLP phase's root too. It is now gated on `cluster_absent` as well, or every standalone run would have left a stray `<prefix>-cluster-vpc` behind and broken the next `cluster up` on a duplicate name.

- **The weekly security sweep no longer fails on documentation.** `gitleaks` is a gated check, and the Monday full-history scan produced 46 `generic-api-key` findings — every one a false positive, most of them one base64 placeholder (`encoded-api-key-value`) carried into the built book by mdBook's search indexer. A blocking gate that fails every week on docs is one people learn to mute. Four exact strings are allowlisted; the rule is not disabled and no path is excluded, so the generated book stays scanned for anything else.

- **`TestIntegration_K8sBackend_JobMode_Echo` no longer depends on cluster state** ([#73](https://github.com/jgruberf5/roksbnkctl/issues/73)). It shared two fixed namespaces and never cleaned up, so it passed on a fresh kind cluster and failed on a reused one — and reuse is the documented debug loop.

### Verified

- **One namespace instead of two, end to end.** Both `bnk.flo_namespace` and `bnk.flo_utils_namespace` set to `f5-bnk`: 30 pods in that namespace, no second namespace created, across a fresh install *and* a re-install onto an existing cluster over an existing Transit Gateway and VPC. The uncertainty was never in this repo — FLO is handed `sharedComponentNamespace` equal to its own namespace, and it tolerates it. The `NOT verified against FLO` caveat is discharged.

### Demos

- **Nine settings could not reach an Argo workflow.** `blueprint-workflows-ci-demo` builds its `bnk-env` ConfigMap from the keys declared in `.env.example`, so that file is an allowlist and anything missing from it is ignored however it is exported. `cluster.network_mode`, the Trusted Profile pair, the three VLAN masks and the GTM trio are now reachable.

  `ROKSBNKCTL_GTM_PASSWORD` also had to be routed to the Secret: adding it to the allowlist alone would have written a GTM password into the ConfigMap, which that demo renders into the Argo UI deliberately. Two tests now fail if a supported override is neither allowlisted nor recorded as deliberately excluded with a reason, and if a credential is missing from `SECRET_KEYS`.

### Known gaps

[#79](https://github.com/jgruberf5/roksbnkctl/issues/79) (`bnk down` against a deleted cluster) remains open. The obvious fix — exit 0 and skip — was implemented, found to deadlock teardown and orphan three account-level IAM objects, and reverted. The finding is recorded on the issue.

## v1.44.0 — 2026-08-13

Surfaces settings that existed only in embedded HCL, closes the environment-override gaps that made the v1.43.0 bring-your-own-network work unusable from BNK Forge, and adds the connection half of GSLB. **Additive**: absence of every new setting reproduces the previous behaviour exactly.

### Added

- **The CNE controller's IBM Cloud Trusted Profile is configurable** ([#65](https://github.com/jgruberf5/roksbnkctl/issues/65)) — the identity granting it `Editor` on the cluster VPC had no config surface at all.

  ```yaml
  bnk:
    trusted_profile:
      service_account:            # blank derives what FLO actually creates
      roles: [Viewer, Editor]
  ```

  Blank derives `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount`. The IAM trust rule is a **matcher**, not a pointer — IBM compares a pod's service-account token against `crn`/`namespace`/`name` with `EQUALS` — so a name that does not match the account the controller runs as means nothing can assume the profile, and nothing reports an error. Set it only if you can also make FLO name the account differently.

- **One namespace instead of two** ([#66](https://github.com/jgruberf5/roksbnkctl/issues/66)) — `bnk.flo_namespace` and `bnk.flo_utils_namespace` may now be the same value. Four resource collisions previously made that impossible.

- **The GTM connection for GSLB** ([#51](https://github.com/jgruberf5/roksbnkctl/issues/51)) — `bnk.gtm.{url,username,password_b64}`. `gslb_datacenter_name` named a datacenter with nothing to register it with.

- **Per-VLAN self-IP masks** ([#67](https://github.com/jgruberf5/roksbnkctl/issues/67)) — `bnk.network.vlan_prefixlen_external` / `_internal`. TMM can front a `/23` externally while the internal side is a `/26`; one shared scalar could not express that.

- **Twelve environment overrides**, closing [#64](https://github.com/jgruberf5/roksbnkctl/issues/64) and the same gap in every field added here. `ROKSBNKCTL_EXISTING_SUBNET_IDS`, `ROKSBNKCTL_FLP_VSI_{CREATE_VPC,VPC_NAME,SUBNET_CIDR}`, `ROKSBNKCTL_TRUSTED_PROFILE_{SA,ROLES}`, `ROKSBNKCTL_VLAN_PREFIXLEN{,_EXTERNAL,_INTERNAL}`, `ROKSBNKCTL_GTM_{URL,USERNAME,PASSWORD}`. Every Forge module builds its workspace from the environment alone, so a field reachable only through YAML could not be used by a blueprint.

### Fixed

- **A custom `bnk.flo_namespace` silently dropped nine SCC bindings** ([#65](https://github.com/jgruberf5/roksbnkctl/issues/65)). A guard compared the namespace against the *default as a literal*, so any other value kept the utils half and lost the FLO half — TMM and DSSM came up without privileged SCC, failing in the cluster at pod start, naming service accounts rather than the setting that caused it.

- **Credentials were not redacted from the applied-tfvars snapshot.** That file is documented as suitable for committing, and `bigip_password` and `registry_mirror_password` had been in it unredacted; `cneinstance_gtm_password` would have joined them.

- **`roksbnkctl init` asked for the self-IP mask before the VLAN CIDRs** it told you to match, then offered `24`. Zones now come first, and the mask is suggested from the CIDRs when they agree — a prompt default only, never a derivation.

- **The docs generator could be broken by its own house style.** Heredoc descriptions became the way rationale is written in `variables.tf`, but a `}` in prose truncated a variable silently and a lone quote failed the whole run, taking `make book` down.

### Notes

The GTM environment-variable names inside the CNEInstance are emitted under both `GSLB_GTM_*` and `GTM_*`, pending verification against the BNK 2.3 install guide — the same cross-version hedge already used for `CLOUD_VPC`/`VPC_NAME`. The book documents no CNEInstance env name until that is settled.

## v1.43.0 — 2026-08-11

Preparation for BNK 2.4 and multi-NIC ROKS, neither of which has shipped. Everything here is **additive**: absence of every new setting reproduces the previous behaviour exactly, and no existing `config.yaml`, environment variable, or CLI invocation changes.

### Added

- **`cluster.network_mode`** ([#63](https://github.com/jgruberf5/roksbnkctl/pull/63)) — how a cluster's worker nodes are attached, `single-nic` (default) or `multi-nic`. The only field added to `config.yaml`.

  ```yaml
  cluster:
    network_mode: single-nic    # omitted means exactly this
  ```

  `ROKSBNKCTL_CLUSTER_NETWORK_MODE` sets the same one for runners that never write a `config.yaml`. Renders to a new terraform variable, `cluster_network_mode`.

  **Create-time only, and enforced.** A cluster is never converted between modes in place, so an *explicit* value that contradicts the built cluster is refused before planning — what terraform would otherwise plan is a *replacement* of the running cluster, rendered as output that reads like an update. An *unset* value defers to what the cluster's record says; silence is not an assertion.

- **`cluster-outputs.json` is now a versioned contract.** New optional fields: `schema_version` (2), `network_mode`, `vpc_cidr` (the block a VPC was actually *created* from — empty when it was adopted), and `node_interfaces` (declared, written by nothing yet). Absent `schema_version` reads as 1 and absent `network_mode` as `single-nic`, so every record written before this release stays valid. Unknown fields are ignored, so an older build still reads a newer file.

- **A support matrix, as data** (`internal/config/support_matrix.yaml`) — which BNK release lines drive which network modes, and which contract versions each can read. Checked before planning, so an unsupported pairing is a message rather than a failed apply against real infrastructure. The BNK line is *derived* from the existing `bnk.manifest_version` (`2.3.0-…` → `2.3`); there is no new field that could disagree with the manifest being installed.

  A line the matrix has never heard of **warns and proceeds**. The matrix ships inside the binary, so an unknown line usually means the binary predates the release — that is missing information, not a known incompatibility, and refusing would make every build refuse every release that shipped after it.

- **Per-BNK-line terraform overlays** — `terraform/lines/<line>/` is layered onto the base tree at extraction: same path replaces, new path is added, nothing is removed. It ships **empty**, because no supported release needs different HCL, and a test pins that the extracted tree is byte-identical without one. This replaces the branch-per-release model, which forked the whole tool to express a difference living in a handful of `.tf` files.

### Changed

- **`cluster.vpc_cidr` now warns when it changes after the cluster exists.** It has always been documented as create-time-only and was never enforced; the warning is the deprecation, and the refusal follows it in a later release rather than arriving without one. It fires only on a genuine disagreement with the recorded block, so a workspace that set it once and left it alone stays silent.

### Documentation

- 16 chapters updated for the above, and the release-branch model removed from Chapters 23 and 31 — it had been deleted from the code in v1.42.0 but survived in the book, where Chapter 31 contradicted itself. Chapter 32 gains "Supporting a new BNK release". Chapters 27 and 29 regenerated.

### Not yet verified

Multi-NIC and BNK 2.4 have never been executed, because neither exists yet. `cluster_network_mode` is declared in HCL and consumed by no module; `node_interfaces` is populated by no writer; the matrix's `2.4` row is an expectation and is marked PROVISIONAL in the file itself. What *is* verified end to end on real IBM Cloud is the additive claim — a fresh ROKS cluster on an adopted shared Transit Gateway with BNK 2.3 installed, no new setting anywhere, no guard firing.

## v1.42.0 — 2026-08-08

### Added

- **The reachability gate's timers are now workspace settings** ([#57](https://github.com/jgruberf5/roksbnkctl/issues/57)). Both were constants; the right values are a property of the environment, not of the tool.

  ```yaml
  bnk:
    preflight:
      reachability_retry_seconds: 180     # per target, before the verdict is believed
      reachability_timeout_seconds: 480   # for every node to report
  ```

  `ROKSBNKCTL_REACHABILITY_RETRY_SECONDS` and `ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS` set the same two on the CI path, where the workspace is built by `init --non-interactive --override-from-env` and there is no `config.yaml` to edit.

  `0` retry seconds is a legitimate setting — one shot, for a static environment where a failure is never a race — and is distinguished from "unset". The timeout is clamped to stay above the retry budget, because a wait shorter than the retry gives up while the probe is still working and reports a timeout that reads as a network problem but is really the config.

### Fixed

- **The reachability gate raced Transit Gateway route propagation, and its verdict was sticky** ([#57](https://github.com/jgruberf5/roksbnkctl/issues/57)). Two independent problems, both reported from a real run.

  A TGW attachment is asynchronous: IBM programs the routes some time after the connection reports `attached`. The probe ran once, ~73 seconds after attach, treated a single TCP failure as terminal, and `bnk up` refused a path that was healthy minutes later — while a sibling cluster on the same gateway and the same blueprint passed simply by landing on the other side of route programming. "Unreachable" and "not reachable *yet*" are different claims, and one connect attempt cannot tell them apart.

  Each target is now retried until its budget is spent. A success ends the retry immediately, so a healthy environment pays nothing, and a failure reports how hard it tried:

  ```
  ✗ kube-…-0000013a -> registry (10.243.0.4:443): dns=skipped-ip tcp=FAILED
      (tcp connect failed -- refused, filtered, or no route
       (still failing after 19 attempts over 180s))
  ```

  The verdict was also a recording. The probe runs once per pod and then holds the pod Ready, and the DaemonSet's pod template changed only when the CA or the target list changed — so a second `bnk up` with identical inputs did not roll it, the pods kept sleeping, and the collector re-read a log written minutes or hours earlier. Someone who fixed the routing and re-ran was shown the original failure, with nothing to indicate it was stale. The gate now rolls the DaemonSet every run: one pod restart per node against a node-cached image, and the CA install is idempotent.

- **`kubectl`/`oc` passthroughs ignored `-w` and could target the wrong cluster** ([#55](https://github.com/jgruberf5/roksbnkctl/issues/55)). The `k` verbs already resolve a workspace-scoped kubeconfig *and context*; the raw passthroughs did not. They took the ambient `KUBECONFIG` and then `preferForgeKubeconfig` replaced it with `~/.roksbnkctl/forge/kubeconfig.yaml` — a single file every workspace shares. Two workspaces on different clusters therefore retargeted each other, so `-w a kubectl get nodes` could list cluster **b**'s nodes, and `-w a kubectl delete …` could delete in **b**.

  The failure was silent and the output believable, which is the worst combination — downstream harnesses had grown an explicit "verify the cluster identity before asserting" guard to work around it.

  The local-exec verbs now resolve the same target the `k` verbs do, pinning both the kubeconfig and the `--context`. Deliberately conservative: a workspace with no known cluster keeps the historical ambient behaviour, and an explicit `--context`/`--kubeconfig` from the caller always wins.

  The pin lives at one chokepoint rather than at the call sites, because *when* it is applied is what makes it correct. It runs **after** the `-w` extraction — the passthroughs disable flag parsing, so `-w` arrives inside argv, and resolving any earlier pins the *current* workspace instead of the named one, which is the same bug made deterministic. It runs **after** the local/remote split, so a local path and context are never forwarded across the `--on` SSH boundary that `WorkspaceEnvCore` exists to scrub. And it covers `shell` and `exec` too, which reach a cluster just as surely as `kubectl` does. A known cluster with no usable kubeconfig on disk now fails with the same "run `kubeconfig --download`" message the `k` verbs give, instead of quietly falling back to the shared file.

- **`bnkforge register` was destructive** ([#54](https://github.com/jgruberf5/roksbnkctl/issues/54)). It DELETEd any same-named cluster and re-POSTed it. Within one project that was called idempotent, but the **cluster id changed**, breaking references and discarding the scan history attached to it. Across projects it was worse: the lookup was project-scoped, so a cluster held by another project was invisible and the POST either moved it silently or failed with a bare exit 1 naming nothing.

  Registration is now non-destructive: held by **this** project → updated in place, id preserved; held by **another** → refused, naming the owning project and its ids, with `--force` to take over deliberately; held by nobody → created.

  Several safety properties are deliberate. The same-project lookup asks that project directly rather than trusting `/api/projects` to list it — a paginated or permission-filtered list that omitted the owner would otherwise send us straight to POST, over the top of the cluster this guard protects. The in-place update falls back to the old delete-and-recreate only on `404`/`405`, i.e. when the route genuinely is not there: treating *every* PUT failure that way would let a transient `500` destroy the cluster id this change exists to preserve. `--force` across projects is a real move (DELETE from the owner, POST into the target) rather than an in-place update, which would have left the cluster registered where it was while reporting success. And the cross-project scan warns rather than fails when a project cannot be read, so a least-privilege Forge account is not blocked from registering into its own project.

  **The automatic registration that runs inside `cluster up` / `bnk up` never forces.** An unattended step silently taking another project's cluster is precisely the harm.

- **`bnk up` spent ~13 minutes failing opaquely on a cluster that already had BNK** ([#53](https://github.com/jgruberf5/roksbnkctl/issues/53)). It could not distinguish "nothing is installed" from "something is installed that I do not own", so an empty workspace state planned a full install — `resources_to_add=64` over a cluster that already had all 64 — and ground on for about thirteen minutes before exiting 1 without naming the cause or the cluster, three times over with retries.

  This is the normal shape when a cluster moves between deployments: BNK Forge gives each project its own deployment-scoped volume, so the second project legitimately has no state for the first's install.

  `bnk up` now refuses **before planning**, naming the cluster, the namespace and the options. The check is narrow by construction — it fires only when the workspace's BNK state holds no managed resources *and* the cluster is actually serving a BNK install — so a workspace that owns an install converges exactly as before. If the cluster cannot be reached, or what the state holds cannot be determined, the guard stays silent: the point is to turn a slow confusing failure into a fast clear one, not to add a new way to refuse.

  "What the state holds" is deliberately not read off disk alone. With `state.backend: s3` (PRD 16) the state lives in object storage, so there is no local `terraform.tfstate` and a workspace that installed BNK itself would read as empty — the guard would then refuse its every converge, advising the user to "use the workspace that installed it", which is the workspace being refused. The on-disk read stays the fast path for the local backend; a remote backend it cannot answer for is confirmed with `terraform state list`, and an unanswerable one stays silent.

  This is the "minimum" the issue proposes. A full `bnk adopt`, mirroring `registry adopt`, is not included here — it needs to derive and validate a complete install record from a live cluster, which is a larger change than a guard and wants its own testing against real installs.

## v1.41.0 — 2026-08-07

### Added

- **`bnk up` proves the mirror and the licence proxy are reachable from every node before installing** ([#52](https://github.com/jgruberf5/roksbnkctl/issues/52)). An unreachable **mirror now fails the install**; the licence proxy is reported but not fatal.

  ```
  → installing registry CA trust on all nodes (10.243.0.4) and checking reachability
    F5-License-Proxy: 3/3 nodes reachable
    registry: 3/3 nodes reachable
  ✓ registry CA installed on all nodes; 10.243.0.4 is trusted and reachable
  ```

  **It runs on the nodes, because nowhere else can answer the question.** The operator host sits on the services VPC with egress; the workers are air-gapped behind a transit gateway. A check from where `roksbnkctl` runs returns a confident green for a mirror the cluster cannot route to — demonstrated during validation, where `registry adopt` timed out reaching Harbor from the operator while all three nodes reached it fine. It rides the DaemonSet that already installs the registry CA, so it needs no new machinery and no egress, and one pod per node means every availability zone is covered without enumerating them.

  Previously an unreachable mirror surfaced as `ImagePullBackOff` and then a helm `context deadline exceeded` roughly ten minutes later, naming neither the registry nor the node.

  **The CA is now optional.** An empty CA used to skip the whole step. A registry already trusted by the node bundle — or one with a publicly-signed certificate — needs no CA installed and can still be completely unroutable, which is the failure that costs an hour. An empty CA now skips only the install; the probes still run.

  DNS and TCP are reported separately, because the fixes differ: a name that will not resolve is a resolver problem, a refused connection is routing, security groups, or overlapping VPC prefixes ([#46](https://github.com/jgruberf5/roksbnkctl/issues/46)). Every node is listed, not just failures — `3/3` is what stops someone chasing the network when the fault is elsewhere, and a per-zone split is only visible if the passes are shown too.

  Validated against a real disconnected cluster (3 nodes, 3 AZs, Harbor and the FLP reachable only over a transit gateway): the success path as part of a complete air-gapped install, and the failure path against an unroutable address with the real mirror as a control, confirming `registry: 0/3` and the error that stops `bnk up`.

### Fixed

- **The gate could pass without hearing from every node.** Results were read from whatever pods existed at that instant, so a pod still starting — or mid-rollout after the target set changed — contributed nothing and its node vanished from the summary. Observed live: three nodes, three Ready pods, all three with correct results in their logs, and the summary printing `0/2 nodes reachable`. Harmless in that direction; the reverse is not, since two passes and one silent node reads as `2/2 reachable` and lets through exactly the per-AZ break the probe exists to catch. Coverage now comes from the DaemonSet's `DesiredNumberScheduled` and is polled before any verdict is issued.

- **A probe label containing a space truncated at the parser**, so `F5 License Proxy` reported as `F5`. Cosmetic there, but `Required` is keyed on the label — the same bug on a multi-word required target would have silently downgraded it to optional.

### Documentation

- Appendix A documents the reachability gate, and now states plainly that **the operator host must itself reach the mirror**: `bnk up` pulls the manifest and charts host-side, so running from a laptop that is not on the transit gateway stops at `helm pull … i/o timeout`. The node gate does not cover this — it answers for the nodes, which is a different question. The operator toolchain (terraform ≥ 1.10, helm) is now spelled out, since a stock VSI has none of it.

- **The CLI demo installed terraform 1.9.8 — below the 1.10 floor roksbnkctl enforces**, so it provisioned a terraform its own binary rejects. Now 1.10.5, matching the runner image, the book, and the enforced floor. helm aligned to 3.16.3, and the demo runner tag to this release.

## v1.40.3 — 2026-08-07  (docs / demo only)

No code changes — `internal/`, `terraform/` and `cmd/` are untouched since v1.40.2. This
release exists to publish the book.

### Documentation

- **Appendix A rewritten around the four cluster topologies**, each covered twice — once from the
  CLI, once as an Argo Workflow:

  | | Topology | Cluster | Worker egress |
  |---|---|---|---|
  | A | New + connected | roksbnkctl creates it | yes |
  | B | New + disconnected | roksbnkctl creates it | no |
  | C | Existing + connected | adopted | yes |
  | D | Existing + disconnected | adopted | no |

  It previously documented exactly one of those (an air-gapped cluster adopted by hand) and buried
  the CI story in a trailing section. Connected clusters need no mirror and no proxy, so the
  appendix now says that in the first screen and routes those readers past the services-infrastructure
  half entirely, instead of leaving them to infer it.

- **Versions are now stated.** Argo Workflows **v4.0.8**, k3s **v1.36.3+k3s1**, runner **v1.40.2**,
  terraform **1.10.5**, OpenShift **4.18.51** — what this was actually deployed and tested on, with
  each floor and its reason. The runner image had been pinned at `v1.33.0` in nine places, which is
  below the floor for `registry adopt` (v1.36.0) and below the fix for a terraform 1.10 validation
  crash (v1.40.1); anyone following the appendix literally would have hit that.

- **New "bring your own controller" section** for sites running their own Argo — stating plainly
  that these are Argo **Workflows** pipelines and not Argo CD `Application`s, and listing the
  constraints that each cost a real run: the controller must be able to reach the mirror's private
  IP and the cluster API (so hosted/SaaS Argo cannot drive it); one workflow at a time per
  workspace; one workspace per cluster; `ROKSBNKCTL_REGISTRY_TARGET` must be set.

- `07-quick-start` claimed `terraform >= 1.5`. The enforced floor is **1.10**
  (`requireTerraformVersion`, and Chapter 12a explains why). The gap was not academic — 1.10 and
  1.15 differ in `||` short-circuiting inside variable validation, which is exactly what shipped
  broken in v1.40.0.

### Fixed (demo)

- **`ROKSBNKCTL_REGISTRY_TARGET` was missing from the Argo demo's `.env.example`.** `registry adopt`
  has no `--target` flag — it reads the workspace config — so it defaulted to `icr` and air-gapped
  clusters tried to pull `us.icr.io/...` and failed `unauthorized`. `wf-far-mirror` passing
  `--target generic` as a *flag* filled the mirror correctly but wrote nothing the later adopt read,
  so the two silently disagreed.

- **The demo's teardown still walked the old shared workspace**, where no cluster lives since the
  one-workspace-per-cluster split — it would have destroyed nothing and reported success while two
  ROKS clusters kept running. It now walks each workspace, runs `tgw disconnect` between `bnk down`
  and `cluster down` (`cluster down` refuses while a connection pins the VPC's CRN), and takes an
  optional workspace filter so each phase comes down under the environment it was built with.

## v1.40.2 — 2026-08-07

### Fixed

- **The address-prefix guard refused a workspace's own re-run.** The `cluster up` guard added in v1.39.0 compared the prefixes it intended to use against *every* VPC attached to the gateway — including the one this workspace had already created. `cluster up` is idempotent and gets re-run constantly (after a partial failure, or just to converge), and on every run after the first the guard found itself:

  ```
  the cluster VPC's address prefixes overlap a VPC already attached to transit gateway "bnkci-testing":
    10.243.0.0/18 overlaps 10.243.0.0/18 on VPC "bnk-dc-cluster-vpc"
  ```

  `bnk-dc-cluster-vpc` *is* the workspace's own VPC. A guard that blocks an idempotent re-run is worse than no guard, because the retry after a partial failure is precisely when it needs to get out of the way — and the suggested remedies (pick another block, detach the other VPC) are both wrong when the "other VPC" is you.

  The guard now excludes its own VPC, identified two ways because either can be the only one available: the id recorded in `cluster-outputs.json` (absent until a run completes) and the name terraform derives from the prefix (valid before anything is recorded). The `tgw connect` guard already did this; only the `cluster up` one was missing it, because it was written assuming the VPC does not exist yet — true on the first run and false on every one after.

  Found by running the disconnected Argo workflow twice against real infrastructure.

## v1.40.1 — 2026-08-07

### Fixed

- **`cluster_vpc_cidr`'s size validation broke every terraform plan on the terraform we ship.** v1.40.0 (and v1.39.0) carried:

  ```hcl
  condition = var.cluster_vpc_cidr == "" || tonumber(split("/", var.cluster_vpc_cidr)[1]) <= 18
  ```

  written on the assumption that `||` short-circuits. In **terraform 1.10** it does not — both operands are evaluated — so with the variable at its default `""` the right side ran `split("/", "")[1]` and raised `Invalid index` before any plan could start. Since `""` is the default, this failed *every* phase (`cluster up`, `flp up`, `bnk up`) for anyone who had not set the variable. Fixed by wrapping the size check in `try(..., false)`, which gives `||` the behaviour it was assumed to have.

  **Why every gate missed it.** Two blind spots lined up:

  1. `terraform validate` does not evaluate `validation` conditions against values — it only type-checks configuration. The pre-tag gate ran `validate` and passed.
  2. Terraform **changed this behaviour between releases**: 1.15 short-circuits `||` here, 1.10 does not. Dev machines had 1.15; the runner image ships **1.10.5**, and roksbnkctl's stated floor is 1.10 (`requireTerraformVersion(…, 1, 10)`). So local testing exercised a terraform no user is obliged to have, and the failure only appeared on a real pipeline run.

  New gate closing both: `scripts/tf-variable-validation-test.sh` runs real `terraform plan`s over each `validation` block, **inside the runner image** so the version under test is the version that ships. It is `make tf-validation-test`, and step 3 of 9 in `make release`. Reverting the fix makes it fail, which is how it was verified.

## v1.40.0 — 2026-08-06

### Changed

- **Dependency updates** ([#47](https://github.com/jgruberf5/roksbnkctl/pull/47), [#48](https://github.com/jgruberf5/roksbnkctl/pull/48), [#49](https://github.com/jgruberf5/roksbnkctl/pull/49)). No behaviour change; the direct bumps are:

  | Module | From | To |
  |---|---|---|
  | `github.com/IBM/go-sdk-core/v5` | `5.23.1` | `5.23.2` |
  | `github.com/IBM/ibm-cos-sdk-go` | `1.14.1` | *held at `1.14.1` — see below* |
  | `github.com/google/go-containerregistry` | `0.21.7` | `0.21.8` |

  `go-sdk-core` `5.23.2` is a dependency-vulnerability fix, which is the reason this is not deferred. It carries `golang.org/x/net` `0.56.0` → `0.57.0`, `x/mod`, `x/tools`, `go-openapi/strfmt`, `klauspost/compress`, `leodido/go-urn`, `oklog/ulid` and `gabriel-vasile/mimetype` along with it; `go-containerregistry` pulls `docker/cli` `29.5.3` → `29.6.2`.

  Also `docker/login-action` `4.5.2` → `4.6.0` and `github/codeql-action` `4.37.3` → `4.37.5` in the workflows.

  Verified locally rather than on the runner: `go mod tidy` produced no change to `go.mod`/`go.sum`, and `build`, `vet`, `staticcheck`, the unit suite and the `-tags integration` suite against an ephemeral kind cluster all pass. GitHub Actions was not delivering `push` events for this repository while these merged, so the usual PR checks did not run — see the release note below.


### Fixed

- **Held `ibm-cos-sdk-go` at `1.14.1`: `1.15.0` does not compile for Windows.** Dependabot's group bump ([#48](https://github.com/jgruberf5/roksbnkctl/pull/48)) took it to `1.15.0`, which fails both Windows targets:

  ```
  ibm-cos-sdk-go@v1.15.0/service/s3/s3manager/pipe_download.go:253:20: undefined: unix.FcntlInt
  ```

  Upstream added a pipe-size probe that calls `unix.FcntlInt` — a symbol that does not exist on Windows — and guards it with a **runtime** `runtime.GOOS != "linux" && != "darwin"` check rather than a build constraint. The file carries no `//go:build` tag at all, so the reference is compiled on every platform and the runtime guard never gets the chance to help.

  Nothing in the unit suite catches this, because it only appears when cross-compiling: `linux/*` and `darwin/*` build and test cleanly. It surfaced in the pre-tag goreleaser snapshot, which is the gate that exists for exactly this. Left unpinned it would have shipped a release with the two Windows assets missing — and `roksbnkctl self update` resolves assets by name, so Windows users would have been broken rather than merely stale.

  `go-sdk-core` `5.23.2` and `go-containerregistry` `0.21.8` are unaffected and kept. All six release targets (`linux`, `darwin`, `windows` × `amd64`, `arm64`) are verified to compile.

### Notes

- **GitHub Actions did not fire on `push` for the `v1.39.0` tag or for the three dependency merges.** Every workflow is `active`, the repository is public with Actions enabled and `allowed_actions: all`, and `workflow_dispatch` runs start normally — so this is event delivery, not configuration. `v1.39.0`'s Release and container builds were started with the `workflow_dispatch` fallback that `release.yml` documents for exactly this case, and both succeeded. Worth re-checking that tag pushes trigger on their own before relying on them again.

## v1.39.0 — 2026-08-06

### Added

- **`cluster.vpc_cidr` — each cluster VPC can now own its address block.** ([#46](https://github.com/jgruberf5/roksbnkctl/issues/46))

  Nothing in `terraform/` ever set `address_prefix_management`, so every VPC roksbnkctl created took IBM Cloud's default of `auto` — and `auto` assigns the **same** three per-zone prefixes to **every** VPC in a region:

  ```
  10.241.0.0/18   10.241.64.0/18   10.241.128.0/18
  ```

  A Transit Gateway routes on those prefixes. Two attached VPCs claiming the same block make the route ambiguous, and the gateway resolves it by silently blackholing traffic for one of them — no error, no log line. It surfaces much later as *intermittent* image-pull timeouts against a private mirror while every security group and network ACL in the path plainly allows the traffic. Because a disconnected cluster **must** share a gateway with its registry, the second disconnected cluster on a gateway collided by construction rather than by bad luck.

  Set a distinct block per cluster:

  ```yaml
  cluster:
    create: true
    vpc_cidr: 10.242.0.0/16      # ROKSBNKCTL_CLUSTER_VPC_CIDR for argv+env callers
  ```

  The block is split into three per-zone prefixes (`cidrsubnet(cidr, 2, i)`), so **`/18` is the smallest usable value** — validated at plan time in all three module levels rather than failing mid-apply.

  **Opt-in, and the default is a no-op by design.** Empty (the default) leaves `auto` in place. That matters because moving a live subnet's CIDR *replaces* the subnet — and the cluster on it — so this could not be switched on for existing workspaces. It is also why `10.241.0.0/16` yields prefixes byte-identical to what `auto` gives today: setting it explicitly on a first cluster changes no addresses, so the value can be adopted without a rebuild.

### Fixed

- **`cluster up` and `tgw connect` now refuse an overlap instead of building one.** The failure above is invisible at the layer it happens, so both doors into it are guarded:

  - `cluster up` computes the prefixes the VPC *would* take and compares them against every VPC already attached to the gateway — **before** terraform creates anything.
  - `tgw connect` compares the existing VPC's actual prefixes before attaching it.

  Both name the conflicting VPC and the colliding CIDRs, and state the remedy (a distinct `vpc_cidr`, or detaching the other VPC — which does not require destroying its cluster). Both are **best-effort**: an unreachable API, an unresolvable gateway, or an unreadable VPC warns and continues, so this is a guard against one silent failure rather than a new precondition on building a cluster at all. Only `attached`/`pending` connections count, matching `tgwConnectionForVPC` — a `deleting` attachment is on its way out and must not block a build.

### Documentation

- New [Give each cluster VPC its own address block](book/src/09a-transit-gateway-sharing.md) section with a worked allocation table for a shared gateway, and what to do when two overlapping clusters already exist.
- New troubleshooting entry for the actual presenting symptom — *intermittent* mirror pull timeouts with every security group and ACL open — including the `ibmcloud tg connections` / `vpc-address-prefixes` commands to confirm it.
- `cluster.vpc_cidr` and `ROKSBNKCTL_CLUSTER_VPC_CIDR` added to the configuration reference (all three tables), the terraform variable reference, the unattended-setup env table, and `config.example.yaml`.

## v1.38.0 — 2026-08-06

### Added

- **Cluster topology is now reachable from the environment.** An argv+env runner (a BNK Forge container module, a CI job) could build a cluster but could not say what *kind* of cluster, because the fields that decide it had no env override:

  - `ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY` → `cluster.public_gateway`. This is the switch that makes a new cluster **disconnected**. `public_gateway` is a `*bool` so that unset stays distinct from an explicit `false`, and only a non-nil value renders `cluster_public_gateway` — so from env alone every cluster inherited terraform's default of `true` and came up with worker egress. A disconnected cluster simply could not be built without a hand-written `config.yaml`.
  - `ROKSBNKCTL_TGW_JUMPHOST_CREATE` → `resources.tgw_jumphost.create`
  - `ROKSBNKCTL_CLIENT_VPC_CREATE` → `resources.client_vpc.create`
  - `ROKSBNKCTL_CLIENT_VPC_NAME` → `resources.client_vpc.existing` — the jumphost lives *in* a client VPC, and terraform resolves that as "the one we created, else the named existing one". Without this the env surface could only express the create branch, so opting the jumphost in without also creating a VPC produced a config terraform cannot plan.

  An unparseable value is ignored rather than guessed at: silently picking a topology is worse than leaving the default in place.

### Fixed

- **`init --non-interactive` no longer builds a testing client nobody asked for.** `DefaultResources()` set `tgw_jumphost.create` and `client_vpc.create` to `true`, while the interview asks *"Add a testing client?"* and defaults to **no** — so the two paths disagreed, and the non-interactive one erred toward creating a jumphost VSI and a client VPC unprompted. The client VPC also consumes a **Transit Gateway connection**, which is a quota'd resource, so this was not a free mistake. The function's own doc comment claimed it mirrored the interview defaults; now it does. Opt in with the two new env vars.

  This changes behaviour for existing non-interactive users: a run that previously produced a jumphost and client VPC no longer will.

- **A TGW jumphost with nowhere to live is now rejected at `init`.** `resources.tgw_jumphost.create: true` with neither `client_vpc.create` nor `client_vpc.existing` left terraform's VPC lookup (`modules/testing/data.tf:69`) resolving an empty name, failing opaquely mid-apply. `init` now fails fast on both seed paths and names the two ways out.
- **One definition of the resource defaults, not three.** `DefaultResources()` said the testing client was off while `internal/tf/vars.go`'s nil-`resources` fallback still inlined an all-true set — and that is a *provisioning* path, so a legacy or hand-edited `config.yaml` with no `resources:` block would have gone on silently building a jumphost and client VPC. Both it and `init.go`'s display-only `allCreateResources` now call `config.DefaultResources()`, so the two cannot drift apart again. (That drift is the same failure this release fixes between `DefaultResources` and the interview.)
- **The cluster-lifecycle demos no longer ship a no-op testing phase.** `testing up` provisions exactly the three toggles, and `cluster_jumphosts` was already off — so defaulting the testing client off left it provisioning nothing. Both demos write a `config.yaml` with no `resources:` block and then run `testing up` + `test`, where `test` runs its probes *from* a jumphost. Their seeds now request the jumphost and its VPC explicitly.

### Changed

- **Book.** The configuration reference documented `resources.tgw_jumphost.create` defaulting to `true` in three places (the example block, the toggle table, the field table) — corrected, with the client-VPC dependency and the Transit-Gateway-connection cost called out. `cluster.public_gateway` now names its env override and explains the tri-state.

## v1.37.0 — 2026-08-05

### Added

- **`roksbnkctl bnkforge unregister` — undo what `register` did.** A workspace could register its cluster with BNK Forge and had **no way to remove it**. `bnkforge disable` reads like the inverse but only clears the local auto-register flag and never contacts the server, so after a teardown the cluster stayed on Forge's Kubernetes page — inside a project that outlived everything it was created alongside, pointing at a cluster that might no longer have BNK on it, or might no longer exist. The capability was already half-present: `RegisterCluster` deletes a same-named cluster before re-POSTing (which is why the cluster id climbs on every re-registration); this exposes that half on its own.

  **Absence is success** — no project, no cluster of that name, or a cluster already gone all report and exit 0, so it is safe on a destroy path that may run twice or run late. Only a genuine server failure surfaces as an error. It also **never creates anything**: unlike `register` it looks a project up rather than ensuring it, because a teardown asking *"is this still here?"* must not bring it into being.

  Registration is **not** removed automatically on `cluster down` — the post-`up` hook has no matching hook on the way down, so a teardown must call `bnkforge unregister` explicitly.

### Fixed

- **`bnkforge disable` no longer implies it unregisters.** Its help and the book now state plainly that it is a local flag which never contacts the server, and point at `bnkforge unregister`.
- **A BNK Forge project list that fails to parse is no longer read as "no project".** `ProjectIDByName` swallowed the unmarshal error, so an HTML error page or an unexpected response shape produced `✓ no BNK Forge project — nothing to unregister` and exit 0 while the cluster stayed registered forever — a silent success is the one outcome a teardown must never produce. It now accepts both `{"projects":[…]}` and a bare array (matching `projectClusterID`, which already tolerated both) and returns an error with a truncated excerpt of the body when neither shape fits. Absence is still success; "I could not tell" is not.

### Changed

- **Book.** `bnkforge unregister` documented in the command reference; a new "Unregistering on teardown" section in the BNK Forge chapter covers the absence-is-success cases, the never-creates guarantee, and the fact that `cluster down` does not unregister automatically. The `disable` entry is corrected in both places.
- **`.gitignore`** now excludes built book PDFs (`roksbnkctl-book-*.pdf`) and `scripts/scratchpad/`. Multi-megabyte release artifacts had been swept into a feature branch by `git add -A`; git history is permanent, so these must never be committed.

## v1.36.0 — 2026-08-05

### Added

- **`roksbnkctl registry adopt` — use a mirror this workspace did not populate.** `bnk up` refuses to render against a mirror the workspace has no record of, but only `registry replicate` ever wrote that record — so a workspace could only use a mirror it had filled itself. That is the wrong constraint: a registry is populated once as a supply-chain step, then many installs pull from it, often from a different workspace, host or team. Those installs had to re-run `replicate` purely to re-derive a record, and `replicate` needs the FAR source (`repo.f5.com`) reachable at install time — which an air-gapped operator usually does not have, that being the whole point of mirroring. `adopt` derives the record from the configured registry target and asks the *mirror* what it holds, so it needs **no source access at all**. `--verify-contents` builds the BOM and digest-checks every artifact before recording, when the source *is* reachable.
- **`registry verify` now points at the fix.** A mirror proven good but unrecorded prints a note naming `registry adopt`, so it is one obvious command away from usable. `verify` itself stays strictly read-only: a verb that promises inspection should not change what a later `bnk up` does.

### Fixed

- **`registry adopt` and `registry delete` did not trust the mirror CA.** Both built a copy engine without `RegistryCA`, and `craneOpts` only installs a private CA when that field is set — so against a **self-signed mirror**, which is the entire case this subsystem exists for, both ran against the system roots alone. `adopt`'s catalog probe failed `x509` and degraded to a warning, meaning the command *succeeded* while silently losing its only validation (the prefix-typo guard could never fire); `adopt --verify-contents` reported every artifact as missing against a healthy mirror; and every manifest DELETE failed. `registryEngine` now takes the CA as a **required parameter**, so a call site cannot omit it — which is how the second instance was found.
- **An adopted inventory now carries digests.** `--verify-contents` recorded `Kind`/`Name`/`Tag` but no digest, so `registry delete` fell back to tag-based deletion rather than the digest-based form. `Verify` returned only failures, discarding digests it had just resolved; the new `VerifyAll` returns one result per artifact with its target digest, and `Verify` filters it, so its contract and existing callers are unchanged.
- **`bnk up`'s mirror guard no longer sends air-gapped operators down a dead end.** `guardRegistryMirror` and `ErrNoRegistryMirror` told the operator to run `registry replicate`, which needs the FAR source. They now name both paths and say which applies:

  ```
  a registry mirror is configured for this workspace but this workspace has no record of it.
    If the mirror still needs filling:      roksbnkctl registry replicate   (needs the FAR source)
    If it is already populated elsewhere:   roksbnkctl registry adopt       (no source access needed)
  ```

### Changed

- **Book.** New `registry adopt` command-reference section; the air-gap chapter's "One mirror, many clusters" guidance corrected (it told a second workspace to re-run `replicate`, which cannot run without the source), and its TLS-trust aside updated — roksbnkctl installs the mirror CA on every node itself from `registry.generic_ca_b64`, rather than the operator doing it by hand.

## v1.35.0 — 2026-08-04

### Changed

- **The air-gap mirror CA is now authenticated out of band instead of trusted from the wire.** A CA captured from the mirror is installed into **every node's `certs.d`** and persisted in `registry-mirror.json`, so adopting one over an unauthenticated connection handed durable, cluster-wide trust to whoever won a race on a single dial. `registry replicate` now resolves the CA in descending order of authority — `--registry-ca` → `registry.generic_ca_b64` → the CA a previous replicate recorded → a **pinned** capture — and the first three never touch the network for trust. Since you generate the CA, the intended path is to supply it from the file that generated it: `roksbnkctl registry target generic_ca /opt/harbor/certs/harbor.crt`.
- **A captured CA must now be pinned, or the capture is refused.** `registry.generic_ca_sha256` / `--registry-ca-fingerprint` pin the served chain by SHA-256, enforced inside `tls.Config.VerifyPeerCertificate` so the check runs *during* the handshake, before any bytes move, against the chain the peer actually presented. Matching is constant-time over the DER, so a peer must hold the private key — replaying a copied public certificate cannot complete the handshake. The pin is accepted as bare hex, `sha256:…`, colon-separated, or raw `openssl x509 -fingerprint -sha256` output.
- **Fail closed.** With no CA and no pin, `replicate` refuses a self-signed mirror rather than silently adopting it, and the refusal quotes the fingerprint the host actually served so a first-time operator can record the pin without a second tool. `--insecure-capture-ca` accepts trust-on-first-use deliberately. **Public mirrors are unaffected** — a chain to a separate issuer adopts nothing, so it needs no pin, and ICR/publicly-issued targets behave exactly as before.

  > **Upgrade note.** A workflow that relied on unpinned auto-capture against a **self-signed** mirror will now error until it supplies `registry.generic_ca_b64` / `--registry-ca`, sets a pin, or passes `--insecure-capture-ca`. This is deliberate: the previous silent adoption is the behaviour being fixed.

- **New workspace fields + env overrides:** `registry.generic_ca_b64` (`ROKSBNKCTL_GENERIC_CA_B64`, verbatim — already base64) and `registry.generic_ca_sha256` (`ROKSBNKCTL_GENERIC_CA_SHA256`), plus `registry target generic_ca <file>` (which records the PEM and derives its pin) and `registry target generic_ca_sha256`. An env-only runner facing a self-signed mirror previously had no way to supply trust at all.
- **The disconnected CLI demo** reads Harbor's CA **and** its fingerprint once from `/opt/harbor/certs/harbor.crt` and feeds both the mirror and BNK workspaces, so `registry replicate` no longer passes `--registry-ca`.

### Fixed

- **A refused CA is fatal, not a best-effort miss.** `describeCAPolicyError` wraps its sentinels (`%w`) so `errors.Is` still identifies them in `resolveMirrorCA`. Without the wrap the refusal degraded to "no CA recorded", the node-trust step no-opped, and `bnk up` failed much later with an opaque `x509` error from a pod pull. Regression-tested.

## v1.34.0 — 2026-08-04

### Added

- **`init --override-from-env` now reaches the standalone FLP VSI and the FAR supply chain.** `--override-from-env` is the only way an argv-only caller can build a `config.yaml` — BNK Forge's container engine and CI jobs invoke `roksbnkctl` with an env map and no shell, so a field with no env var is unreachable from them entirely. Two load-bearing groups were missing. The FLP deployment backend gains `ROKSBNKCTL_FLP_MODE` (`helm` | `vsi`) plus `ROKSBNKCTL_FLP_VSI_VPC` / `_ZONE` / `_PROFILE` / `_SSH_KEY` / `_REACH` / `_BOOT_SIZE_GB` / `_FLOATING_IP` / `_{MANAGEMENT,LICENSING}_ALLOWED_CIDRS` / `_STATUS_IMAGE` / `_STATUS_REGISTRY_HOST` / `_STATUS_REGISTRY_CA_B64`; together these reproduce, from env alone, the exact Phase 3 `config.yaml` the disconnected walkthrough writes by heredoc. The FAR supply chain gains `ROKSBNKCTL_MANIFEST_VERSION`, `ROKSBNKCTL_{FAR_AUTH,SUBSCRIPTION_JWT}_LOCAL_FILE`, `ROKSBNKCTL_COS_{INSTANCE,BUCKET,REGION}` and `ROKSBNKCTL_{FAR_AUTH,SUBSCRIPTION_JWT}_FILE` — the COS coordinates previously had **no env surface at all**, pinning an env-only runner to defaults that were renamed in v1.22.0, with the failure surfacing as an opaque fetch error mid-`bnk up`.

### Fixed

- **`registry replicate` was broken outright on any runner image built with a current helm.** Both login call sites (the FAR pull in `registry/source`, the mirror push in `registry/mirror`) passed the secret as `-p <secret>`; current helm **rejects** that with a non-zero exit (`Using --password via the CLI is insecure. Use --password-stdin.`) where it used to only warn. Because the runner image installs "helm latest stable", this was a latent time-bomb rather than a code change: v1.33.1's image works, an image built today does not. Both paths now pipe the secret via `--password-stdin`, which also keeps the FAR service account and the mirror password out of the process table.
- **`GO-2026-5970` — infinite loop on invalid input in `golang.org/x/text`.** Reachable from `ibm.Client.GetCluster`, `k8s.Run` and `tf.init`; fixed by the dependency bump below.

### Changed

- **Dependencies.** `golang.org/x/text` 0.38.0 → 0.40.0 (the CVE above), `golang.org/x/crypto` 0.53.0 → 0.54.0, `x/sync` 0.21.0 → 0.22.0, `x/term` 0.44.0 → 0.45.0, `x/sys` 0.46.0 → 0.47.0, `x/tools` 0.46.0 → 0.47.0, `github.com/IBM/go-sdk-core/v5` 5.22.1 → 5.23.1, `github.com/moby/moby/client` 0.5.0 → 0.5.1, and the `k8s.io/*` set 0.36.2 → 0.36.3. CI: `actions/setup-go` 6 → 7.
- **Every demo now shares one capture pipeline and one CLI contract.** `cluster-lifecycle-{cli,ci}`, `far-replication` and `shared-licensing-{cli,ci}` are refactored onto the `disconnected-cluster-cli-demo` format, so they differ only in *what* they demonstrate. Voiceover is gone — `say`/`note` write the spoken context onto the screen, so a recording needs no TTS, narration files or chapter markers. New shared tooling in `scripts/demos/lib/`: `demo-format.sh` (cleared-screen phase banners; the `PHASE`/`COMMAND`/`OUTPUT` queue rows that drive every still), `post_10x.py`, `record-demo.sh` and `check-masking.sh`. Each demo ships `.env.example` + `record.sh`.
- **Demo recordings are credential-safe by construction.** Each demo registers its secrets with `secret()`, after which `banner`/`say`/`note`/`ok`/`die`/`show` and the `show_file`/`runmask` pair replace every registered value — **and its base64 form** — with `***REDACTED***`. This closed two real leaks in the shared-licensing CI demo, which displayed its `--env-file` with `grep -v PASSWORD` and so printed `IBMCLOUD_API_KEY` verbatim on camera twice. `lib/check-masking.sh` proves it before a shoot.
- **No demo tears itself down.** Each ends with a report naming every reachable web UI and its credentials **by variable name, never the value**, and takes a `teardown` subcommand that removes only what that demo created — adopted clusters (`cluster register`) and the off-camera registry are left running, and the report says so.
- **Capture fixes.** The `COMMAND` mark is taken after a render hold, so the extracted still shows the command rather than its output; `OUT_POST_HOLD` keeps the output on screen after its mark (without it the next phase's `clear_screen` landed ~0.1s later and the "output" still captured the **next phase's banner**); and `roksbnkctl` is matched as a command word — plus the `roksbnkctl-tools-runner` image, since the CI demos invoke every step through it — so a repo path in an argument no longer stamps a spurious still.
- **cspell dictionary** gains 63 technical terms and British spellings, clearing 115 findings across 19 book/docs files.

## v1.33.1 — 2026-07-30

Documentation and demo assets only — **no change to the `roksbnkctl` binary from v1.33.0**.

### Changed

- **The disconnected-cluster CI demo is now Argo Workflows — git-free, with the runner served from Harbor.** It drives the `roksbnkctl-tools-runner` container through two `argo submit` Workflows (mirror + install) on a k3s VSI — no ArgoCD Application, no git repo — sharing a **persistent PVC** so teardown via `bnk down` is clean (an ephemeral `emptyDir` orphans the IAM trusted profile). The runner image is pulled from the **private Harbor mirror** over the TGW (k3s trusts Harbor's CA via `/etc/rancher/k3s/registries.yaml`), so nothing is pulled from a public registry at run time; a silent cwc-guard sidecar clears F5's cwc Multi-Attach (RWO) deadlock on reused clusters by forcing `strategy: Recreate` and cycling replicas.
- **Book Appendix A rewritten to match.** Both topology diagrams are now Mermaid; the CI section highlights all three Workflow YAMLs (prereqs + mirror + install) plus the Argo Workflows UI screenshots, adds a box on uploading the FAR key + subscription JWT into COS with `roksbnkctl cos object`, and shows the runner served from Harbor. The **PDF build keeps every code/YAML block unbroken across page breaks** (`fvextra` + a smaller monospace so wide file examples don't wrap).
- **The disconnected CLI demo README gained a cut-and-paste "Building the services infrastructure (Harbor + FLP)" section** so an end user can clone, edit `.env`, and stand up Harbor + the FLP (which the CI demo reuses); the CI demo README links to it.

## v1.33.0 — 2026-07-29

### Added

- **Air-gapped node trust for a private registry is now roksbnkctl-native — no hand-rolled DaemonSet.** When `bnk up` pulls images from a self-signed / private registry (a co-located Harbor by private IP), the cluster's worker nodes must trust that registry's CA or CRI-O image pulls fail with `x509: unknown authority`. roksbnkctl now emits the whole trust path itself — a dedicated `roksbnkctl-registry-trust` namespace, a privileged ServiceAccount + SCC binding, the CA as a ConfigMap, and a node-installer DaemonSet that writes `ca.crt` into every node's `/etc/containers/certs.d/<host>/` and `/etc/docker/certs.d/<host>/` — then **gates `bnk up` on that installer reaching 1/1 on every node** before any image pull. The CA is captured automatically from the mirror over TLS during `registry replicate` (or supplied with **`registry replicate --registry-ca <file>`**) and recorded on the mirror record; the DaemonSet's pod template carries a CA-hash annotation so a changed CA **auto-rolls** the installer. The installer image is node-cached (`imagePullPolicy: IfNotPresent`), so the trust step itself needs no egress. This replaces the DaemonSet the disconnected walkthrough used to ship by hand.
- **Book Appendix A — "Disconnected ROKS cluster".** An end-to-end walkthrough for deploying BNK air-gapped onto an **existing** ROKS cluster over an **existing** Transit Gateway: mirror FAR into a private Harbor, stand up a standalone F5 License Proxy, and install BNK with images from the mirror and licensing via the proxy — with the CLI commands matched 1:1 to the reproducible `scripts/demos/` walkthrough.

### Fixed

- **`registry replicate` captures the mirror's CA from the served chain, not a verified dial.** The capture decided "is this a private CA the nodes must trust?" from a verified TLS handshake — which succeeds on the operator host whenever the mirror's CA is already in the local trust store (e.g. after `update-ca-certificates`), returning "nothing to install" even though the air-gapped cluster nodes do **not** trust it. It now keys on whether the served chain's top certificate is **self-signed** (the signature of a private CA / co-located Harbor), matching what the nodes actually see.
- **The whole operator side now trusts a private mirror's CA — not only the cluster nodes — so the disconnected install runs end-to-end from a container/CI operator.** The captured (or `--registry-ca`) CA taught the cluster *nodes* to trust the mirror, but every roksbnkctl operation that itself contacts the mirror still relied on the host OS trust store. That was invisible when the operator ran on the Harbor VSI (its CA was in system trust), but the `roksbnkctl-tools-runner` **container** has none, so each step failed `x509: certificate signed by unknown authority`. Fixed across the operator's paths, keyed on the same captured/recorded CA:
  - **`registry replicate`** resolves the CA **before** the copy and trusts it for the push — a custom `RootCAs` pool (system roots + mirror CA) on the crane transport for image/chart copies, and `--ca-file` on the one classic-Helm chart's `helm registry login` / `helm push`.
  - **`registry verify`** trusts the CA for its crane digest HEAD checks the same way.
  - **`bnk up`** exports an operator CA bundle (system roots + the recorded mirror CA) via `SSL_CERT_FILE` before running terraform, so the **terraform helm provider** — which pulls each chart from the mirror as a plugin subprocess that inherits the env — trusts it too.

  Public targets (whose chain is covered by the default roots) are unaffected. This is the operator-side complement to the node CA-trust installer: nodes get the CA via the DaemonSet, the operator gets it here.

### Removed

- **The legacy `bnk_cr_mode = "legacy_curl"` BNK install path and the `--legacy-bnk` flag are gone.** The terraform-native path (`helm_release` `wait = true` + alekc/kubectl `kubectl_manifest` with real `wait_for` readiness gates) has been the default and is now the only path. Removed: the `--legacy-bnk` flag on `bnk up` / `bnk down`, the `bnk.cr_mode` workspace-config key, the `bnk_cr_mode` terraform variable at every module level, and every `null_resource` / `curl` / `time_sleep` block that implemented the curl Server-Side-Apply baseline across the `cert_manager`, `cne_instance`, `license` and `flo` modules. No action is required — configs that never set `cr_mode` / `bnk_cr_mode` render byte-identically.

## v1.32.0 — 2026-07-28

### Added

- **`bnk.flp.vsi.floating_ip` — operator floating IP for the standalone FLP appliance (default on).** The FLP VSI attached no floating IP, so remote `roksbnkctl flp status` and the `:80` web UI were only reachable from inside the VPC. A new `bnk.flp.vsi.floating_ip` (default `true`) attaches an operator floating IP purely as a **management path** — it is added to the leaf-cert SAN so `:8443` and the web UI are valid over it, and recorded in `flp-outputs.json` so **`flp status` prefers it** (reachable from a machine outside the VPC). It is **not** the CWC endpoint — the consuming cluster still reaches the proxy privately over the VPC / Transit Gateway. `roksbnkctl init` prompts for it.
- **Per-plane FLP security-group CIDRs — `bnk.flp.vsi.management_allowed_cidrs` + `licensing_allowed_cidrs`.** With the floating IP now on by default, a single open CIDR list would have published the licensing proxy to the Internet. The FLP VSI's ingress is now split by plane, each with a safe default: **`management_allowed_cidrs`** gates the `:80` flp-status web UI (read-only status — defaults to **`0.0.0.0/0`**, open); **`licensing_allowed_cidrs`** gates the `:8443` proxy and `:22` SSH (trusted access — defaults to the **RFC-1918** private ranges, since the cluster reaches the proxy privately over the VPC / Transit Gateway). The legacy `bnk.flp.vsi.allowed_cidrs` is deprecated but still honored — when set it seeds both planes.
- **flp-status turnkey deployment wiring (completes the v1.31.0 service).** `bnk.flp.vsi.status_image` (+ `status_registry_host` / `status_registry_ca_b64` for a self-signed mirror) runs the flp-status web UI as a container in the FLP podman pod on the VSI, published on `:80`; cloud-init trusts the mirror's CA so the image pulls from an air-gapped Harbor by private IP. The image builds from `cmd/flp-status/Dockerfile` (a **static** `CGO_ENABLED=0` binary — a dynamically-linked build crashes on the musl base image) and mirrors into the disconnected supply chain; an in-cluster `Deployment` + NodePort manifest ships under `deploy/flp-status/`. Validated end-to-end on a live air-gapped VSI: pulled from the private-IP mirror, all four dependent services + listener + TEEM reported, CNEInstance fields (including the root CA) surfaced, `roksbnkctl flp status` rendered it with the web-UI link.

### Fixed

- **`bnk up` converges the F5SPKVlan CRs in a single pass (no more "run twice").** The declarative `external-vlan` / `internal-vlan` (`F5SPKVlan`) CRs are admitted by the `f5validate` webhook, whose TLS server (in `f5-cne-controller`) comes up a few seconds **after** `CNEControllerAvailable=True` — a real apply in that gap failed `http: server gave HTTP response to HTTPS client`, which is why the VLANs (and thus `bnk up`) historically needed a second pass. A `validation_webhook_ready` gate now probes the webhook with a **server-side dry-run apply** (routes through admission, `sideEffects: None`, persists nothing) and retries until it is accepted, so the VLAN applies land first time. The probe targets the correct REST plural **`f5-spk-vlans`** (CRD `f5-spk-vlans.k8s.f5net.com`) — the resource path the CNE reconcile's crd-installer establishes — and tolerates the CRD not existing yet (early 404s) as well as the webhook-TLS race. Mirrors the License CR's existing admission-retry so both consumers of the webhook are consistent.

## v1.31.0 — 2026-07-28

### Added

- **F5 License Proxy status service + `roksbnkctl flp status`.** A new `flp-status` web service reports the live state of an FLP appliance: a status indicator for **every dependent service** (postgresql, vault, vault-init, f5-license-proxy), the `:8443` listener, and the F5/TEEM connection — plus the CNEInstance CR fields (endpoint + root CA, ready to paste into `bnk.flp.external`) and a **live `f5-license-proxy` log stream** (SSE). It serves a **mobile-first, self-contained page on plain HTTP with NO authentication** — the FLP is a private endpoint and the page is read-only. The **same binary runs for either deployment type** (a container in the podman pod on a standalone VSI, or a Deployment in a ROKS cluster), auto-selecting its data source (the podman socket vs. the Kubernetes API) or honoring `FLP_BACKEND`. **`roksbnkctl flp status`** renders the same information in the terminal — including the browsable web-UI link — deriving the service URL from the workspace's `flp-outputs.json` (or `--url`), with `-o json` for scripting.

  This release ships the service binary (`flp-status`) + the CLI, both validated live against a running proxy. The turnkey deployment wiring — building + mirroring the `flp-status` image into the air-gap supply chain, running it in the FLP podman pod (`--publish 80`) on the VSI, and as an in-cluster NodePort Deployment (reusing the `flp_node_port_access` pattern, with a ServiceAccount/RBAC for pods + logs) — is the next increment.

## v1.30.0 — 2026-07-28

### Added

- **`bnk.flp.vsi.ssh_key` — operator SSH access to the standalone FLP appliance.** The FLP VSI attached no SSH key, so the licensing appliance could not be inspected or recovered into (a real operational hole). A new `bnk.flp.vsi.ssh_key` names an existing IBM Cloud VPC SSH key to attach, and a scoped port-22 security-group rule (limited to `allowed_cidrs`) is opened **only** when a key is set.
- **`roksbnkctl init` — a full F5 License Proxy interview.** Interactive init now asks whether to license via an FLP and, if so, whether to deploy it **in-cluster** (helm) or as a **standalone VSI appliance**. For the VSI it collects the region, lets you **pick an existing VPC or create a new one** (a new `CreateVPC` client call), the zone, and an **SSH key** (generate + upload, or reuse an existing one). For in-cluster it asks whether to use this workspace's cluster or **pick + adopt a running ROKS cluster** to license.

### Fixed

- **A genuinely air-gapped (`public_gateway: false`) deployment now works end-to-end** — validated by building a real disconnected cluster (us-south) whose entire BNK supply chain is served privately from a services VPC (us-east) over a Transit Gateway, with roksbnkctl driven from the Harbor VSI itself. Several gaps were closed:
  - **Standalone FLP VSI + cluster-less modules.** `flp up` refused a cluster-less VSI (a CLI precondition), and the standalone FLP phase ran the full BNK root where `cert_manager`/`flo`/`cne_instance`/`license`/`testing` resolved cluster data sources against a nonexistent cluster. A new `cluster_absent` gate skips every cluster lookup + kube provider + the adopt data source when there is no cluster (default `false`; every existing path is unchanged, the `[0]`-indexed references resolve identically when a cluster is present).
  - **FLP VSI local supply chain.** The FLP VSI forced its FAR auth + subscription JWT from COS; it now honors local files (`use_cos_bucket=false`), matching the cluster path.
  - **flo chart versions on the mirror path.** `flo` discarded the chart versions it resolved from the manifest unless `use_cos_bucket` was true, emitting an empty `--version` (and a mangled `pull-chart`) on the disconnected mirror path. Versions now resolve whenever the mirror is used.
  - **`zone_worker_map` duplicate key.** With `workers_per_zone > 1` (all workers in a zone share a subnet) the zone→worker-IP map collided on the subnet key and failed the apply. Grouped by subnet with one representative IP per zone.
  - **`registry_cos: { create: true }` is required** for a ROKS-on-VPC cluster — its internal image registry needs a backing COS instance (provisioning error `E7278`) even in a disconnected deployment. Documented.
- **The standalone FLP VSI survives reboots.** The appliance relied on `podman-restart.service`, which Ubuntu's podman package does **not** ship, so a host reboot left the pod's infra container (and its `:8443` publish) dead. `flp-pod-up.sh` now generates + enables systemd pod units (`podman generate systemd --new`) so systemd rebuilds the whole pod on boot with its data volumes (Vault/postgres) intact, and a conservative `flp-health.timer` — which acts only **after** the proxy has served at least once, and only issues a single gentle `systemctl restart` (never a destructive re-stage) — self-heals residual failures without ever looping or interrupting the ~3–5 min initial bring-up.

### Documentation

- **Appendix A rewritten as a validated air-gap runbook.** Co-located operator on the Harbor VSI (Harbor addressed by its **private IP everywhere** — no split-horizon DNS, no jumphost); a reachability diagram showing **only the Harbor + FLP VSIs reach the Internet** (FAR pulls + F5 TEEM telemetry); the ROKS-specific **CA-trust DaemonSet** (OpenShift `image.config` is HostedCluster-managed and blocked, so drop the CA into each node's `certs.d` via a privileged DaemonSet using a **node-cached** installer image); the private **CSE-range routing** (`161.26.0.0/16` + `166.8.0.0/14` — a no-egress ROKS cluster reaches private ICR/IAM/COS/master without operator-built VPEs); and the `bnk up` convergence re-run.

## v1.29.0 — 2026-07-27

### Added

- **`cluster.public_gateway` — build a private, disconnected cluster with no worker egress.** ROKS clusters were always created with a public gateway on every subnet (workers had Internet egress). A new `cluster.public_gateway` config toggle (rendered as the `cluster_public_gateway` terraform variable) defaults to `true` (unchanged behavior); set it to `false` and no `ibm_is_public_gateway` is created and no subnet attaches one — a genuinely private cluster. `roksbnkctl init` prompts for it ("Attach public gateways for worker Internet egress?") and warns when you choose private. **Expert topology:** a no-egress cluster needs private connectivity you provide (VPEs / private service endpoints for IBM Cloud services, plus a privately-reachable mirror registry); the toggle removes the egress path but does not build those paths, and the cluster master keeps its public API endpoint. See the new Appendix A.

- **Standalone `flp up` (VSI mode) — an F5 License Proxy appliance with no cluster.** `flp up` in VSI mode previously required a `cluster-outputs.json` and deployed the proxy into the cluster's own VPC. A new `bnk.flp.vsi.vpc` config field names an existing VPC to deploy the FLP VSI into **without any cluster** — a licensing appliance you can place in a services VPC that has controlled egress to F5, which a disconnected cluster in another VPC then reaches over a Transit Gateway (via `bnk.flp.external`). The cluster-adopt terraform lookup is now gated on a non-empty cluster name (with `try()`-guarded outputs) so a cluster-less apply doesn't fail resolving a cluster named `""`; normal adopt is unchanged. This is the piece that makes a clean "services-VPC" disconnected topology native — see Appendix A and Chapter 10c.

### Fixed

- **`registry replicate` is documented truthfully — it needs no cluster.** The help text said the mirror verbs "need a live cluster." They don't: `replicate` is a purely host-side, registry-to-registry copy (go-containerregistry) that pulls from the FAR source and pushes to the target from wherever roksbnkctl runs. The help now states the real requirements (a configured `registry:` block, the FAR source credential, and host reachability to both endpoints) and that the mirror can be pre-seeded as a standalone supply-chain step before any cluster exists.

### Documentation

- **New Appendix A — "A disconnected ROKS cluster"**: a manual, end-to-end runbook for the services-VPC topology (Harbor mirror + standalone FLP appliance in a VPC with egress; a `public_gateway: false` cluster in another VPC with none; joined by a Transit Gateway), mirroring the `disconnected_deployment_demo.sh` script.
- Chapter 10a documents standalone `registry replicate` and adds a "truly disconnected cluster" section; Chapter 10c adds "Running the FLP as a VSI" + the standalone-appliance flow; the config/tfvars references (Ch. 12/13/28) cover `cluster.public_gateway`, `cluster_public_gateway`, and `bnk.flp.vsi.vpc`.

## v1.28.0 — 2026-07-27

### Added

- **Customize BNK data-plane networking from `config.yaml` and the `init` interview.** The per-availability-zone subnet CIDRs and TMM self-IPs — `ext_vlan_cidr`, `int_vlan_cidr`, `int_snat_cidr`, `int_vip_cidr`, `external_selfip`, `internal_selfip` — plus the two network-wide TMM knobs (`vlan_prefixlen`, `tmm_k8s_routes`) are now first-class `bnk.network` config, and `roksbnkctl init` prompts for all of them (opt in at *"Customize BNK networking?"*, seeded with the install-guide defaults so you edit only what your fabric differs on; re-init pre-fills from the saved config). Supplying zones replaces the install-guide defaults entirely; they render `cneinstance_network_zones` (driving the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs). `vlan_prefixlen` (F5SPKVlan `spec.prefixlen_v4` — the self-IP subnet mask) and `tmm_k8s_routes` (the pod CIDR TMM routes to, `TMM_K8S_ROUTES`) were previously reachable only by forking the embedded Terraform; they are now plumbed root → `cne_instance` → `cneinstance` and settable via config or a `--var-file` (`cneinstance_vlan_prefixlen` / `cneinstance_tmm_k8s_routes`). An unset `bnk.network` is byte-identical to before — the module's defaults stand.

- **`plan --out <file>` / `apply --plan <file>` — review a plan, then apply exactly it.** `plan` and `apply` were already separate commands, but `apply` re-planned and applied fresh, so the applied change wasn't guaranteed to equal the reviewed one. `roksbnkctl plan --out <file>` now saves a binary Terraform plan file (plus a human-readable `<file>.txt` — so the full diff lands in a file instead of scrolling off the terminal), and `roksbnkctl apply --plan <file>` applies that saved plan **verbatim** — no re-plan, no var-files (the plan captured them). If state or config drifted since the plan was saved, Terraform refuses the stale plan rather than applying something un-reviewed — the change-control guarantee. The gateway-api admission-policy sweep still runs on the plan-apply path. Requires the local backend (a docker/remote backend errors clearly).

### Fixed

- **`staticcheck` is clean again (CI's lint job goes green).** A pre-existing ST1005 in `cluster_vpc_guard.go` (a multi-line actionable operator message ending in a period) is now explicitly suppressed with a documented `//lint:ignore`, so the CI staticcheck job — red for several releases — passes.

### Documentation

- Book updated for the above: `bnk.network` in the workspace-config and configuration-reference chapters, the new networking Terraform variables in Chapter 13, a "Reviewing a plan before applying" section in Chapter 10 (cross-linked from the three-phase-lifecycle chapter), and an installation-prerequisites rewrite noting the full deploy path now runs on **native Windows without WSL** (validated end-to-end — see v1.27.5–v1.27.7).

## v1.27.7 — 2026-07-27

### Fixed

- **Console output no longer mojibakes on Windows.** roksbnkctl's UTF-8 glyphs (`✓ ⚠ ✗ → ─` in `doctor`, phase progress, separators) rendered as cp1252 garbage (`Γ£ô ΓÜá ΓÇö`) on the Windows console, whose default output code page is the legacy OEM/ANSI page rather than UTF-8. A Windows-only `init` now sets the console output code page to UTF-8 (`SetConsoleOutputCP(65001)`) at startup, so the whole surface displays correctly without changing a single output string. Best-effort (a no-op when stdout is redirected/piped, failures ignored — cosmetics only), stdlib `syscall` (no new dependency), and a strict no-op on Linux/macOS via build tag. A UTF-8 console is a superset of ASCII, so plain output and child processes (terraform/helm) are unaffected.

## v1.27.6 — 2026-07-27

### Fixed

- **The gateway-api admission-policy sweep now targets the deploying cluster by ID, and fails loudly instead of silently sweeping nothing.** The tfx migration (v1.27.0) replaced the FLO crd-installer's Linux-only `nohup bash` delete-loop — which targeted the cluster *directly* via `var.kube_host`/`var.kube_token` — with an in-process Go goroutine that **re-resolved** the cluster from the *BNK* workspace's terraform outputs, falling back to the cluster **name**. Two problems compounded: (a) on a first apply the BNK outputs don't exist yet at sweep-start, forcing the name fallback; and (b) a ROKS cluster name is **not unique** in an account, so with a duplicate-named (or orphaned) cluster present the name resolved to the wrong — even a dead — endpoint. Every delete then silently no-op'd against that cluster (errors were discarded), so the real cluster's `openshift-ingress-operator-gatewayapi-crd-admission` policy was never removed; FLO's `backendtlspolicies` CRD create stayed blocked, and the *only* visible symptom — 15 minutes later — was the CNEInstance never reporting `CNEControllerAvailable`. The sweep now (1) resolves the cluster by **ID** from `cluster-outputs.json` (written by the cluster phase, so it exists before the BNK phase and is immune to a duplicate name), then `roks_cluster_id`, only falling back to a name as a last resort; (2) **warns** when it must resolve by name; (3) logs a delete failure once per resource instead of discarding it; and (4) reports how many deletes actually landed on stop — **zero** now prints a loud red-flag line instead of looking like success. Platform-independent (helps Linux too), but it's what unblocks the CNEInstance on a native-Windows deploy where a stale same-named cluster exists.

## v1.27.5 — 2026-07-27

### Fixed

- **The FLO/CIS/FLP `helm_release` charts install from a locally-staged archive — the helm provider does no OCI login at all (Windows fix, definitive).** v1.27.4's premise was wrong: the terraform helm provider (`hashicorp/helm` 2.x) does **not** read `HELM_REGISTRY_CONFIG` per-resource for an `oci://` `helm_release` — it loads registry config once at provider-init (before any `local_file` writes), and the resource's only auth path is `repository_username`/`repository_password`, which triggers the login-and-**store** that fails on Windows (`The stub received bad data`). Dropping those creds made it fetch an anonymous token → `403 Forbidden`. There is no file the provider re-reads to hit. So the provider is now taken out of the OCI-pull business entirely: a new `tfx helm-value pull-chart` verb stages the chart `.tgz` on disk (authenticating inline via `helm pull --registry-config`, the mechanism that already works on Windows for the manifest/version pulls), and `helm_release.flo` / `.cis` / `.flp` set `chart` to that **local archive path** — no `repository`, no `version`, no `repository_username`/`password`. A local chart path does zero registry auth, so every Windows credential-store and anonymous-token failure disappears at once. Identical on Linux/macOS (same staged-archive install), so the platform-specific `local_file`/`repository_*` branching from v1.27.4 is gone.

## v1.27.4 — 2026-07-25

### Fixed

- **The FLO/FLP `helm_release` OCI pull authenticates inline instead of via the provider's login-and-store (Windows fix, part 3).** v1.27.3's env redirect (`HELM_REGISTRY_CONFIG`/`DOCKER_CONFIG`) did not stop the terraform helm provider's OCI **login** from storing the credential through a docker credential helper — helm's registry client falls back to the docker config for helpers, and the store still failed on the multi-KB FAR password with `The stub received bad data`. On Windows, roksbnkctl now writes the pull credential **inline** into the registry config the provider reads (via a `local_file` resource keyed on the new `helm_registry_config` path roksbnkctl exports), and the `helm_release.flo` / `.cis` / `.flp` resources drop `repository_username`/`repository_password` — so the provider performs **no** OCI login-and-store at all; its chart pull simply reads the inline auth. Confined to Windows (`runtime.GOOS`); Linux/macOS keep the proven `repository_username`/`password` login path unchanged.

## v1.27.3 — 2026-07-24

### Fixed

- **The helm provider's OCI login stores credentials inline, not via a native helper (Windows fix, part 2).** With the tfx helm-value pull fixed in v1.27.2, the FLO/FLP deploy reached the in-process terraform **helm provider**'s `helm_release`, which does its own OCI registry login-and-store. On Windows a `credsStore` in the docker config (Docker Desktop sets `"desktop"` in `~/.docker/config.json`) makes that store shell out to a native credential helper — which fails on the multi-KB FAR `_json_key_base64` password with `error storing credentials … The stub received bad data` (the Windows Credential Manager blob cap), erroring `helm_release.flo`. `prepareToolEnv` now writes the isolated `HELM_REGISTRY_CONFIG` and a redirected `DOCKER_CONFIG` as fresh `{"auths":{}}` files with **no** `credsStore`/`credHelpers`, so the provider's login stores the credential as inline base64 in the file (no helper). Overwritten each run (the login re-populates it) and a no-op on Linux, where the store was already inline. tfx helm-value is unaffected — it passes its own `--registry-config`.

## v1.27.2 — 2026-07-24

### Fixed

- **`tfx helm-value` authenticates the OCI chart pull via a config file, not `helm registry login` (Windows Credential Manager fix).** The helm-value verbs (`pull-file`, `chart-version` pull mode, `prod-jwks`) ran `helm registry login <host> --password <far-sa>` before pulling. On Windows `helm registry login` stores the credential in the Windows Credential Manager, whose credential blob is capped at ~2.5 KB — the FAR `_json_key_base64` service-account password is a multi-KB base64 blob, so the store failed with `Error: The stub received bad data` and the FLO/FLP version-resolution provisioners errored at apply time. tfx now writes a temporary docker-style registry-config file with the auth and passes `helm pull --registry-config <file>` instead — no credential store, no size cap, the password never touches the command line (so helm's insecure-`--password` warning is gone too), and the behaviour is identical on Linux. The temp file lives in the same scratch dir the pull cleans up.

## v1.27.1 — 2026-07-24

### Fixed

- **`tfx` deploy phases run on native Windows (`cmd.exe` quoting fix).** The v1.27.0 tfx `local-exec` commands wrapped the binary path in escaped quotes (`"\"${roksbnkctl}\" tfx …"`). That parses fine under `/bin/sh` on WSL/Linux, but on Windows terraform runs `local-exec` via `cmd /C <command>` and Go's arg-escaping turned the inner quotes into `\"`, so `cmd.exe` tried to execute a program literally named `\"C:\…\roksbnkctl.exe\"` and failed with *"is not recognized as an internal or external command"* — breaking every FAR/FLO/FLP/license/cne provisioner at apply time. (The verbs themselves were fine; only the terraform-to-`cmd.exe` handoff was — and it was never exercised on Windows before, since the tfx validation harness invoked the verbs directly rather than through a `local-exec`.) All 17 tfx `local-exec` command strings now pass the binary unquoted, which `cmd.exe` and `/bin/sh` both execute correctly (the Windows install dir and the tfx arguments contain no spaces). The `data.external` programs were already argv lists and were never affected.

## v1.27.0 — 2026-07-24

### Added

- **The FAR + FLO/FLP deploy phases now run on native Windows — no WSL.** The last Windows-blocking shell glue in the default (non-legacy) deploy path is gone: every `curl` / `tar` / `grep` / `awk` / `helm pull` `local-exec` in the `flo`, `flp`, and `flp_vsi` modules is replaced by a native `roksbnkctl tfx <verb>` the terraform provisioner execs directly (no `interpreter`, so `cmd.exe` runs `roksbnkctl.exe`). New internal verbs back this:
  - **`tfx cos-get`** — downloads the FAR-auth tarball via the COS SDK (was `curl` + a hand-rolled IAM bearer-token exchange).
  - **`tfx far-extract`** — writes the FAR service-account JSON via a Go tar-extract to a fixed path (was `tar -xzf … | grep '\.json$'`).
  - **`tfx helm-value`** — `chart-version` resolves a sub-chart version out of the pulled BNK manifest (helm binary for the OCI pull, Go for the extract), `pull-file` extracts a bundled file, `prod-jwks` extracts + base64-decodes the license-proxy keyset, and `--manifest-file` reads a version from an already-pulled manifest so one pull feeds several reads. In-chart file lookup walks the untarred tree tolerantly, so helm `--untar`'s top-dir naming doesn't matter.
  - **`tfx read-json`** — emits `data.external` JSON from a file, with a repeatable `--pair key=file` for multi-key outputs and a missing-file→empty tolerance so a fresh-container destroy refresh stays well-formed.
  - Rounding out the surface added earlier in this cycle: **`tfx apply`** (server-side apply from a manifest stream), **`tfx wait`** (client-go watch + poll), **`tfx patch`** (strategic/merge/json/apply, `--patch-b64`), and **`tfx delete`** (`--ignore-not-found`). The cne_instance admission-policy delete loop moved from a detached `nohup bash` into an in-process Go goroutine (identical on Windows and Linux).

- **Quota preflight for the VPC-per-region and TGW-per-account walls.** `doctor` now reports VPCs-in-region and account transit-gateway counts against their limits (warns at the wall), and the `init` interview surfaces the same headroom before you commit to a create — so an apply fails fast with a clear message instead of deep in a `terraform apply`.

### Changed

- **Terraform error output is summarized and de-duplicated.** A failed plan/apply/destroy now collapses the repeated per-resource IBM `Error: --- summary: ---` blocks into a single deduplicated diagnostic (`x3 …`) instead of scrolling the same error once per affected resource.

- **One shared IAM authenticator instead of a token exchange per call.** The IBM client now reuses a single `core.IamAuthenticator` (and one injectable `http.Client`) across every raw-REST helper, rather than exchanging the API key for a fresh bearer token on each request.

### Fixed

- **Idempotent Transit Gateway attach when a shared VPC is already on the target gateway.** An IBM VPC holds exactly one Transit Gateway attachment, so when a second cluster is created in a first cluster's VPC (the shared-VPC path) and both point at the same gateway, the second `up`'s TGW-connect phase hit `the requested network is already connected to an existing transit gateway` and surfaced it as an error (non-fatal — the cluster/BNK still deployed — but `tgw status` then reported "not connected" even though the shared VPC *was* attached via the first cluster's connection). The connect phase now pre-checks: if this workspace's cluster VPC is already attached to the target gateway, it records the live connection and returns success, skipping the apply. The normal first-cluster path (VPC not yet attached) is unchanged, and a genuine conflict (VPC on a *different* gateway) still surfaces loudly. This is the attach-side analogue of the tolerant-detach `down` already does for shared infra.

## v1.26.1 — 2026-07-23

### Fixed

- **Windows `install` now lands on a directory that is actually on `%PATH%`.** `roksbnkctl install` (and the `install.ps1` one-liner) defaulted to the Unix `~/.local/bin`, which is never on the Windows PATH — so the copy succeeded but `roksbnkctl` didn't resolve, and the follow-up hint was Unix `export PATH` / `.bashrc` advice. On Windows it now installs into a writable directory already on `%PATH%` — preferring `%LOCALAPPDATA%\Microsoft\WindowsApps` (on the per-user PATH by default, no admin), so the binary resolves immediately in the same session — and the PATH hint (for the rare fallback dir) is now the PowerShell `SetEnvironmentVariable(...,'User')` one-liner. `isOnPATH` is also case-insensitive on Windows.

- **Windows `uninstall` no longer refuses when removing the running binary.** Windows can't delete a running `.exe`, so `uninstall` used to error and tell you to delete it by hand. It now moves the running binary aside to `<name>.old` (renaming a running exe *is* allowed on Windows), freeing its install path; the `.old` remnant unlocks once the process exits. Unix behaviour (unlink the running file) is unchanged.

- **Shared-VPC teardown is safe now (guardrail + tolerant detach).** When multiple clusters share one VPC (the `resources.cluster_vpc` adopt-existing feature), the cluster that **created** the VPC owns its per-zone public gateways and must be destroyed **last**. Two fixes make that correct:
  - **Guardrail:** `cluster down` / `down` now refuses to destroy a workspace that created the VPC while the VPC still holds **another cluster's subnets**, naming them and telling you to tear the sharers down first — instead of failing mid-destroy with `The VPC is in use` after already deleting the shared gateways. (A correctness check; `--auto` does not bypass it. Best-effort — a discovery-API hiccup warns and proceeds.)
  - **Tolerant detach:** cluster subnets now carry their public gateway **inline** (`ibm_is_subnet.public_gateway`) instead of via a separate `ibm_is_subnet_public_gateway_attachment`. Deleting the subnet removes the association implicitly, so an adopter's destroy no longer fails with `UnsetSubnetPublicGateway ... the specified subnet has no public gateway` when the owner's gateway was already gone.

## v1.26.0 — 2026-07-23

### Added

- **`init` discovers an existing VPC for the new cluster to build into.**
 In the interactive interview, when creating a new ROKS cluster, declining "Create a new cluster VPC?" now lists the account's existing VPCs **in the chosen region** (name + status) and lets you pick one by number — mirroring the transit-gateway discovery. Picking one records it as `resources.cluster_vpc { create: false, existing: <vpc-id> }`, which renders `use_existing_cluster_vpc` + `existing_cluster_vpc_id`, so the cluster's subnets (and reused per-zone public gateways) land in the adopted VPC. This lets **multiple workspaces put different clusters in the same VPC**. Picking `0` (or if the region has none) falls back to creating a new VPC, since a cluster must have one. Falls back to a free-text VPC-id prompt if the listing call fails. (The Terraform, config model, and tfvars rendering for BYO cluster VPC already existed — this exposes it in the interview instead of requiring a hand-edited `config.yaml`.)

### Changed

- **`roksbnkctl down` auto-detaches an existing Transit Gateway before tearing down the cluster.** When a workspace **created** its VPC but attached it to an **existing** (shared) Transit Gateway — via `tgw connect` or the init interview — that connection lives in its own phase (`state-tgw/`) the composite `down` previously ignored. Because the connection pins the cluster VPC's CRN, the cluster-phase VPC delete would fail (`VPC still has an attached transit gateway connection`) until you manually ran `tgw disconnect`. `down` now detects the connection (`Presence.TGW`) and disconnects it automatically — **after** the BNK/Testing teardown and **before** the cluster — removing only this cluster's connection (the gateway and every other cluster's connection stay intact). A detach, never a delete of the shared gateway. Unlike the Gateway/FLP phases (guarded with "run `X down` first" because their teardown has cluster-namespace finalizer ordering), a TGW connection is a pure IBM resource with deterministic ordering, so automating it is safe.

### Fixed

- **Removed the deprecated `tcp {}` block from the FLP-VSI security group rule.** `modules/flp_vsi`'s inbound-8443 rule used the nested `tcp { port_min port_max }` form, which the IBM provider now warns is deprecated (`tcp is deprecated, use 'protocol', 'code', and 'type' instead`). Rewrote it to the flat `protocol = "tcp"` / `port_min` / `port_max` form already used in `modules/flp` and `modules/testing` — same rule, no more warning on every plan/apply/destroy.

## v1.25.0 — 2026-07-23

### Added

- **`init` discovers existing transit gateways to attach.** When the interactive interview's "Create Transit Gateway?" is declined, `init` now lists the account's existing transit gateways (name, location, status) and lets you pick one by number to attach the cluster to — instead of typing a name/ID from memory. Picking `0` (or none present) leaves the cluster unattached to connect later with `tgw connect`. Falls back to the free-text name/ID prompt if the listing call fails.

- **Ctrl-C exits the interactive `init` cleanly.** From the point the partial workspace is persisted, `SIGINT` prints `^C interrupted — workspace "X" is saved. Re-run roksbnkctl init -w X to finish it.` and exits (130), leaving the saved (partial) workspace intact to resume — rather than dropping into a half-answered interview or a defaulted config. A dedicated handler, so it fires only on a real Ctrl-C, not the normal end-of-run.

## v1.24.1 — 2026-07-23

### Fixed

- **`init` provisions a globally-unique COS bucket (was `BucketAlreadyExists`).** COS bucket names share ONE global namespace (like S3), so provisioning the generic default `bnk-artifacts` failed with `BucketAlreadyExists: Container bnk-artifacts exists with a different storage location than requested` whenever another account already owned that name. `init` now provisions under an account-scoped name, `bnk-artifacts-<first-12-of-account-id>`, which is globally unique. An explicitly-configured `cos.bucket` is still used as-is.

  The name is **deterministic per account and discoverable**: `init`'s supply-chain check looks for both the plain default and the account-suffixed name, so a **second workspace run from the same account's API key finds and reuses the bucket the first one provisioned** (and records the resolved coordinates on the workspace so the BNK phase pulls from exactly that bucket) — no duplicate bucket, no re-upload. An interrupted run that already created the bucket is reused rather than re-created.

## v1.24.0 — 2026-07-23

### Added

- **One-line installers for Linux/macOS and Windows.** `install.sh` (`curl -fsSL https://raw.githubusercontent.com/jgruberf5/roksbnkctl/main/install.sh | sh`) and `install.ps1` (`irm …/install.ps1 | iex`) download a release archive for the host OS/arch, verify its checksum, extract the binary, hand off to the binary's own `roksbnkctl install` (which copies it onto `PATH`, replacing any existing copy), then delete the archive — leaving only the installed binary. `VERSION=vX.Y.Z` pins a release; `ROKSBNKCTL_INSTALL_ARGS` passes flags to `install`.
- **`roksbnkctl uninstall`** — removes the installed binary from `~/.local/bin` (or `--dir`), the opposite of `roksbnkctl install`. On Windows it refuses to delete the currently-running `.exe`.
- **`roksbnkctl upgrade` interactive version picker** — with no `--version` on a terminal it now lists the releases newer than the running binary and lets you pick one (a `dev` build is offered every release). `--version vX.Y.Z` still pins a specific release; `--yes` still takes the latest non-interactively.
- **Local-file FAR supply chain (no COS) — `bnk.far_auth_local_file` + `bnk.subscription_jwt_local_file`.** The BNK phase can now read the FAR auth tarball and the subscription JWT from **local files** instead of an IBM COS bucket. When both are set, roksbnkctl reads them at render time — extracting the FAR `_json_key_base64` service account from the tarball **in Go** (no `curl`/`tar`/`grep`) — and injects the content as `far_service_account_b64` / `f5_cne_subscription_jwt`, setting `use_cos_bucket = false` so the `flo` and `license` modules skip the COS download entirely. `init` sets these automatically (see below); they can also be set by hand. When unset, the COS path is unchanged.

### Changed

- **`init` checks COS, then falls back to local files.** The interactive supply-chain check now treats any COS error (transport/DNS/auth) as **non-fatal**: instead of aborting, it offers to use local files for the FAR tarball + JWT and records them on the workspace (`bnk.*_local_file`). When COS is reachable but an artefact is missing, it prefers local files (no COS) but still offers to provision + upload. This unblocks accounts whose COS is unreachable.

### Removed

- **IBM Cloud Schematics deployment path (~5,300 lines).** The Terraform modules were originally authored as standalone IBM Schematics workspaces; roksbnkctl is now the sole driver, so the Schematics tooling is dead weight. Removed the five `terraform/modules/*/schematics_runner.py` lifecycle scripts, the "Deploying with IBM Schematics" sections + `ibmcloud_schematics_*` workspace naming in the module READMEs, and the Schematics-lifecycle `.gitignore` entries; the remaining `local-exec` rationale comments that cited "Schematics compatibility" now cite lean runtimes (the tools-runner) generally. No functional change to any deployment — nothing in the Go or `.tf` code referenced Schematics. (This also deletes the last `python3` scripts in the tree; the two remaining `python3` uses are the deterministic-poll `null_resource`s, which are being converted to `tfx` — see `docs/prd/native-windows-tfx.md`.)

### Fixed

- **`roksbnkctl version` shows the release as `vX.Y.Z`.** Release binaries are now stamped `v{{.Version}}` (goreleaser), so `version` prints `v1.23.2` rather than `1.23.2` — and it matches the `v`-prefixed tag the bundled tool images are published under (previously `Version` was `1.23.2` while the images were `:v1.23.2`, so a release binary's `ops`/docker-backend pulls could miss). Local `make build` derives the version from `git describe` (e.g. `v1.23.1-3-g<sha>-dirty`), so a locally-built binary is clearly a build, not a release.

- **`init` no longer leaves NO workspace when it fails partway.** Previously the only `SaveWorkspace` was at the very end of the interview, so any earlier failure (cluster listing, the COS supply-chain check) aborted with **nothing persisted** — and manual commands like `roksbnkctl cos …` then had no workspace to resolve. `init` now persists a **partial workspace** (region + a resolvable API key) as soon as credentials verify, before the fallible steps. A failed init leaves a usable workspace, and re-running `init` completes it.

- **`roksbnkctl init -w <existing-workspace>` resumes to complete/update the workspace** rather than only offering a destructive "Overwrite config? → abort". The prompt is reframed to "Continue and update this workspace?" (default yes) and the interview pre-fills from the saved config — the intended way to finish a partial workspace left by a failed first init.

- **Orchestration-COS region no longer defaults to the cluster region.** `init`'s supply-chain check derived the COS S3 endpoint region from `ibmcloud.region`; a VPC-only region like `eu-fr2` has **no COS S3 endpoint**, so the check failed with `dial tcp: lookup s3.eu-fr2.cloud-object-storage.appdomain.cloud: no such host`. It now defaults to `DefaultCOSRegion` (`us-south`), overridden only by an explicit `cos.region`.

## v1.23.1 — 2026-07-23

### Fixed

- **`f5licenseproxy` licensing no longer deadlocks — BNK now licenses through the FLP reliably.** On a from-scratch `bnk up` in `f5licenseproxy` mode, the apply would hang indefinitely (observed 3+ hours) and the CNEInstance never licensed: the CWC polled the proxy forever with an empty entitlement (`GetBackLater`). Root cause: the CNEInstance `kubectl_manifest` gated on the `alekc/kubectl` provider's `wait_for { condition = CNEControllerAvailable }`, which did not clear even after that condition was `True` (it hung past its own timeout). Because the License CR — and the FLP CA Secret + CWC restart it needs — is gated on the CNEInstance's ready-id, none of the FLP wiring ever applied, so the CWC licensed against nothing. The two fragile provider `wait_for`s (CNEInstance `CNEControllerAvailable`, License `LicenseActive`) are replaced with **deterministic Kubernetes-API polls** (`null_resource` running `curl` + coreutils `grep`/`tr` — **no `python3`, no `jq`**, so it runs in the tools-runner container where python is absent): each clears as soon as its condition is met (seconds), is bounded (~15 min), and **fails loudly instead of hanging**. The change is mode-agnostic — `connected`/`disconnected` (direct licensing, no FLP) get the same deterministic gating with no FLP Secret or CWC rollout written. Validated across repeated from-scratch cycles: each licensed through the FLP in 2–5 minutes with zero hangs.

- **FLP VSI reuses the cluster VPC's existing zone gateway instead of failing the per-zone quota.** With `bnk.flp.mode: vsi`, `flp up` into an existing cluster VPC failed with `CreatePublicGatewayWithContext failed: … over quota. Quota: 1` — the module always created its own public gateway, but IBM Cloud allows exactly one public gateway per zone per VPC and the cluster phase already attached one to every zone. The `flp_vsi` module now looks the VPC's gateways up and **reuses** the one already in the FLP VSI's zone, creating its own only when the zone has none (the same reuse pattern the cluster module uses).

## v1.23.0 — 2026-07-22

### Added

- **Run the F5 License Proxy on a standalone VSI instead of a Helm install — `bnk.flp.mode: vsi`.** The FLP phase gains a second deployment backend: rather than the `f5-license-proxy` Helm chart into the ROKS cluster, `mode: vsi` provisions a headless Ubuntu VSI in the cluster VPC that runs the same four containers (f5-license-proxy + postgresql + vault + vault-init) as a **podman pod** — no Kubernetes. Terraform generates the mTLS CA and injects it via cloud-init; the box signs the leaves and brings the pod up on 8443; the FAR pull key, subscription JWT, image tags, and F5 public JWKS are resolved from the same COS + BNK manifest the Helm path uses. It terminates in the **same** `flp-outputs.json` (endpoint + root CA) the Helm path produces, so `bnk up` in `f5licenseproxy` mode consumes the handoff **unchanged** — the BNK side is untouched.

  Config lives under `bnk.flp.vsi` (`profile` — default `bx2-4x16`; `zone`; `boot_size_gb`; `reach` — `private` [default]; `allowed_cidrs`; optional `forward_proxy`). The CWC reaches the proxy over the VPC / a transit gateway (private reach). See [Licensing BNK with the F5 License Proxy](book/src/10c-flp-licensing.md).

## v1.22.0 — 2026-07-22

### Added

- **`init` provisions the FAR supply chain when it's missing.** The interactive `roksbnkctl init` now checks that the orchestration COS actually holds what the BNK phase needs — the FAR auth tarball and the subscription JWT — and, when the instance, bucket, or either object is absent, offers to create them from local files: it provisions the COS instance and bucket if needed and uploads the two artefacts, then records the coordinates under `cos:` so the BNK phase and `registry` resolve from exactly what was created. Declining is non-fatal (it prints a warning and continues). Interactive path only — `--config-file` / `--non-interactive` are unchanged (they assume COS is already populated, as in CI).

### Changed

- **Generic default COS/FAR resource names.** The orchestration COS defaults, which previously named a specific test environment, are now generic: instance `bnk-supply-chain` (was `bnk-orchestration`), bucket `bnk-artifacts` (was `bnk-schematics-resources`), and the default subscription JWT `subscription.jwt` (was `trial.jwt`). The FAR auth tarball default (`f5-far-auth-key.tgz`) is unchanged. These are the fallbacks when the `cos:` block and `bnk.subscription_jwt_file` are unset, and they're centralized in `internal/config` so the Terraform render, the `registry` resolver, and the new init provisioning share one source of truth. **A workspace that relied on the old defaults** (no `cos:` block) should either set `cos: {instance: bnk-orchestration, bucket: bnk-schematics-resources}` + `bnk.subscription_jwt_file: trial.jwt` to keep the old target, or let the new `init` flow provision the new-named resources.

## v1.21.0 — 2026-07-22

### Added

- **`roksbnkctl upgrade` — self-upgrade the binary, on Linux, macOS, and Windows.** `roksbnkctl upgrade` downloads the latest GitHub release for the host OS/arch, verifies its SHA256 against the release's `checksums.txt`, and replaces the running binary in place. `--version vX.Y.Z` pins a specific release (and may downgrade or reinstall); `--yes` skips the confirmation prompt. Windows is now supported: a running `.exe` can't be overwritten, so the old binary is moved aside to `<binary>.old` (which Windows permits) and the new one takes its place, with the sidecar swept on the next run. `roksbnkctl self update` remains as the latest-only interactive alias.

  Release binaries are not yet code-signed, so on a host with an application-allowlist policy (e.g. Windows Device Guard/WDAC) the freshly downloaded binary may be blocked until its hash is trusted — the command's `--help` says so.

### Fixed

- **`self update` no longer always reported an update was available.** goreleaser stamps the version without a leading `v` (`1.21.0`) while GitHub tag names carry it (`v1.21.0`), so the old equality check never matched a real release. Version comparisons now ignore the `v`.

## v1.20.1 — 2026-07-22

### Fixed

- **`up`/`plan` on a new cluster VPC no longer fails at plan time with "Invalid count argument".** The public-gateway reuse logic (shared-VPC / air-gapped topology) fed the cluster VPC's id into the `ibm_is_public_gateway` `count`, but when the VPC is *created* in the same run that id is unknown until apply, so Terraform could not resolve the count. The lookup is now gated on `use_existing_cluster_vpc`: a freshly created VPC skips it entirely (it cannot already have gateways) and the counts resolve at plan time; an adopted VPC keeps reusing its existing gateways as before.

- **Phase-combination hardening (defense-in-depth).** The cert-manager / FLO / CNE / license / gateway / FLP modules now gate their resources on `!create_roks_cluster` in addition to their `deploy_*` flag — matching the guard already on their provider + cluster-config data sources. In every correct phase these modules already run with `create_roks_cluster = false`, so this changes nothing there; it only turns an accidental phase combination (e.g. a legacy monolithic apply that enables BNK while the cluster is still being created) into a clean no-op instead of a plan-time crash.

- **`roksbnkctl testing up` on a cluster-less workspace now errors instead of provisioning a cluster.** Like the Gateway / FLP / TGW phases, the Testing phase now requires `cluster-outputs.json` and hard-errors with an actionable message when it is missing, rather than silently rendering `create_roks_cluster = true` and planning a whole cluster into `state-testing/`.

### Added

- **More Terraform options are now settable from `config.yaml`.** Surfaced eleven knobs that previously only had their upstream HCL defaults: `cluster.min_worker_vcpu_count` / `min_worker_memory_gb` (worker-flavor auto-select floors), `bnk.flo_namespace` / `flo_utils_namespace` / `gslb_datacenter_name`, `bnk.cert_manager.namespace` / `version`, `bnk.flp.storage_class`, `cos.instance` / `bucket` / `region` (the orchestration COS — honoured by **both** the Terraform render and the `registry` FAR resolver), and `resources.testing_jumphost_profile` / `testing_min_vcpu_count` / `testing_min_memory_gb`. Each renders only when set, so existing configs are unchanged. See [Workspace config](book/src/12-workspace-config.md) and the [Configuration reference](book/src/28-configuration-reference.md).

## v1.20.0 — 2026-07-16

### Added

- **Share a Transit Gateway across clusters — `roksbnkctl tgw connect/disconnect/status`.** Attach a cluster's VPC to an **existing** Transit Gateway, by name or by id, at create time or after, so several clusters can share one gateway. It's its own phase (`state-tgw/`) that reads the cluster VPC from `cluster-outputs.json`, so it works against a cluster roksbnkctl created **or one you registered** — each workspace owns its own connection. Decline to create a gateway in `init` and name an existing one, and `cluster up`/`cluster register` attach it automatically; `tgw connect <name-or-id>` does it afterward. `tgw status` (and `cluster config`) show the gateway id/name and the **live** connection state (attached / pending / detached), queried from IBM. See [Sharing a Transit Gateway across clusters](book/src/09a-transit-gateway-sharing.md).

### Fixed

- **Adopting an existing Transit Gateway now actually connects the cluster VPC.** Previously `create_roks_transit_gateway = false` created no connection at all — the only `ibm_tg_connection` was gated on *creating* the gateway and hard-referenced it, so a cluster pointed at an existing gateway was left unattached. The new `tgw` phase resolves the gateway (name **or** id, via the account's gateway list — an ambiguous name errors instead of picking one) and creates exactly one connection to the cluster VPC.

## v1.19.0 — 2026-07-14

### Added

- **Run the disconnected install from CI, with nothing installed on the runner.** Licensing BNK through an F5 License Proxy in *another* cluster, with every chart and image from a private registry, now works end to end inside the [tools-runner container](book/src/07b-github-actions-ci.md) — no `roksbnkctl`, `terraform`, `helm`, `kubectl` or `ibmcloud` on the runner, and no `config.yaml` templated anywhere. See [Flow C in CI](book/src/10c-flp-licensing.md#flow-c-in-ci--the-runner-container-no-host-install).

  Six new environment overrides make a pipeline able to describe the whole workspace: `ROKSBNKCTL_REGISTRY_TARGET`, `ROKSBNKCTL_GENERIC_HOST`, `ROKSBNKCTL_GENERIC_REPO_PREFIX`, `ROKSBNKCTL_GENERIC_USERNAME` say *where* the mirror is (only the password was settable before, so a job still had to shell out to four `registry target` subcommands), and `ROKSBNKCTL_FLP_EXTERNAL_URL` + `ROKSBNKCTL_FLP_ROOT_CA_B64` are the **cross-job handoff** — exactly what `flp output` prints, so the proxy's address and CA travel between CI jobs as ordinary job outputs. The CA variable is stored **verbatim** (it is already base64); re-encoding it hands the CWC a corrupt CA, so a test pins it.

### Fixed

- **`flp up` could not run in a container at all.** The `f5-license-proxy` chart is installed through a Helm post-renderer, and that post-renderer was a *generated python script* — which made `python3` an undeclared runtime dependency of the whole FLP phase. Invisible on a laptop that happens to have python; fatal in the tools-runner image, which has none, where `flp up` died with `error while running post render`.

  A post-renderer is just "a binary Helm pipes manifests through", so roksbnkctl is now that binary (`roksbnkctl flp postrender`), with the transform as a plain tested function. Terraform points Helm at the *running* roksbnkctl, so the chart is always post-rendered by the exact build driving the apply. The hidden dependency is removed rather than documented, and the runner image still contains no python.

## v1.18.0 — 2026-07-14

### Added

- **License a BNK install from a proxy in ANOTHER cluster — `flp up --add-node-port-access`.** The F5 License Proxy is a cluster-wide broker, and it is the only component that needs egress to F5. It can now run **once**, in a services cluster that has that egress, and license BNK installs in other clusters — same VPC, or across a transit gateway — that reach nothing but your private registry and the proxy. See [Flow C — a shared licensing cluster](book/src/10c-flp-licensing.md#flow-c--a-shared-licensing-cluster).

  The chart already ships a `NodePort` Service, but three things stopped it being usable from outside, and the flag fixes all three: it hardcodes `externalTrafficPolicy: Local` with one replica (so only the node hosting the pod answers, and that node moves) — now flipped to `Cluster`; the proxy's certificate had no **IP SANs**, so a remote controller dialling `https://<node-ip>:30001` failed the handshake with `bad certificate` — the worker IPs are now SANs; and ROKS workers sit in a security group that does not admit another cluster — `--node-port-source-cidr` opens just that port, just to the CIDR you name.

  The consuming workspace never runs `flp up`. It points at the foreign proxy with `bnk.flp.external` (`url` + `root_ca_b64`, both from `roksbnkctl -w <owner> flp output`), and `bnk up` wires its License CR and delivers the CA to the CWC exactly as it would for a local proxy.

- **License BNK with an in-cluster F5 License Proxy (FLP) — optional, opt-in.** A new `flp` lifecycle phase deploys the `f5-license-proxy` chart (the proxy plus its bundled Vault and PostgreSQL) into an existing cluster, and points BNK's `License` CR at it, so the cluster's workloads never talk to F5's licensing service directly. `roksbnkctl flp up` generates the proxy's CA and mTLS certificates, creates its secrets, installs the chart, and records the CA + service endpoint in `flp-outputs.json`; a subsequent `bnk up` reads that handoff and wires the License CR — no certificate is ever copied by hand.

  **Both licensing modes are first-class.** Everything is gated behind `bnk.license_mode: f5licenseproxy`; leave it unset (the default) and BNK licenses with the subscription JWT exactly as before, rendering byte-identical tfvars. The subscription JWT is still required in FLP mode — the proxy presents it to F5.

  Like `gateway`, the FLP is an independent phase with its own terraform state (`state-flp/`), run explicitly between the cluster and BNK; the composite `up`/`down` never runs it. Works against a cluster `roksbnkctl` creates **and** against an [existing cluster you register](book/src/09-registering-existing-cluster.md) — `flp up` adds only the proxy workload, and never takes ownership of a registered cluster's lifecycle. New `roksbnkctl flp up|down|output`, config keys `bnk.license_mode` / `bnk.flp.*`, and env overrides `ROKSBNKCTL_LICENSE_MODE` / `ROKSBNKCTL_FLP_NAMESPACE`. See [Licensing BNK with the F5 License Proxy](book/src/10c-flp-licensing.md).

- **Install from an external OCI registry mirror (Harbor, Artifactory, …) using its own credentials.** Mirror-mode installs previously only knew how to authenticate to IBM Container Registry (`iamapikey`) or the in-cluster OpenShift registry (kube token). A private Harbor accepts neither, so replicating FAR into one worked but installing back *out* of it failed with `unauthorized`. Chart **and image** pulls now authenticate to the mirror with the same basic-auth credential `registry replicate` used (`registry target generic_username` / `generic_password`) — so a private registry needs no anonymous/public project. Chart pulls log in with the credential; the pods get a `mirror-secret` dockerconfig built from it, in every namespace that pulls (cert-manager, the FLO/BNK namespaces, and `kube-system` for the node-labeler). Previously the pull secret was dropped for *any* mirror, which assumed the mirror authorizes by RBAC — true for the in-cluster OpenShift registry, false for a private Harbor, whose pods then pulled anonymously and hit `ImagePullBackOff`. The only workaround was to make the mirror project world-readable, which for a registry holding F5's proprietary images is not an acceptable requirement. ICR and in-cluster mirrors are unaffected and keep their existing auth.

- **`scripts/deploy-far-registry.sh`** — stands up a standard open-source Harbor on a fresh IBM Cloud VSI (own security group + floating IP, Let's Encrypt TLS via `sslip.io`) as a replication target, and optionally wires it straight into a workspace as `registry target generic`. Replaces the non-functional `deploy-artifactory.sh`, which is removed.

### Fixed

- **A REGISTERED cluster now works as a base for every phase.** Adopting a cluster you already own — `cluster register`, the documented alternative to `cluster up` — was broken end to end, in three independent places:

  - Terraform never actually used the cluster you named. `roks_cluster_id_or_name` is the declared "existing cluster" input, but nothing downstream read it: the cluster module resolves an adopted cluster with `data "ibm_container_vpc_cluster" "existing_cluster" { name = var.openshift_cluster_name }`, which was passed through unconditionally — so the lookup ran against that variable's *default* and every phase died with `The specified cluster could not be found`. The submodule now receives the same coalesced identity `roks_cluster_name` already exposes.
  - `cluster register` could not find the registry COS instance of a cluster **roksbnkctl itself created**: it probed only `<cluster>-cos` (the upstream HCL fallback), while `cluster up` names it `<prefix>-registry-cos`. It now probes the names actually in use, and `--registry-cos-name` still overrides.
  - `flp up` refused to run on a registered cluster at all ("no cluster found"). Phase presence only sees a cluster roksbnkctl *created* (a managed resource in `state-cluster/`); a registered one has no terraform state, only `cluster-outputs.json`. It now falls back to that, as the composite `up` already did.

- **`roksbnkctl -w <ws> k …` talks to the workspace's cluster.** Every kubeconfig it could reach is *ambient*, not workspace-scoped — `$KUBECONFIG`, `~/.kube/config`, and even the minted forge config are single files shared by all workspaces — so with two clusters up, `-w a k get` read whichever cluster was configured last, and `-w a k delete` would have deleted in it. The kubeconfig is now selected by the workspace's cluster id, and the context is pinned explicitly rather than trusting `current-context`. Selection is credential-aware: IBM's kubeconfigs carry a context that *names* the cluster but has an empty user (which authenticates as `system:anonymous` and is forbidden everything), while the context holding the token points at a host-named cluster entry with no id in it.

- **A "mirrored" BNK install no longer phones home.** Two gaps meant an air-gapped install still reached back to `repo.f5.com`, and on a mirror it could not complete at all:
  - The **`f5-bigip-k8s-manifest` was never mirrored.** It is the bill-of-materials' own *source*, so it was not a BOM member — yet the install pulls it to derive the FLO and CIS chart versions. It is a normal OCI chart, so it is now a BOM artifact (`release/f5-bigip-k8s-manifest`); `registry replicate` copies it and the install reads it from the mirror.
  - **FLO fetched the manifest from the registry at runtime.** The F5 Lifecycle Operator resolves the BNK manifest by listing cluster-scoped **`CNEManifest`** CRs and matching `spec.version`, and only falls back to pulling the manifest chart from the CNEInstance's `spec.registry.uri` when none matches. That fallback 404s against a mirror, so the CNEInstance never reconciled (*"No CNEManifest exists which contains expected manifestVersion"*). `roksbnkctl` now converts the manifest into a `CNEManifest` CR and applies it before the CNEInstance — FLO reads the manifest from the cluster and never contacts a registry for it.

  Verified end-to-end: a clean `bnk down` + `bnk up` against a private Harbor makes **zero** `repo.f5.com` requests.

- **`bnk.flp.chart_version` is now genuinely optional.** The BNK manifest lists `charts/f5-license-proxy` for the release, exactly as it lists the FLO and CIS charts, so the FLP chart version is resolved from it. Previously an unset version was a hard error (an OCI `helm pull` cannot resolve "latest"), which made the "optional" pin effectively mandatory. Set it only to override the manifest.

- **`roksbnkctl k get` shows a resource's real columns.** It never asked the API server for a `Table`, so the response was a plain object list and the printer could only fall back to `NAME`/`AGE` — quietly degrading *every* get. A `License` printed without `STATE`/`MODE`/`ENTITLEMENT`; even `k get pods` lost `READY`/`STATUS`/`RESTARTS`. It now requests a server-rendered table the way `kubectl` does, so a CRD's `additionalPrinterColumns` come through as real columns. `-o yaml/json/name/jsonpath/go-template` are unaffected.

- **`roksbnkctl init --config-file` no longer zeroes the resource toggles it doesn't mention.** A config carrying only, say, `resources.transit_gateway.create: false` also set `bnk`, `registry_cos` and `cert_manager` to `false` (the bool zero value), disabling the install. The file's toggles now overlay the defaults, matching what `--non-interactive` already did.

  > **Behaviour change.** An omitted toggle now means *"use the default"* (mostly `create: true`) rather than *"off"*. If you have a `--config-file` with a **partial** `resources:` block that relied on the old zero-value behaviour to keep things off, it will now create them — including cost-bearing ones (`transit_gateway`, `client_vpc`, `tgw_jumphost`). Spell out the toggles you want off.

- **The FLP phase is registered in the phase-mapping tables.** `state-flp/` was missing from both `phaseFromStateDir` (the backend state key) and `phaseLabel` (the applied-tfvars snapshot), so it fell through to the BNK phase's identifiers. On an **s3/COS backend that meant `flp up` initialised against the BNK phase's remote state object** and could destroy the BNK deployment; on any backend it overwrote the BNK phase's tfvars snapshot. `state migrate` also skipped the FLP state. A test now asserts every phase's state dir maps to a distinct key in both tables.

- **`k get` regressions from the server-side Table switch.** Rows are now decoded like `kubectl`'s `decodeIntoTable`, so `k get -A` prints real values in the `NAMESPACE` column instead of blanks; and an empty result is detected on the Table's row count, so `No resources found` is printed again instead of silent, blank output.

- **A wrong mirror credential fails fast.** `registry replicate` checks the push credential once up front. Previously a bad password was retried against every artifact in the BOM (a 401 is retryable — Harbor's token service genuinely flakes), so the command ground on for minutes and reported ~100 failures instead of one clear message. A malformed `registry.generic_password_b64` is also now an explicit error rather than silently dropping the credential.

- **The CWC no longer restarts on every apply.** `null_resource.cwc_flp_rollout` keyed on `kube_token`, which is a fresh IAM token on each refresh, so it was replaced on every run and bounced the cluster-wide controller. It now rolls only when the FLP CA actually changes, as documented. The FLP root-CA Secret is also written in `legacy_curl` CR mode, which previously got the FLP License-CR fields with no CA ever delivered.

- **`roksbnkctl down` refuses while the FLP phase has resources**, instead of destroying the cluster out from under it and stranding `state-flp/`. Mirrors the existing `gateway` and `cluster down` guards: run `flp down` first. Conversely, the BNK teardown no longer *requires* the FLP handoff — `flp down` legitimately removes `flp-outputs.json` before the composite `down` runs, and a destroy does not need the proxy's endpoint or CA.

- **FLP on ROKS/OpenShift.** The `f5-license-proxy` chart ships `hostPath` PersistentVolumes (a single-node/dev model) that cannot bind on a multi-node, non-root ROKS cluster; a Helm post-renderer now drops them and repoints the PVCs at a dynamic StorageClass (`flp_storage_class`, default `ibmc-vpc-block-metro-10iops-tier`, which provisions in the consuming pod's zone). The proxy's service account is also bound to the `privileged` SCC (it needs `fsGroup` + `IPC_LOCK`), and the proxy's server certificate now carries its Kubernetes Service DNS names — without them the CWC rejected the TLS handshake with `bad certificate`.

### Dependencies

- **Go modules** — `IBM/go-sdk-core` 5.17.5→5.22.1, `IBM/ibm-cos-sdk-go` 1.11.0→1.14.1, `IBM/platform-services-go-sdk` 0.66.0→0.101.0, `google/go-containerregistry` 0.21.6→0.21.7, `hashicorp/terraform-exec` 0.21.0→0.25.2, `moby/moby/api` 1.54.2→1.55.0, `moby/moby/client` 0.4.1→0.5.0, `testcontainers/testcontainers-go` 0.42.0→0.43.0.

  The platform-services bump is a **breaking API change**: `NewCreateProfileLinkRequestLink` went from `(crn, namespace)` to `(crn)`, and `Namespace` became an `omitempty` struct field. The API still requires it for `cr_type` `IKS_SA`/`ROKS_SA` — which is exactly what the ops-pod trusted-profile link sends — so *not* setting it still compiles and still marshals; it just silently omits `namespace` on the wire. The field is now set explicitly, and a test asserts the link request body carries `crn`, `namespace` and `name`, because nothing else would have caught it.

- **GitHub Actions** — `actions/checkout` 6→7, `github/codeql-action` 3→4, `gitleaks/gitleaks-action` 2→3.

## v1.17.5 — 2026-07-09

### Fixed

- **`bnkforge register` sets the project's target platform (was "Unknown").** A BNK Forge project's target platform is configured, not derived from its clusters' detected platform — so even after v1.17.4 gave the *cluster* the IBM platform, the *project* still showed **Target Platform: Unknown**. `EnsureProject` now sets `target_platform_profile: "roks"`, `platform_provider: "ibm"`, and `cloud_provider: "ibm"` on the project (on both create and reuse, so pre-existing projects are corrected too). Best-effort: a Forge that lacks these fields won't fail registration.

## v1.17.4 — 2026-07-08

### Fixed

- **`bnkforge register` sets the IBM platform and is now idempotent.** (1) The register body now sends `cloud_provider: "ibm"` — BNK Forge reads `cloud_provider` (not `provider`) for the platform badge, so registrations previously showed as **Unknown** (the stored default `on-prem`). (2) Registration is now **idempotent**: if a cluster with the same name already exists in the target project (a re-run of a CI pipeline, or a re-registration with a refreshed kubeconfig), the stale registration is removed before the new one is created, instead of conflicting. This makes `bnkforge register` safe to call repeatedly — including from a re-triggered CI job.

## v1.17.3 — 2026-07-08

### Fixed

- **`bnkforge register` now completes against BNK Forge v3** — three request/response field mismatches in the v1.17.2 REST port, found by an end-to-end run against a live Forge, are corrected: (1) the project-create response returns the id as `project_id` (not `id`), so registration failed with "create project returned no id" even though the project was created; (2) the cluster-register body **requires the cluster `kubeconfig`** (roksbnkctl now sends the cert-based forge kubeconfig `cluster up` writes, falling back to `KUBECONFIG`/`~/.kube/config`); and (3) that kubeconfig must be **base64-encoded**. Created-id parsing is now tolerant of the `id`/`project_id`/`cluster_id` naming variance across Forge's endpoints. `bnkforge register` verified green end-to-end (login → IBM credential template → project → register → verify).

## v1.17.2 — 2026-07-08

### Changed

- **`bnkforge register` now talks to BNK Forge v3's REST API directly** instead of shelling out to a `bnk-forge` CLI. BNK Forge v3 is REST/UI-first and ships no CLI, so the old exec-based path could never succeed against it. A new `internal/forge` client authenticates (`POST /api/auth/login`), ensures a default IBM credential template holding the workspace's IBM Cloud API key (so Forge re-derives the cert-based kubeconfig on demand), ensures the target project, and registers the cluster (`POST /api/projects/{id}/k8s/clusters`). Credentials follow the IBM-API-key pattern: URL/username from `--url`/`--username`/`BNK_FORGE_URL`/`BNK_FORGE_USER`/config; the **password is never persisted** (`--password`/`BNK_FORGE_PASSWORD`/hidden prompt), and the returned **session token is cached in the OS keychain** for repeat runs. New flags: `--username`, `--password`, `--insecure` (self-signed lab certs). `bnkforge status` now shows URL/username/project/insecure/cached-session instead of the CLI path.

## v1.17.1 — 2026-06-20

Fixes the forge kubeconfig so a registered IBM ROKS cluster can actually authenticate.

### Fixed

- **The forge kubeconfig (`$ROKSBNKCTL_HOME/forge/kubeconfig.yaml`) is now cert-based, not token-based.** ROKS is Red Hat OpenShift: its API server authenticates via client certificates or OpenShift OAuth tokens and **rejects raw IBM IAM bearer tokens with 401**. The token-based forge kubeconfig introduced in v1.16.x registered in BNK Forge but could not connect (dashboard red, 401 on every call). `cluster up` now builds the forge kubeconfig from the cluster's admin client certificate/key (new `k8s.BuildCertKubeconfig`), which authenticate directly. The freshness gate already classifies a cert kubeconfig as cert-based and keeps it current by re-fetching the admin kubeconfig as the certs near expiry; `kubeconfig --refresh` forces that re-fetch. CA stays optional (ROKS public masters carry none). Docs (book chapters 24a/27/33 + the embedded operator AGENTS.md) updated to match.

## v1.17.0 — 2026-06-20

Adds an opt-in **agentic mode**: drive a ROKS + BNK trial with an AI coding agent acting under role-scoped personas, while roksbnkctl stays a deterministic tool (it embeds no LLM).

### Added

- **`roksbnkctl agent` — agentic operating mode.** `agent init` scaffolds a workspace with `AGENTS.md` (the shared operator reference — phased lifecycle, destructive-command gate contract, field-tested gotchas), four persona role contracts under `personas/` (`solution-architect` = customer interface + scope owner; `cloud-operator` = runs the lifecycle; `test-engineer` = validation probes; `doc-specialist` = the report), a `decisions.md` seed, and a `journal/`. Re-running is idempotent — operator edits survive. `roksbnkctl agent [claude|gemini|aider|openai|pi|opencode]` prints the invocation to launch your chosen coding-agent CLI against them; bare `agent` lists the CLIs and the workspace default. roksbnkctl embeds no LLM — you bring your own agent and endpoint.
- **`roksbnkctl journal {add,list,report}` — the append-only handoff substrate** the personas coordinate through (`<workspace>/journal/`). `add` appends a timestamped note; `list` shows the timeline; `report` assembles `report.md` from `decisions.md` + the journal.
- **Optional `agent:` block in `config.yaml`** (`default` CLI + `llm_endpoint`), annotated in the example config.
- **New book chapter: "Agentic mode"** (Part X) documenting the personas, the `agent`/`journal` commands, the coordination protocol, and the safety model.

## v1.16.1 — 2026-06-20

Fixes the token-based forge kubeconfig so it's actually written for IBM ROKS clusters.

### Fixed

- **The token-based forge kubeconfig (`$ROKSBNKCTL_HOME/forge/kubeconfig.yaml`) is now written for IBM ROKS clusters.** In v1.16.0 the builder hard-required `certificate-authority-data` and bailed with `cluster has no certificate-authority-data (admin kubeconfig is not self-contained)` — so the file was never written and BNK Forge skipped cluster registration (`Declared cluster.kubeconfig_file not readable … No such file or directory`). But IBM ROKS masters (`*.containers.cloud.ibm.com`) present a publicly-trusted TLS cert, so the admin kubeconfig legitimately has **no** CA and none is needed (system trust validates the server). The builder now treats a missing CA as the expected case: it carries `certificate-authority-data` through when the source has one (private/self-signed clusters) and omits the field entirely when it doesn't — never emitting an empty value. The post-apply export remains best-effort and never fails `cluster up`.

## v1.16.0 — 2026-06-20

Token-based kubeconfig with automatic refresh: `cluster up` now also emits a
portable, token-based kubeconfig for BNK Forge registration, and every
kubeconfig consumer self-heals an expiring session before it runs. Plus the
BNK Forge cluster provider is declared as `IBM`.

### Added

- **`cluster up` emits a token-based kubeconfig at `$ROKSBNKCTL_HOME/forge/kubeconfig.yaml`.** Alongside the existing admin (cert-based) config, roksbnkctl now writes a fully self-contained, **token-based** kubeconfig — one cluster entry with the public server + embedded `certificate-authority-data`, one `token` user, no client cert/key. This is the form BNK Forge registers from (it re-mints the IAM token from the project credential template), so a long-idle registered cluster keeps authenticating. Written `0600`, overwritten on every `cluster up`.
- **`roksbnkctl kubeconfig --refresh`.** Force-refresh the token-based kubeconfig now (re-mint the IAM token), for CI/scripting.

### Changed

- **`kubeconfig` / `kubectl` / `oc` / `shell` prefer the auto-refreshed token kubeconfig.** Before each use, a single `ensureFreshKubeconfig` gate checks the embedded credential's expiry locally (no network); if a token is within ~5 minutes of expiry it re-mints it (a cheap IAM exchange) and rewrites the file atomically — otherwise it's a no-op. This keeps `roksbnkctl`'s own CLI passthroughs working without manual `kubeconfig --download`. Falls back to the admin cert-based config at `~/.kube/config` when there's no token kubeconfig or it can't be refreshed offline. The admin-kubeconfig write is unchanged.
- **BNK Forge registration declares `--provider IBM`** (was `ibm_roks`), matching BNK Forge's provider taxonomy.

### Fixed

- **The IBM admin kubeconfig now inlines the cluster CA** (`certificate-authority` file ref → `certificate-authority-data`). Modern IBM kubeconfigs already embed it (no-op); older ones used a relative `.pem` ref that broke once the kubeconfig moved off its download dir. This makes the admin config self-contained and is what lets the token kubeconfig embed the CA.

## v1.15.0 — 2026-06-20

Runner-image robustness for the phased lifecycle: Helm chart pulls and the admin-kubeconfig fetch no longer depend on a writable `$HOME`, phase `down` is idempotent for reverse-order teardown, and the cluster module's kubeconfig data source is wired explicitly. Plus a new `apikey` command and an Artifactory deploy helper for air-gap testing.

### Added

- **`roksbnkctl apikey` — print the workspace's resolved IBM Cloud API key.** Resolves the key through the same credentials chain the rest of the CLI uses and prints it to stdout, so you can splice it into ad-hoc tooling, e.g. `ibmcloud login --apikey "$(roksbnkctl -w <ws> apikey)"`. The key is never written to disk by roksbnkctl.
- **`scripts/deploy-artifactory.sh` — stand up an Artifactory VSI in the cluster VPC** for air-gap registry-replication testing. Step-by-step progress with no silent login hang; generates an RSA SSH key (IBM Cloud VPC rejects ed25519).

### Changed

- **`bnk` / `testing` / `gateway down` now no-op-succeed on empty/absent state.** They exit 0 with a "nothing to do" message instead of erroring when the phase was never deployed (an optional `testing`/`gateway` phase, or a cluster-only/empty workspace for `bnk down`), so an unconditional reverse-order teardown of every phase no longer trips on a missing phase (which would otherwise block the cluster destroy). `bnk down` still refuses on legacy single-state workspaces (use `roksbnkctl down` there).

### Fixed

- **Helm chart downloads no longer depend on a writable `$HOME`.** Each phase now exports `HELM_CACHE_HOME` / `HELM_CONFIG_HOME` / `HELM_DATA_HOME` (the homes that actually govern Helm's anonymous chart-repo-URL path, plus the OCI path), alongside `HELM_REPOSITORY_*`, under `$ROKSBNKCTL_HOME/.helm`, and pre-creates them. In a fresh runner whose `$HOME` is empty or non-writable, a `helm_release` pulling from a chart-repo URL (cert-manager off `charts.jetstack.io`) previously failed with `could not download chart: ... open <HOME>/.cache/helm/repository/<hash>-index.yaml: no such file or directory`; the index now downloads into the writable workspace tree. `ROKSBNKCTL_DEBUG=1` logs the resolved Helm/kubeconfig env before each phase's terraform exec.
- **The admin-kubeconfig fetch lands in a writable path in a fresh runner.** `$KUBECONFIG` is redirected to `$ROKSBNKCTL_HOME/.kube/config` when `$HOME/.kube` isn't writable (otherwise the conventional `~/.kube/config` is kept), and `roksbnkctl k …` resolves the same fallback. Removes the `warning: creating <HOME>/.kube: mkdir <HOME>: permission denied` from the post-apply fetch.
- **The cluster module's `ibm_container_cluster_config` data source now sets an explicit `config_dir`** (threaded `roks_cluster` → `cluster`), and roksbnkctl pre-creates the `kubeconfig/cluster` leaf dir. Fixes `Error downloading the cluster config: Path: '', to download the config doesn't exist` (and the `.../kubeconfig/cluster ... doesn't exist` variant) on a runner with no usable `$HOME`, even for a healthy cluster.

## v1.14.0 — 2026-06-18

The OpenShift internal registry is removed as a registry-replication target. ICR (default) and OCI-compliant registries (Artifactory / Harbor / Quay / `registry:2`) are the supported targets.

### Removed

- **BREAKING: the `openshift` registry-replication target is removed.** `registry replicate` / `registry target` / `verify` / `prune` / `delete` no longer accept `openshift` — the supported targets are `icr` (the default) and `generic` (any OCI-compliant registry). Gone with it: the `--target openshift` value, the `internal/registry/openshift` package, and the openshift-only `--kubeconfig` flag on the cluster-touching registry verbs. A workspace still carrying `registry.target: openshift` now errors — `unsupported registry target "openshift" (expected icr or generic)` — switch it with `roksbnkctl registry target icr` (or `generic`).

### Changed

- **Docs: the "Air-gapped install" chapter is rewritten around ICR + OCI/Artifactory** (it previously centered the cluster's own OpenShift internal registry). The air-gap flow — BOM → `replicate` → install-redirect → `verify` — now leads with the private-registry targets; the "Registry targets" chapter drops the `openshift` row and notes its removal.

## v1.13.0 — 2026-06-18

Declarative-input cleanup and a phase-output fix: `init example` now prints an annotated **config.yaml**, the legacy `init --var-file` is removed, and per-phase `output` is scoped to each phase's own attributes (plus a new top-level `output` that merges them).

### Added

- **`roksbnkctl output` — the merged outputs across all phases.** The union of every phase's own outputs, each read from its owning phase's state, so values are the populated ones and never conflict. Pairs with the now-scoped per-phase commands; `roksbnkctl output <name>` returns one value from whichever phase owns it.
- **The gateway phase now emits outputs.** `gateway output` surfaces the gateway's managed attributes — app + FLO namespaces; the `Gateway` / `F5BnkGateway` / `GatewayClass` / `HTTPRoute` names; per-zone listener networks (VIP ranges); egress mode with the `F5SPKSnatpool` / `F5SPKEgress` CR names and per-zone SNAT addresses; the `F5SPKStaticRoute` set; and the VXLAN port. (Previously the gateway phase promoted no root outputs.) Needs a `gateway up` re-apply to populate an existing workspace.
- **`gateway status` probes the live CRs.** Beyond the configured outputs, it reports what terraform state can't: the controller-assigned `Gateway` address and the `Programmed` / readiness conditions on the `Gateway` + `F5BnkGateway` CRs. Best-effort, and resolves CRD plurals via cluster discovery (a new `k8s.BuildRESTMapper`), so no plural is hardcoded.

### Changed

- **`roksbnkctl init example` now prints an annotated `config.yaml`**, not `terraform.tfvars`. config.yaml is the canonical input; the embedded terraform is internal. The template fills the required fields and documents every optional axis (cluster create-or-attach, BYO infrastructure reuse incl. `cluster_vpc`, BNK install + CIS + per-AZ addressing, gateway, registry mirror, remote state). One-shot author-and-seed: `roksbnkctl init example > config.yaml && roksbnkctl -w <ws> init --config-file config.yaml`. The template is schema-checked in CI (it strict-parses into the workspace config, unknown fields rejected) so it can't drift.

### Removed

- **BREAKING: `roksbnkctl init --var-file` is removed.** `init` previously seeded `config.yaml` (and dropped a verbatim `terraform.tfvars.user`) from a `terraform.tfvars` file; the seed surface is now `config.yaml` itself via `--config-file` (local path or URL) or `--non-interactive` (from `ROKSBNKCTL_*` env). Raw terraform-variable overrides are **unchanged**: place a `terraform.tfvars.user` at the workspace root (auto-layered on every lifecycle op), or pass `--var-file` on a **phase** command (`cluster`/`bnk`/`gateway up`/`down`) — those flags are unaffected.

### Fixed

- **`roksbnkctl <phase> output` leaked other phases' attributes and could show conflicting values.** All phases apply the same terraform root into separate states, so each phase's `terraform.tfstate` carried the full output schema — `bnk output` showed blank `testing_*`, `testing output` showed blank `flo_*`, and shared keys (e.g. `flo_trusted_profile_id`) differed between phases. Each `<phase> output` is now scoped to the outputs that phase actually manages (per `terraform/outputs.tf` module ownership: `roks_cluster` → cluster, `flo` → bnk, `testing` → testing); cross-phase keys are dropped, and naming another phase's output points you at the right command. A drift test pins that the ownership map partitions every root output exactly once.

## v1.12.0 — 2026-06-18

The customer-release cut: a leaner, declarative, CI-driven workflow, a trimmed repository, and a security cleanup of leaked cluster credentials. New config axes let one committed `config.yaml` (plus `ROKSBNKCTL_*` env) stand up many workspaces unattended; the repo drops 443 internal-development files; and 48 leaked ROKS kubeconfigs are purged from the tree, the `.gitignore`, and git history.

### Added

- **`init --non-interactive` — build `config.yaml` from the environment alone.** No seed file, no prompt, no TTY: the workspace is assembled entirely from `ROKSBNKCTL_*` / `IBMCLOUD_API_KEY`, validated for completeness, and fails fast (never falls back to a prompt) if a required field is missing. This is the argv+env container-runner path (a CI job or a BNK Forge container step).
- **A single create-or-attach cluster toggle.** `cluster up` now **creates** a cluster when `cluster.create: true` and **attaches** to an existing one when `false` — collapsing the previous pair of inverse booleans into one switch driven by config.
- **Bring-your-own infrastructure, expressed as config (never new commands).** Adopt an existing Transit Gateway by name, a cluster VPC by ID (`resources.cluster_vpc`), name the Testing client VPC, set per-AZ addressing (external/internal VLAN, SNAT, and VIP CIDRs + self-IPs), and supply CIS BIG-IP URL/username/password — all via `config.yaml` and matching `ROKSBNKCTL_*` env overrides.
- **`output` on every phase.** `roksbnkctl <phase> output [name] [--json] [--show-sensitive]` (cluster/bnk/testing/gateway) prints that phase's terraform outputs, mirroring `terraform output`: the full set (sensitive redacted) or one named raw value for `$(…)` capture. The sibling of the existing per-phase `status`.
- **BNK Forge registration configured from the CLI.** The `state:` / `exec:` blocks and cluster registration are set through the CLI — no `config.yaml` hand-editing — with a best-effort registration after `cluster up`.
- **Docs: a "GitHub Actions CI as an example" chapter** (env → `init --non-interactive` → `up`, single cluster and a matrix fleet) and **PRD 17 (declarative-CLI direction)**.

### Security

- **Removed 48 leaked IBM ROKS kubeconfigs** (`terraform/**/*_k8sconfig/config.yml`, each carrying an IAM token) that were `go:embed`'d into the binary and shipped in public runner images. The path is now `.gitignore`d so they can't return, and **git history has been rewritten to purge them**. The exposed tokens must be treated as compromised and rotated in IBM Cloud — a history rewrite cannot recall what was already published.

### Removed

- **443 internal-development files** trimmed for the customer release (~890 → ~447 tracked): the agentic-dev process (`.archive/`, `issues/`, `prompts/`, `agents/`), internal QA scripts and CI workflows, and dev-only root files. The shipped surface — `cmd/` `internal/` `terraform/` (cleaned) `book/` `docs/` `go.*` `embedded.go` `tools/docker` `tools/refgen` and the release/CI/security workflows — is unchanged and the embed still resolves.

## v1.11.4 — 2026-06-15

A one-line auth fix: the BNK install now pulls the FLO chart correctly from an ICR-backed air-gap mirror.

### Fixed

- **BNK install failed to pull the FLO chart from an ICR air-gap mirror.** With `registry.target: icr` (the Sprint 30 default), the FLO OCI chart pull authenticated with the cluster's **OpenShift bearer token** — but IBM Container Registry **rejects** a bearer token (`"The requested authentication method is not supported"`); it requires `iamapikey` + an IBM Cloud API key. The on-mirror credential branch had assumed the OpenShift in-cluster registry route. `module.flo` now routes chart-pull credentials by backend: FAR (`_json_key_base64`) off-mirror, **`iamapikey` + the workspace API key for an `*.icr.io` mirror**, and the OpenShift token for the in-cluster registry route.

## v1.11.3 — 2026-06-13

A reliability patch with two live-validated fixes — gateway static routes and teardown — plus a workspace-aware `full-shakeout.sh --live` harness that exercises a real cluster lifecycle end-to-end. No config changes; existing workspaces are unaffected.

### Fixed

- **Gateway `up` failed for CIDR client subnets.** `module.gateway` set `F5SPKStaticRoute.spec.destination` to the whole client-subnet CIDR (e.g. `10.241.0.0/24`) and hardcoded `prefixLen = 32`, but the CRD wants a **bare network IP** in `spec.destination` with the mask in `spec.prefixLen` — so `gateway up` was rejected (`spec.destination: Invalid value`) whenever a `gateway_client_subnet_{local,remote}` entry carried a prefix (the common case since the per-AZ CIDR-list support). The module now splits the CIDR: `destination` = the network address, `prefixLen` = the CIDR's prefix (defaulting to `/32` for a bare host IP).
- **`roksbnkctl down` could leak a VPC on a transient teardown race, and couldn't resume.** A `terraform destroy` that hit an IBM-provider delete-race — `DeletePublicGatewayWithContext failed: … not found`, the gateway already removed by terraform's own parallel destroy — aborted the cluster-phase teardown **before the VPC**, leaking it; and a re-run reported "nothing to destroy" once the cluster resource was gone. Teardown now **retries** transient destroy failures (`destroyWithRetry` — idempotent, so the retry refreshes the vanished resource out of state and finishes), and `down` / `cluster down` **resume** past a gone cluster resource (a new `ClusterResidual` presence signal) instead of orphaning the leftover network.
- **Live-cluster integration tests hard-failed against a torn-down cluster** instead of skipping. `make test-integration`'s kubectl-passthrough / ops checks now `clusterUnreachableSkip` on connection-level errors to a *real* (non-`localhost:8080`) API server — a missing precondition, not a regression; the `localhost:8080` no-config regression guard is preserved.

### Added

- **`full-shakeout.sh` workspace mode + opt-in live tier.** Pass an initialized workspace name (`./scripts/full-shakeout.sh <ws>`) to target that workspace's own rendered inputs instead of a loose `./terraform.tfvars`. A gated `--live` tier (TIER L) then runs a full cloud lifecycle against it — `up` → `gateway up` → connectivity/DNS probes → `down` — only behind a green Tier 0 + Tier 1, with a teardown safety-trap so a partial apply never leaks. Off by default; never spends without `--live` + `IBMCLOUD_API_KEY`. Internal test tooling — no change to the shipped CLI surface.

## v1.11.2 — 2026-06-12

A maintenance patch: a leaner runner image and release binaries built with the latest stable Go (shipping the remaining Go-stdlib advisory fixes). No functional changes.

### Changed

- **The `roksbnkctl-tools-runner` image is ~58 MB smaller.** A late `chown -R … && chmod -R … /home/runner` rewrote the ibmcloud plugin's files into a duplicate ~58 MB layer (copy-on-write). The `/home/runner` and `/work` dirs are now created owned by uid 1000 / gid 0 up front, and the ibmcloud config is chowned *inside* the install RUN (in-layer) — same runtime permissions, one fewer duplicate layer.

### Security

- **Release binaries now build with the latest stable Go** (`release.yml`), not just the `go.mod` minimum (1.25.0), so they ship current Go-stdlib security fixes — the toolchain-level advisories `govulncheck` flagged (go1.26.3 → go1.26.4) that a module bump can't address. Takes effect from the next tagged release.

## v1.11.1 — 2026-06-11

A security patch on top of v1.11.0: a CI security sweep plus the dependency CVE fixes it surfaced. No functional changes.

### Security

- **Added a CI security sweep** and fixed the reachable vulnerabilities it found. New `security.yml` runs `govulncheck` (gated), `gitleaks` (gated), CodeQL, and Trivy (terraform IaC + deps → code scanning), plus a Trivy CVE scan of the runner image in `runner-smoke.yml` and a `dependabot.yml` (gomod + github-actions). The first `govulncheck` pass flagged 3 reachable module CVEs, now patched: **`golang.org/x/crypto` v0.51.0 → v0.52.0** (GO-2026-5013, reachable via the SSH client), **`github.com/moby/spdystream` v0.2.0 → v0.5.1** (GO-2026-4958, via k8s exec), and **`golang.org/x/net` v0.54.0 → v0.55.0** (GO-2026-5026). Remaining advisories are Go-stdlib toolchain fixes; the scan job runs on current stable Go.

## v1.11.0 — 2026-06-11

Sprint 30 + 31 in one release, aimed at **running roksbnkctl unattended and self-contained**: provisioning a workspace from a committed template (no secrets in git), the air-gap mirror gaining **ICR + generic-OCI** targets, an **all-in-one runner image** (the binary + every tool it dispatches in one container), and a **COS/S3 remote state backend** with `state migrate` so a stateless runner / parallel CI keeps state outside the container, locked. All additive — the default state backend stays **local** and existing workspaces are unchanged; the one behaviour change is `registry replicate` now defaulting to `icr` (set `registry.target: openshift` to keep the old default).

### Added

- **COS/S3 remote terraform state backend** ([PRD 16](docs/prd/16-REMOTE-STATE-BACKEND.md)). An opt-in `state:` config block routes each phase's terraform state into an S3-compatible bucket (IBM COS) instead of local files — so a stateless runner ([PRD 15](docs/prd/15-RUNNER-IMAGE.md)) and parallel CI need no shared host volume, with a real lock. `state.backend: s3` + `state.s3.{endpoint,bucket,region}` renders a `backend "s3"` with a per-phase key (`<prefix>/<workspace>/<phase>/terraform.tfstate`, so the four phases share one bucket), the COS skip-flags, and `use_lockfile` (the native S3 lockfile — the only COS-compatible lock, no DynamoDB). Selecting `s3` preflights **terraform ≥ 1.10** (the lockfile floor) with an actionable error; the COS **HMAC** keys resolve env-first (`state.s3.{access,secret}_key_source` → `ROKSBNKCTL_COS_HMAC_*` → `AWS_*`) and inject as `AWS_*` env to terraform — never written into the rendered HCL or the state object. Default stays **local** (an absent `state:` block is byte-identical to before). The bucket + HMAC keys are operator-provisioned. **`roksbnkctl state migrate`** copies a deployed workspace's local state into the bucket (per phase, via `terraform init -migrate-state`), refusing to overwrite a key that already holds state (`--force` to override) and leaving the local files in place for you to verify the read-back before deleting.
- **All-in-one runner image** ([PRD 15](docs/prd/15-RUNNER-IMAGE.md)). A new `ghcr.io/jgruberf5/roksbnkctl-tools-runner` image carries the `roksbnkctl` binary **plus every backend dependency it dispatches** — `ibmcloud`, `terraform` (≥ 1.10), `helm`, `kubectl`, `oc`, `iperf3`, `h2load` — so one `docker run` is a complete roksbnkctl with no host tools (CI / fleet / air-gap). `ENTRYPOINT` is `roksbnkctl`; run `--backend local` in-container (every tool is already on `PATH`). State lives under `ROKSBNKCTL_HOME=/work/.roksbnkctl` — mount a volume at `/work` to persist it (a COS/S3 remote backend is the no-volume path, [PRD 16](docs/prd/16-REMOTE-STATE-BACKEND.md), coming next). The image is **use-time only**: the docs toolchain (mdbook/pandoc/texlive) is deliberately excluded. The per-tool images stay for the docker/k8s backend-dispatch path.
- **Unattended workspace setup** ([PRD 13](docs/prd/13-WORKSPACE-CONFIG-SEEDING.md)). `roksbnkctl init` gains three options for CI / fleet provisioning from a committed template, no secrets in version control:
  - **`--config-file <path|url>`** seeds the workspace `config.yaml` directly (sibling of `--var-file`, which seeds `terraform.tfvars`). Strict parse — unknown fields are rejected, not silently dropped — and non-interactive when the config is complete; otherwise a clear error names the missing required fields.
  - **`--var-file` and `--config-file` accept an `http(s)` URL** as well as a local path (fetched with a 30 s timeout, 10 MB cap).
  - **`--override-from-env`** overlays a fixed set of `config.yaml` fields from environment variables (`IBMCLOUD_API_KEY` → `ibmcloud.api_key_b64`, plus `ROKSBNKCTL_{PREFIX,REGION,RESOURCE_GROUP,API_KEY_B64,TESTING_SSH_KEY_NAME,GENERIC_PASSWORD}`); the environment wins, and applied-field labels are logged without secret values. Full table in the book.
- **`registry replicate` targets IBM Container Registry and any generic OCI registry** ([PRD 14](docs/prd/14-REGISTRY-TARGETS.md)). The air-gap mirror now has three backends — `icr` (the **new default**), `generic` (Artifactory / Harbor / `registry:2`), and `openshift` (the cluster internal registry). ICR derives its host from `ibmcloud.region` (override `registry.icr_host`), namespaces under `registry.icr_namespace` (default: the workspace prefix), and authenticates with `iamapikey` + the workspace API key. The generic target is `registry.generic_{host,repo_prefix,username,password_b64}` (the password templatable via `ROKSBNKCTL_GENERIC_PASSWORD`). The book adds a FAR → private-Artifactory walkthrough.
- **`roksbnkctl registry target`** configures the mirror entirely from the CLI — no `config.yaml` editing. With no args it prints the current target + fields; otherwise the first arg is a backend kind (`icr`/`generic`/`openshift`) or a field name + value (`icr_namespace`, `generic_host`, …). The generic password reads from stdin with `--password-stdin`.
- **`roksbnkctl registry delete`** removes **all** replicated artifacts from the target (by digest, recorded in `registry-mirror.json`) and clears the record so the install reverts to pulling from FAR. Destructive — confirms first (`--force` to skip); artifacts that fail to delete stay in the record for a retry. (`registry prune` still handles the narrower "remove only what's no longer in the BOM".)

### Changed

- **`registry replicate` now defaults to `icr`** when `registry.target` is unset (was `openshift`). Existing air-gap workspaces must set `registry.target: openshift` explicitly — see the migration note in the book.

### Fixed

- **`ws delete` removes SSH keys `init` copied into `~/.ssh/`.** When `init` generated a testing SSH key and you accepted the copy prompt, the copied files now follow the workspace to the grave — but only files `init` itself created (recorded in `resources.copied_ssh_key_files`); a pre-existing `~/.ssh` key with the same name is never touched.

## v1.10.0 — 2026-06-10

### Added

- **`roksbnkctl test matrix`** — a declarative, repeatable BNK-on-ROKS performance grid that runs against an already-deployed cluster (a post-setup process; it changes no Terraform and never mutates the gateway-phase objects). One `matrix.yaml` declares cells across two families and the runner executes each, emitting a `roksbnkctl.v1` report (`-o json|text|md`):
  - **iperf3 (L4)** over a TCPRoute VIP, with a content-size knob (`length: "128"` vs `"512K"`, iperf3 `-l`) as the L4 analog of the perf plan's 128 B / 512 KB payload axis.
  - **h2load (L7)** against an HTTPRoute, **http and https (TLS terminate at TMM)**, in `cps` / `tps` / `throughput` modes — reporting req/s, transfer rate, and request-time min/max/mean (h2load's native stats; no fabricated percentiles).

  The locality axis (same-zone / different-zone / different-VPC) is implicit in which `vsi` jumphost a cell names as its client, so the per-AZ jumphost targets the Testing phase auto-registers (`jumphost`, `jumphost-<zone>`) are the traffic-source fleet. `--dry-run` expands the grid and prints the resolved (client, server, argv) plan plus the fixtures it would apply, with no cluster calls; `--only <glob>` runs a subset. The runner owns only ephemeral fixtures — an iperf3 server, an nginx file backend serving `/128`/`/5k`/`/512k`, and optional **TCPRoute / HTTPRoute / TLS** objects (with a self-signed cert) that **attach to the existing Gateway by name** — all torn down after (label-selected) unless `--keep`. Implements the in-scope subset of [PRD 10](docs/prd/10-PERF-TEST-MATRIX.md) with h2load substituted for the OSLO load generator. Both generators are now **preinstalled on every jumphost** by the Testing-phase `user_data` (`iperf3` + `nghttp2-client`), so the ssh runs need no `--bootstrap`; the tools images workflow also publishes a bundled `roksbnkctl-tools-h2load` for the docker/k8s backends.

  > Caveat: the route/backend fixture *apply* path is wired against the existing SSA + iperf3-fixture machinery but has not yet been validated against a live ROKS cluster; the grid model, expansion, dry-run plan, argv builders, manifest rendering, and h2load/iperf3 parsers are unit-tested.

- **`roksbnkctl gateway up` auto-derives the client subnets from the Testing phase** ([PRD 12](docs/prd/12-GATEWAY-CLIENT-SUBNET-AUTODISCOVERY.md)). The gateway static routes need the client-VSI subnets (`gateway_client_subnet_local` / `_remote`), which defaulted to placeholder `/32`s wrong for every real workspace — so a hand-run `gateway up` silently installed routes to nowhere, and a single host couldn't serve traffic from more than one client subnet. Both variables are now **lists of CIDRs** (one `F5SPKStaticRoute` per entry × zone, default `[]`), so the perf matrix can drive from a same-zone *and* a different-zone cluster jumphost and each gets a TMM return route. `gateway up` reads the deployed jumphosts' **subnet CIDRs** and fills any unset list (`remote` ← the TGW jumphost subnet, `local` ← **every** cluster jumphost subnet, logged), while anything you set in config.yaml / a user tfvars file / `--var-file` still wins; a missing or pre-this-build Testing phase warns and falls back (never fails the command). Four new outputs (`testing_tgw_jumphost_subnet_cidr`, `testing_cluster_jumphost_subnet_cidrs`, + module forwards) carry the data; an existing Testing deploy surfaces them after a no-op `testing up`.
- **`terraform.tfvars.example` now covers the previously-undocumented surfaces.** Added the **Gateway phase** block (8 vars + the client-subnet discovery recipe), **`cneinstance_network_zones`** (the per-zone TMM data-plane template — essential for a raw `terraform apply`, or TMM never reaches `RoutingDone`), the **air-gap registry mirror** vars (`far_chart_repo_url` / `far_image_repo_url` / `use_registry_mirror`), `bnk_cr_mode`, and a documented "managed by roksbnkctl — don't hand-set" group for the phase-orchestration internals.
- **Air-gap registry mirror — `roksbnkctl registry`** (PRD 11). A new command group (`bom`, `list`, `replicate`, `verify`, `diff`, `prune`) mirrors the entire BNK bill-of-materials — every chart + image the `f5-bigip-k8s-manifest` enumerates, plus the cert-manager and node-labeler dependencies — from FAR (`repo.f5.com`) into the cluster's **OpenShift internal registry**, so `bnk up` can install with all external registry egress blocked. `registry replicate -w <ws> --target openshift` prepares the registry (default route, push token, pull RBAC), copies the artifacts (linux/amd64, retried), and records `registry-mirror.json`; that record then redirects the install (`use_registry_mirror` / `far_image_repo_url` / `far_chart_repo_url`) onto the in-cluster registry. FAR auth is resolved automatically from the workspace's orchestration COS, so the bare `registry replicate` works flag-free. The OpenShift internal registry's flat `<project>/<name>` model maps each FAR category to a project (`images`, `charts`, …); a generic OCI target would keep nesting under the configured namespace.
- **Per-phase `status` commands (+ `--json`).** `cluster`, `bnk`, `testing`, and `gateway` each gain a `status` subcommand that prints the phase's live runtime state — read straight from its `terraform.tfstate` outputs (no terraform init, no API key) — plus a light cluster probe (node readiness, BNK component readiness, jumphost SSH reachability). Every `status` command, **and the top-level `roksbnkctl status`**, takes `--json` for use as a CI stage gate; sensitive outputs (the jumphost private key) are redacted. `testing status` surfaces the jumphost IPs / SSH commands and the IBM Cloud VPC SSH key name.
- **`roksbnkctl init` interviews an IBM Cloud VPC SSH key for the testing jumphosts.** When a jumphost is enabled, init asks for an SSH key name: an existing key is reused (and replicated into every region a jumphost uses but where it's missing — VPC keys are regional), otherwise roksbnkctl generates an **ed25519** keypair, stores the private key at `~/.roksbnkctl/<ws>/ssh/<name>` (chmod 600, offering to copy it into `~/.ssh`), and uploads the public key. A spinner shows progress during the network round-trips. The name is rendered as `testing_ssh_key_name`.

### Changed

- **`roksbnkctl status` now reports all four phases.** It read the old two-phase `DetectShape` and printed only **Cluster phase** + **BNK trial**, so a deployed **Testing** or **Gateway** phase was invisible (a workspace with jumphosts up showed nothing about them). It now reads `config.DetectPresence` — the same four-phase signal the `up`/`down` guards use — and prints a `deployed (last apply <time>)` / `not deployed` line for **Cluster, BNK trial, Testing, and Gateway**.
- **`roksbnkctl cluster show` is now `cluster config`** (with `show` kept as a hidden alias), and gains `--json`. It prints the *recorded* cluster identity from `cluster-outputs.json`; the new `cluster status` prints *live* runtime state — a clean config-vs-status split.
- **`cluster up` reliably records the registry COS.** `cluster-outputs.json`'s `registry_cos_name` / `registry_cos_crn` were filled by a best-effort name-guess lookup that silently missed; they're now emitted as terraform outputs and read directly (falling back to the lookup only for a reused/existing instance).

### Removed

- **The legacy single-state workspace path and `roksbnkctl testing migrate`.** The v1.0.x "cluster + trial in one tfstate" shape and the one-shot pre-Sprint-28 jumphost migration existed only to carry old workspaces forward; with those retired, the refusal guards, shape detection, the `status` `Last apply` fallback, and the `migrate` command are gone. (`--legacy-bnk`, the unrelated deploy-mode toggle, is unaffected.)

### Fixed

- **`roksbnkctl init` no longer expires its API calls while you answer prompts.** The whole interview ran under a single 60-second deadline, so taking too long answering questions failed the next call (e.g. the resource-group lookup) with `context deadline exceeded`. The interview context is now deadline-free and each network call is time-boxed on its own.

## v1.9.1 — 2026-06-08

A patch fixing two `up`/`down` regressions found running a fresh end-to-end deploy after v1.9.0.

### Fixed

- **`up` / `down` / `bnk` / `testing` / `gateway` without `-w` failed with `Provided Name … is not unique`.** `lifecycleInputs()` / `clusterInputs()` passed the **raw** `-w` flag — empty when omitted — as the workspace identifier. The orchestration uses it to look up `cluster-outputs.json` for the `create_roks_cluster=false` reuse override, so with an empty name the lookup missed, the override was skipped, and the BNK/Testing legs ran the full root config and tried to re-create the cluster VPC + transit gateway the cluster phase had just made. Everything else re-resolved the workspace via `config.New`; only this identifier used the raw flag, and the bug was masked because every test/soak run passed an explicit `-w`. Both input builders now resolve `-w` → the current-workspace pointer (the same thing `config.New` does).
- **Cold-start readiness races no longer fail the first `up` on a fresh cluster.** The BNK leg applies the F5SPKVlan CRs and the License CR while the FLO validating-webhook pod and the ResourceQuota controller are still coming up. Two races could outlast the apply-retry budget: the webhook (`server gave HTTP response to HTTPS client`) exhausting its retries, and `licenses.k8s.f5net.com … forbidden: status unknown for quota: f5-single-license-quota`, which wasn't recognized as transient at all. Added the quota race to the transient set and widened the apply-retry budget from 3×60s to 5×90s (only transient-shaped failures retry; genuine errors still fail fast).

## v1.9.0 — 2026-06-07

A large release landing three stacked workstreams: **BNK on native terraform state** (the trial layer is real terraform resources, not `null_resource`/`curl` shell), **the three-phase Cluster / BNK / Testing split** — with parallel `up`, independent teardown, and the optional **Gateway** data-plane phase — and **account-aware `init` + workspace ergonomics + the `cleanup` command**. Validated end-to-end by a 10-cycle deploy/teardown soak on a live ROKS cluster. The subsections below group every change by area.

### Added

- **`roksbnkctl init` now interviews your account.** After it verifies the API key it asks *"Create a new ROKS cluster?"* — **create** lists your account's available regions to pick from (live VPC `/v1/regions`, with a built-in fallback when offline) and floors workers-per-zone at 1 (a ROKS cluster spans all 3 AZs, so the minimum is 3 workers total); **reuse** lists your running OpenShift clusters (live `/global/v2/vpc/getClusters`), takes the region + name from the one you pick, and writes `cluster-outputs.json` inline (the same thing [`cluster register`](book/src/09-registering-existing-cluster.md) does) so a later `up` deploys straight onto it. Adding the optional testing client re-prompts for **its own region** (new `resources.client_region` → `testing_client_vpc_region`). Region/cluster calls reuse the existing raw-REST + IAM-Bearer transport — no new SDK dependency. See [Chapter 7 §"Step 2"](book/src/07-quick-start.md#step-2--roksbnkctl-init).
- **`roksbnkctl cleanup`** — a recovery command for a `down` that errored partway and stranded cloud resources. It sweeps the account for everything named after the workspace prefix (`<prefix>-*`) — instances, floating IPs, public gateways, subnets, security groups, VPCs, the Transit Gateway, registry COS, the ROKS cluster, and the BNK trusted profile — and deletes them in dependency order. Best-effort, always lists before deleting; `--dry-run` / `--auto` / `--region` / `--all-regions`. Fixes the "`… name is not unique`" wall where a half-deleted resource blocked the next `up`. See [Chapter 11](book/src/11-tearing-down.md).
- **`roksbnkctl gateway up / down`** — a new **optional** phase command that applies (and tears down) the BNK **data-plane config** in its own state (`state-gateway/`): the Gateway API objects (GatewayClass, F5BnkGateway, Gateway, HTTPRoute), the egress SnatPool + Egress CRs, the per-zone static routes, and the cluster security-group VXLAN rule. It reuses the existing cluster + BNK (via `cluster-outputs.json`) and is **not** run by the composite `up`/`down` — you run it explicitly after BNK is healthy. All CR shapes/defaults come from the BNK 2.3 install guide (overridable via the `config.yaml` `gateway` block). Documented in [Chapter 8a](book/src/08a-three-phase-lifecycle.md).
- **`roksbnkctl testing up / down / migrate`** — a new top-level phase command that provisions and destroys the testing jumphosts (TGW jumphost, per-AZ cluster jumphosts, client VPC) independently of BNK. Pure IBM VPC — no Kubernetes. **Not to be confused with `roksbnkctl test` / `test hosts`**, which *run* connectivity/DNS/throughput probes and provision nothing. `testing migrate` moves a pre-split workspace's jumphosts out of the combined BNK state into `state-testing/` (a `terraform state mv` — no cloud churn, jumphosts keep their IPs/known_hosts).
- **Parallel `up`** — a fresh `roksbnkctl up` provisions the Cluster phase first, then brings up **BNK and Testing concurrently** (line-prefixed `[bnk]` / `[testing]` output). Without `--auto`, `up` plans both phases first (each diff cleanly attributed) and asks a **separate confirmation for each** before applying the approved phases in parallel; a phase whose plan is a no-op is skipped without prompting.
- **The BNK phase now creates the `cloud-network-mapping` ConfigMap + the external/internal VLANs.** The CNE controller reads `CLOUD_NETWORK_CONFIGMAP=cloud-network-mapping` for the zone→CIDR map that programs TMM's data-plane networking, but terraform never created it (and env-discovery defaults off) — so TMM's `RoutingDone` readiness gate never flipped. The cne_instance module now applies that ConfigMap **before** the CNEInstance, plus the external/internal `F5SPKVlan` CRs (TMM self-IPs) after the CNEInstance is Available. Zone **names** derive from the region (`<region>-1/2/3`); CIDRs + self-IPs come from a new `cneinstance_network_zones` variable (install-guide defaults, overridable via the `config.yaml` `bnk.network.zones` block).
- **`alekc/kubectl` terraform provider dependency** — the BNK custom resources are applied as `kubectl_manifest` resources from `alekc/kubectl` (`>= 2.4.0`; resolves `2.4.1`), the one provider that applies a CR whose CRD is installed *in the same apply* without a plan-time schema lookup. Co-resolves `hashicorp/helm` (`~> 2.12`) and `hashicorp/kubernetes` (`>= 2.25`). **Air-gap caveat**: offline installs must pre-stage the providers via a `terraform providers mirror` bundle (a `provider_installation { filesystem_mirror }` block in the terraform CLI config); `.terraform.lock.hcl` records the exact hashes so the mirror is reproducible. See [Chapter 10 §"The terraform-native deployment model"](book/src/10-deploying-bnk-trials.md#the-terraform-native-deployment-model).
- **`--legacy-bnk` flag on `bnk up` / `bnk down` + the `bnk_cr_mode` install-mode tfvar** — the legacy `null_resource`/`curl`/`time_sleep` path is kept intact behind an install-mode switch as a benchmark baseline and a transition fallback. `bnk_cr_mode = "kubectl"` (default) selects the terraform-native path; `"legacy_curl"` selects the old one. Rendered from the optional `bnk.cr_mode` workspace-config key; the flag overrides it for a single run (must match between `bnk up` and the corresponding `bnk down`).

### Changed

- **BNK is now terraform-native.** The trial layer used to mutate the cluster from inside terraform through `null_resource` + `local-exec` shell (raw `curl` SSA per custom resource, `helm upgrade --install` shell-out, ≈210 s of fixed `time_sleep`). It is replaced by proper providers: chart installs → `helm_release` (in-process `hashicorp/helm`, no host `helm` shell-out); custom resources → `kubectl_manifest` (`alekc/kubectl`) with `wait_for` blocks that watch the live object's real `.status`; namespaces/Secrets → `hashicorp/kubernetes`. No Go reconciler, no custom provider — terraform stays the state keeper. The CRs are now **real terraform state**: `plan` diffs the exact changed fields, drift is detected, an unchanged re-apply is a true no-op, and `bnk down` deletes the CRs finalizer-aware with no destroy-time `curl`.
- **Readiness is now gated on real `.status`, not fixed sleeps.** The ≈210 s of unconditional `time_sleep` (cert-manager-ready, two SCC-propagation waits, the CNEInstance/License-CRD waits, a 60 s flo-pods wait) is **deleted** from the default path. `helm_release wait = true` and `kubectl_manifest wait_for` return the instant the live object reports ready and no sooner; dropping over-conservative `depends_on` edges also lets the independents (NADs, Secrets, SCC bindings, node-labeler) apply concurrently.
- **Workspace selection follows you.** Creating a workspace (`init` or `ws new`) now makes it current, and `ws delete` no longer refuses the current workspace — it deletes it and moves the pointer to another existing workspace (or clears it when none remain). With nothing selected there is **no phantom `default`**: commands report `no workspace selected` instead of silently operating on a `default`. The end-to-end test's parking-lot dance is retired. See [Chapter 6](book/src/06-workspaces.md).
- **Independent phase teardown.** `roksbnkctl bnk down` leaves the testing jumphosts in place (and `testing down` leaves BNK), so you can reset the BNK trial (`bnk down && bnk up`) and reuse the same jumphosts. Bare `roksbnkctl down` destroys BNK ∥ Testing (parallel) then the Cluster, behind a single composite confirmation. `roksbnkctl cluster down` refuses while any BNK / Testing / Gateway state still has resources (they reference the cluster VPC/TGW) — `--auto` does not bypass that guard. `cluster-outputs.json` is deleted only on `cluster down`.
- **The `cluster up` phase no longer creates cert-manager or the jumphosts** — it creates only the durable cluster-shared infrastructure (ROKS cluster + transit gateway + registry COS). cert-manager moved to the BNK phase (it became provider-based and can't authenticate during cluster creation); the jumphosts moved to the Testing phase. The `cluster-outputs.json` handoff gained a `TransitGatewayName` field so the standalone Testing phase can look the transit gateway up by name.

### Performance

- **Shared terraform provider plugin cache.** roksbnkctl points terraform at a single `TF_PLUGIN_CACHE_DIR` under `~/.roksbnkctl/plugin-cache/`, so each provider (the ~440 MB set, incl. the 158 MB IBM provider) downloads **once** and every phase/workspace links it from the cache instead of re-fetching per phase. Combined with terraform installing providers at deploy time (rather than shipping them in the binary), the first `up` populates the cache and subsequent phases/workspaces init near-instantly.

### Fixed

- **The terraform-native BNK phase reaches a licensed, Ready TMM on its own**, and the full deploy/teardown cycle is reliable — proven by a 10-cycle live soak. The issues fixed along the way:
  - **Deadlock** — the License CR waited on the CNEInstance being `Available`, but `Available` needs TMM and TMM needs the License; the CNEInstance wait now gates on `CNEControllerAvailable` (pre-License) so the License applies and TMM comes up.
  - **License `wait_for`** matched a stale `status.state` string and hung — it now gates on the `LicenseActive` condition.
  - **count=0 cluster "husk"** in the BNK state was misread as a legacy single-state workspace (the root cause of cross-phase state corruption) — detection now requires ≥1 instance.
  - **Gateway-API CRD admission policy** — the OpenShift ingress operator recreates the blocking `ValidatingAdmissionPolicyBinding` faster than the crd-installer creates the CRDs BNK requires; the up-front delete is now a detached delete-loop, with longer CNEInstance/License create timeouts.
  - **`bnk down` dialed `http://localhost`** — it rendered `create_roks_cluster=true`, so the k8s/helm/kubectl providers got an empty host. `bnk down` now re-asserts the cluster-reuse override (like the other downs); and `phaseLabel` gained a `state-gateway` case so `gateway up` no longer clobbers the BNK applied-tfvars snapshot.
  - **Teardown order** — `bnk down` / composite `down` now refuse while the Gateway phase has resources (its CRs live in the BNK namespace and would hang the teardown), pointing at `gateway down` first.
  - **F5 validating-webhook race** — a fresh `up` could apply the F5SPKVlan CRs before the `f5validate` webhook served TLS; `applyWithRetry` now treats that as transient and retries.
- **The FLO/CIS chart pulls authenticate to `repo.f5.com`.** The `helm_release.flo` / `.cis` pulls passed no registry credentials, so the in-process helm provider pulled anonymously and the registry returned **403 Forbidden**. The terraform-native path now passes the FAR service-account credentials via `repository_username` / `repository_password`.
- **FLO/CIS `helm_release` no longer blocks on helm-level readiness** (`wait = false`, matching the legacy `--wait=false`) — `wait = true` raced the cert-manager-issued webhook certs applied later and timed out (`context deadline exceeded`). cert-manager keeps `wait = true`; real readiness is gated downstream by the CNEInstance / License `wait_for`.
- **All BNK `kubectl_manifest` resources set `force_conflicts = true`.** The FLO operator co-manages fields on the CRs it reconciles (e.g. the CNEInstance's `.spec.advanced.cneController.env`), so SSA failed with `conflict with "f5-lifecycle-operator"` once the operator took ownership. Added to all 14 roksbnkctl-managed `kubectl_manifest` resources — a no-op without a conflict, field-level SSA ownership when there is one.
- **The embedded terraform bundle no longer drags in the `.terraform/` provider cache.** `//go:embed all:terraform` pulled in dotfiles, baking a dev machine's gitignored ~440 MB `.terraform/` plugin cache into the binary (~670 MB; the providers also extracted `0644` → `fork/exec … permission denied`). The directive is now plain `//go:embed terraform`; the binary drops to ~100 MB and providers resolve at init. `tf.Open` also self-heals an already-poisoned workspace by chmod +x'ing non-executable provider binaries.
- **Parallel `up` no longer fails with `Backend configuration block has changed`.** The data dir was a process-global `TF_DATA_DIR` (`os.Setenv`) that the BNK ∥ Testing legs raced; it now defaults into each phase's distinct per-phase source dir, so concurrent applies are isolated.

### Notes

- **The terraform-native path is now the default** (`bnk_cr_mode = "kubectl"`); the legacy `curl` modules stay byte-for-byte intact behind `legacy_curl` as a benchmark baseline and rollback path. A later release removes the legacy path.
- **IBM IAM trusted-profile + COS reads stay in terraform, unchanged** — they are IBM-Cloud-native terraform, not Kubernetes mutations. FAR version discovery (the `helm pull` that reads chart versions) also stays terraform-side.
- **`roksbnkctl doctor` still requires host `helm`** — the default path no longer shells out to it, but the `legacy_curl` path does, so the requirement stays until the legacy path is removed.

## v1.8.0 — 2026-06-04

Sprint 26 — **prefix-driven, collision-free resource naming.** Two `roksbnkctl` workspaces that both create infrastructure in the same IBM Cloud account no longer collide on resource names. Previously every workspace rendered the *same* upstream module default names (`tf-openshift-cluster`, `tf-cluster-vpc`, `tf-tgw`, …), so the second workspace to `up` hit `Provided Name … is not unique` / `gateway with the same name already exists` — the collision class that stranded the `canada-roks-*` resources behind the 2026-05-28 cleanup incident. `init` now asks for a single workspace **prefix** and generates every account-scoped resource name from it. **Backward compatible**: existing workspaces (whose `config.yaml` has no `prefix:`) are unaffected — they keep the old sparse render and the upstream default names, byte-for-byte. The `--var-file` / `terraform.tfvars.user` override path is unchanged and still wins over any generated name. See [PLAN.md §"Sprint 26"](docs/PLAN.md), [`issues/issue_sprint26_architect.md`](issues/issue_sprint26_architect.md), [`issues/issue_sprint26_staff.md`](issues/issue_sprint26_staff.md), [`issues/issue_sprint26_validator.md`](issues/issue_sprint26_validator.md), and [`issues/issue_sprint26_tech-writer.md`](issues/issue_sprint26_tech-writer.md).

### Added

- **Prefix-driven resource naming prevents cross-workspace name collisions** ([`issues/issue_sprint26_staff.md` Issue 1](issues/issue_sprint26_staff.md)) — `roksbnkctl init` now collects a single workspace **prefix** and derives every account-scoped IBM Cloud resource name from it: cluster `<prefix>`, cluster VPC `<prefix>-cluster-vpc`, registry COS `<prefix>-registry-cos`, Transit Gateway `<prefix>-tgw`, client VPC `<prefix>-client-vpc`, TGW jumphost `<prefix>-jh-tgw`, and per-zone cluster jumphosts `<prefix>-jh-<zone>`. The names are deterministic, so `roksbnkctl` re-derives them on every `up` / `plan` / `apply` and renders a complete `terraform.tfvars` — a faithful record of exactly what the tool asks IBM Cloud to create. Give each workspace a distinct prefix and two workspaces in the same account never collide. The cluster name deliberately *equals* the prefix (no suffix) so the prefix-length limit is the tightest resource limit (the 35-char ROKS cluster cap); a valid prefix guarantees every derived name fits. See the new [Chapter 13 §"Resource naming & collision avoidance"](book/src/13-terraform-variables.md#resource-naming--collision-avoidance).
- **Length + charset validation at `init` time** ([`issues/issue_sprint26_staff.md` Issue 1](issues/issue_sprint26_staff.md)) — the prefix (and every name derived from it) is validated against IBM Cloud's own per-resource-type limits (cluster ≤ 35, IS resources ≤ 63, COS ≤ 180; lowercase label charset) *before* anything is provisioned, so an over-long or malformed name is rejected up front with an actionable message — the offending resource, its computed length, its limit, and the maximum allowable prefix length — and `init` re-prompts (or hard-errors in a non-TTY context). No more discovering a bad name minutes into a `terraform apply`. There is **no silent truncation or hashing**.
- **`init` interview rewrite: prefix prompt + per-resource create toggles + existing-resource adoption** ([`issues/issue_sprint26_staff.md` Issue 1](issues/issue_sprint26_staff.md)) — the no-`--var-file` interview now asks for the prefix, then a short series of create/adopt toggles (ROKS cluster, registry COS, Transit Gateway, cert-manager, BNK, TGW test jumphost + its client VPC, per-zone cluster jumphosts). Declining a resource that a still-enabled resource depends on (e.g. declining the Transit Gateway while keeping the TGW jumphost) prompts for the existing resource's name to adopt instead. `init` prints the resolved name plan to stderr before saving so you see exactly what will be created vs. adopted. The new `config.yaml` carries an additive `prefix:` field and a `resources:` block of `{create, existing}` toggles — both optional and `omitempty`, so old configs load without migration. Documented in [Chapter 12 §"Worked example"](book/src/12-workspace-config.md#worked-example-bootstrap-a-workspace-from-scratch) and the [Chapter 28 configuration reference](book/src/28-configuration-reference.md#resources-block).

### Changed

- **A workspace with a `prefix` now renders a complete, explicitly-named `terraform.tfvars`** ([`issues/issue_sprint26_staff.md` Issue 1](issues/issue_sprint26_staff.md)) — pre-`v1.8.0` the generated tfvars was *sparse* (it omitted every resource-name variable and let the upstream module defaults take over), which is exactly why every workspace shared the same names. A prefix-set workspace now emits the full de-duplicated variable set (each variable exactly once); a created resource gets its prefix-derived name, a declined-but-depended-on resource gets the operator's adopted name. **Override path preserved**: every generated name is just a value in the rendered `terraform.tfvars`, so `~/.roksbnkctl/<ws>/terraform.tfvars.user` and `--var-file` still layer last and override any name (the Sprint 19 layering is unchanged). The separate second-phase `bnk-phase-override.tfvars` handoff still layers last as its own `-var-file` and is untouched. `init --var-file` keeps its Sprint 19 behaviour and additionally seeds a sanitized `prefix` + a default all-create `resources:` block so the generated base is collision-safe while the supplied file still overrides via layering.

### Notes

- **Backward compatibility is explicit and tested.** An existing pre-`v1.8.0` `config.yaml` has no `prefix:` field; `roksbnkctl` detects the empty prefix and renders the **old sparse `terraform.tfvars`** unchanged — no names emitted, upstream module defaults in force, byte-for-byte the prior behaviour. To opt an existing workspace into prefix-derived names, re-run `roksbnkctl init -w <ws>` and answer the prefix prompt.
- **Namespaces are not prefixed.** Kubernetes namespaces (`cert-manager`, `f5-bnk`, `f5-utils`) keep their conventional fixed values — they are cluster-internal and only collide on a *shared* cluster, where a shared namespace is what FLO and the BNK charts expect. Only account-scoped IBM Cloud infrastructure names are prefixed.
- **Detection complement.** Prefix-derived naming *prevents* the collisions; the forward-looking `roksbnkctl doctor --orphan-sweep` diagnostic (tracked for a later release — see [`issues/issue_sprint25_staff.md`](issues/issue_sprint25_staff.md)) *detects* already-stranded resources by deriving the same `<prefix>-cluster-vpc` / `<prefix>-tgw` formulas this release makes canonical.

## v1.7.1 — 2026-05-27

Patch release bundling **three sprints** that were drafted, dispatched, and closed in a single 2026-05-27 cycle after a presenter `demo.sh` re-verify of the `v1.7.0` release exposed two operator-visible bugs in the cluster/trial phase-split dispatch (PRD 06) and one CLI ergonomic gap. All three sprints ship together to keep the `roksbnkctl bnk down` resource-damage hazard closed in lockstep with the heuristic fix that exposes it. **No breaking surface**; all changes are bug fixes + one additive CLI surface. See [PLAN.md §"Sprint 22"](docs/PLAN.md), [PLAN.md §"Sprint 23"](docs/PLAN.md), and [PLAN.md §"Sprint 24"](docs/PLAN.md).

### Changed

- **`roksbnkctl down` on a Split-shape workspace (cluster + bnk both deployed) now takes ONE up-front confirmation that names both phases, then destroys trial → cluster without re-prompting** ([Sprint 22 — `issues/issue_sprint22_staff.md` Issue 2](issues/issue_sprint22_staff.md)) — pre-`v1.7.1`, the composite teardown emitted two confirmation prompts: one in `RunTrialDown` (`"This will destroy workspace \"X\"'s resources."`) and a second in `runClusterDown` (`"This will destroy the cluster phase for workspace \"X\" (ROKS + transit gateway + registry COS + cert-manager + jumphost)."`). Both defaulted to **No**, so operators who said `yes` to the first and walked away (or hit Enter on the second assuming one Yes covered both) ended up with the BNK trial gone and the cluster still running — silently. The new combined prompt is `"This will destroy BOTH the BNK trial AND the cluster phase for workspace \"X\" (ROKS + transit gateway + registry COS + cert-manager + jumphost)."` followed by a single `Continue? [y/N]:`. Implementation: `internal/orchestration/lifecycle.go`'s `RunDown` Split branch takes the prompt, flips `in.Auto = true`, and the cli adapter's `RunClusterDown` closure (`internal/cli/lifecycle.go`'s `lifecycleInputs`) mirrors `in.Auto` onto `flagAuto` for the cluster-down call's duration so the still-cli-resident `runClusterDown` reads the post-prompt state. LegacySingle, ClusterOnly, Empty, and standalone `bnk down`/`cluster down` flows are unchanged.

### Fixed

- **`roksbnkctl down` / `bnk down` against a freshly-applied Split workspace no longer misclassifies it as `legacy-single-state`** ([Sprint 22 — `issues/issue_sprint22_staff.md` Issue 1](issues/issue_sprint22_staff.md)) — pre-`v1.7.1`, `internal/config/tfstate.go`'s `trialStateHasClusterModules` heuristic matched any resource under `module.roks_cluster` / `module.cert_manager` / `module.testing` prefixes without filtering on `mode` or `type`. A normal post-`up` Split trial state legitimately carries data-source refreshes under those prefixes (the BNK trial modules read cluster info via data lookups), which tripped the classifier and routed `RunDown` to the LegacySingle branch (`return RunTrialDown(ctx, in)`) — destroying only the trial phase and exiting 0 without chaining to cluster destroy. The narrowed criterion now requires ALL three: `mode == "managed"` AND `type == "ibm_container_vpc_cluster"` (the ROKS cluster itself, the unambiguous v1.0.x marker) AND a cluster-phase module prefix match. Data sources, stray `tls_private_key` instances, and stray `ibm_resource_instance` COS objects under cluster-phase prefixes no longer trip legacy classification. PRD 06 §"Design" rewritten to describe the narrower criterion accurately; new fixture `internal/config/testdata/tfstate_split_data_in_trial.json` + two new helper tests pin the mode + type filters.

- **`roksbnkctl bnk down` no longer destroys cluster-shared infrastructure** ([Sprint 23 — `issues/issue_sprint23_staff.md`](issues/issue_sprint23_staff.md), live-verified GREEN with residual on 2026-05-27 against canada-roks). Two rounds of fixes; closure paragraph in the issue file documents both. **Round 1**: `tls_private_key.jumphost_shared_key` (previously ungated → always created in trial state on every `up`) is now gated `count = (var.testing_create_cluster_jumphosts || var.testing_create_tgw_jumphost) ? 1 : 0`; the `bnk-phase-override.tfvars` produced by `writeAndInitSecondPhase` (when `cluster-outputs.json` exists, indicating cluster phase already deployed cluster-shared infra) gained `create_roks_registry_cos_instance = false` so the registry COS instance no longer lands in trial state. **Round 2** closed the broader leak class the 2026-05-27 live verify surfaced — the outer `terraform/modules/cert_manager/main.tf:35` hardcoded `enabled = true` on the inner cert-manager submodule (whose null_resources carry a destroy provisioner that runs `kubectl delete namespace cert-manager`, wiping cert_manager + every cert it issued on a subsequent `bnk down`); a new `var.deploy_cert_manager` (default `true`) is now plumbed through the outer wrapper + root + bnk-phase override forces it `false`. `ibm_is_security_group_rule.cluster_sg_inbound_all` in `terraform/modules/roks_cluster/modules/cluster/main.tf` gained `count = var.create_cluster ? 1 : 0` (was unconditional). The flo module's `null_resource.ca_certificate` template interpolation (`flo/modules/flo/main.tf:364-365`) was blowing up on the now-null `module.cert_manager.cert_manager_namespace` output; `terraform/main.tf:86` switched to passing `var.cert_manager_namespace` directly (always defined). Five `jumphost_user_data` locals in `terraform/modules/testing/main.tf:39-194` gained `length(...) > 0 ? ... : ""` guards so they no longer index `[0]` into the count-flipped empty `tls_private_key.jumphost_shared_key` tuple. **Live-verify result**: `jq '.resources[] | select(.mode == "managed" and (.module | startswith("module.roks_cluster") or startswith("module.testing") or startswith("module.cert_manager")))' ~/.roksbnkctl/<ws>/state/terraform.tfstate` returns **2 entries** (down from 6 pre-fix). Both are inert `null_resource.roks_cluster_gate` bootstrap entries (one in `module.cert_manager`, one in `module.testing`) with `triggers = { dep = "direct-apply" }` and NO destroy provisioner — destroying them via `bnk down` removes them from state only, zero cloud impact, zero cluster impact. The four catastrophic leaks (`cert_manager_namespace`, `cert_manager`, `cert_manager_ready`, `cluster_sg_inbound_all`) are closed. A future cleanup adding `count = var.create_roks_cluster ? 1 : 0` to the two bootstrap gates would close the residual; deferred as a non-safety-critical Sprint 25+ candidate.

### Added

- **`roksbnkctl test hosts {list,add,remove,clear}` CLI surface** ([Sprint 24 — `issues/issue_sprint24_staff.md`](issues/issue_sprint24_staff.md)) — first-class management of the workspace's `test.connectivity.extra_hosts` slice (the field that drives both `roksbnkctl test connectivity` and the no-flag workspace-driven `roksbnkctl test dns`). Mirrors `roksbnkctl targets {list,add,remove}`' ergonomic byte-for-byte where it makes sense: `list` (`cobra.NoArgs`; newline-separated URLs on stdout; `--output json` emits the slice as a JSON array; empty list → zero bytes + exit 0), `add <url> [<url>...]` (`cobra.MinimumNArgs(1)`; `url.Parse` validation; idempotent + log on already-present), `remove <url> [<url>...]` (`cobra.MinimumNArgs(1)`; idempotent + log on absent; preserves remaining order), `clear` (`cobra.NoArgs`; confirmation prompt defaults to No, `--auto` skips). Persistence routes through a new `mutateExtraHosts` helper wrapping the existing `config.LoadWorkspace` / `config.SaveWorkspace` round-trip. The `"no hosts configured to probe"` error message in `internal/cli/test.go:803` now points operators at `roksbnkctl test hosts add <url>` instead of telling them to hand-edit `config.yaml`. New book subsection at [chapter 20 §"Managing test hosts via the CLI"](book/src/20-connectivity-testing.md) with a worked example; cross-links from the `test connectivity` and `test dns` chapter sections. Scope is intentionally tight to `extra_hosts` only — `test.dns.{default_target,resolvers}` and `test.throughput.*` keep their existing flag-driven equivalents (`--target`, `--server`, `--duration`, etc.) and a parallel CLI for them is a separate scope decision deferred to a future sprint.

- **`mdbook` Docker image is now CI-managed** ([Sprint 22 — `issues/issue_sprint22_validator.md`](issues/issue_sprint22_validator.md)) — `.github/workflows/tools-images.yml` gained `mdbook` in its `strategy.matrix.image` list alongside `ibmcloud` and `iperf3`. Every `main` push now auto-publishes `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`; every `v*` tag push publishes `:<tagname>` + `:latest`. Pre-`v1.7.1`, the mdbook image was manually built + pushed by the integrator on each Dockerfile edit, and a fresh contributor running `make book-pdf BOOK_BACKEND=docker` could pull a stale image (or, before 2026-05-21, hit a 401 because the package didn't exist publicly). `CONTRIBUTING.md` updated at three sites to reflect the CI-managed flow; the `make build-mdbook` target on the host still works for local iteration.

### Notes

- **Closure under `live-verify-high-issues`**: Sprint 22's DetectShape fix could not be live-verified in isolation because the misclassification was masking the Sprint 23 phase-separation leak — verifying one in the absence of the other was structurally unsafe. The combined `v1.7.1` cut bundles both behind a single live-verify cycle (2026-05-27, canada-roks), which is why this release ships three sprints worth of fixes rather than one. Sprint 24 is UX-only and hermetic-test sufficient; it rode along to remove the YAML-editing pre-step from the demo flow.
- **Sprint 23 lessons**: three rounds of live verify were needed to close the phase-separation leak. The hermetic test class pinned the override CONTENT but never ran `terraform plan` against the integrated HCL + state, so the first two rounds shipped passing tests + a still-broken trial-state shape (Invalid index in locals, Invalid template interpolation value in flo's `ca_certificate`). A small `terraform plan -refresh=false` sanity gate against fixture state would catch this class hermetically next time; filed as Sprint 25+ candidate.
- **CHANGELOG entry length for this release** is intentionally longer than the per-sprint norm because three sprints are bundled; subsequent releases revert to the one-sprint-per-tag cadence.

## v1.7.0 — 2026-05-21

Sprint 21 — cobra/pflag argv-parser strictness, first regular work cycle post-`v1.6.4`. **Behavioural change**: short flags no longer accept the stuck-together `-fvalue` form; the binary now rejects it at parse time with an actionable error before any `RunE` / workspace mutation / IAM call. Motivated by a 2026-05-21 live-use observation in which `roksbnkctl init -ws canada-roks --var-file ./terraform.tfvars` silently parsed as `-w s` (workspace `s`) with the positional `canada-roks` dropped, creating a workspace named `s` carrying the right cluster identity, and a follow-on bare `cluster up` then resolved to a stale `current_workspace: default` and rendered a 25-resource-destroy / 41-resource-add plan against real cloud state (caught at plan review, not after apply). The follow-on `current_workspace` stale-resolution UX is a separate concern that **Sprint 21 does NOT close** — the strictness work surfaces the typo at the bad argv so the misparse never propagates to that point in the first place. See [PLAN.md §"Sprint 21"](docs/PLAN.md), [`issues/issue_sprint21_staff.md`](issues/issue_sprint21_staff.md), [`issues/issue_sprint21_architect.md`](issues/issue_sprint21_architect.md), [`issues/issue_sprint21_validator.md`](issues/issue_sprint21_validator.md), and [`issues/issue_sprint21_tech-writer.md`](issues/issue_sprint21_tech-writer.md).

### Changed

- **BREAKING for any operator using stuck-together short-flag-values: short flags now require `-f value` (space) or `-f=value` (equals); the `-fvalue` form is rejected at parse time** ([`issues/issue_sprint21_staff.md` Issue 1](issues/issue_sprint21_staff.md)) — pre-`v1.7.0`, cobra/pflag accepted `-fvalue` and silently parsed it as `-f` with value `value`. This produced a silent-misparse class: `roksbnkctl init -ws canada-roks --var-file …` parsed as `-w s` (workspace `s`) with the positional `canada-roks` silently dropped, allowing the typo to propagate through `init` and a follow-on `cluster up` before surfacing as a wrong-workspace destroy plan against real cloud state. A new pre-parse argv preflight in `cmd/roksbnkctl/main.go` walks the cobra tree at process start, collects the set of value-requiring short flags from the binary's live flag set (no hand-maintained typo list — pflag's `NoOptDefVal == ""` marker is the discriminator), and rejects any argv token of the shape `-X<suffix>` (where `X` is value-requiring, `<suffix>` is non-empty, and `<suffix>[0]` is not `=`) with an actionable error that names the offending token, both acceptable shapes (`-X value` / `-X=value`), the long-flag equivalents (`--longname value` / `--longname=value`), and a "did you mean" suggestion derived from the binary's own flag set. The rejection runs BEFORE `cli.Execute()`, so no workspace dir, IAM call, or filesystem mutation precedes the error (validator's hermetic test pins the no-workspace-dir property via `os.Stat` under `t.TempDir()`). Operators who were typing `-wcanada-roks` / `-fpath/to/file` / `-nf5-bnk` / similar must switch to `-w canada-roks` / `-f path/to/file` / `-n f5-bnk` (space) or the equivalent `=` forms. Boolean shorts (`-v`, `-q`, `-i`, `-t`, `-A`) and bool-stacking (`-it`, `-vvv`) are **unaffected** — the preflight skips them via the `NoOptDefVal != ""` marker. Passthroughs (`roksbnkctl kubectl …`, `oc`, `ibmcloud`, `terraform`, `exec`) keep `DisableFlagParsing` semantics — argv after the subcommand name flows to the wrapped tool untouched. See the new "Flag-value syntax" callout in [book chapter 7](book/src/07-quick-start.md) and the per-command reference in [book chapter 27](book/src/27-command-reference.md).
- **Commands that don't take positionals now reject stray positionals at parse time** ([`issues/issue_sprint21_staff.md` Issue 1](issues/issue_sprint21_staff.md)) — companion to the argv strictness change above; catches the OTHER half of the original failure mode (the silently-dropped `canada-roks` positional). 32 cobra commands gained `Args: cobra.NoArgs`: `init`, `up`, `plan`, `apply`, `down`, `bnk up`/`down`, `cluster up`/`down`/`show`, `cos instance list`, `cos bucket list`, `status`, `install`, `k apply`, `version`, `self update`, `doctor`, `ops install`/`show`/`uninstall`, `targets list`, `test connectivity`/`dns`/`throughput`/`list`, `tfvars`, `workspaces list`/`current`, `shell`, `kubeconfig`. Commands that DO accept positionals (`ws delete <name>`, `cos object get <key> <local>`, `cos bucket get <bucket> <local-dir>`, `cluster register [name]`, `targets show`/`add`/`remove`, `k get`/`describe`/`delete`/`logs`/`exec`/`port-forward`, `logs <component>`, etc.) preserve their existing `Args:` constraints byte-identically.

## v1.6.4 — 2026-05-21

Sprint 19 — first regular work cycle post-`v1.6.3`. Closes the residual UX gap manual testing of `v1.6.3` exposed: between `roksbnkctl init` and the workspace's first successful `up --var-file <path>`, bare `<verb> -w <ws>` commands still failed with `No value for required variable …` because no var-file existed yet for the lifecycle to layer in. `roksbnkctl init --var-file <path>` persists the operator's tfvars at init time so every subsequent bare `-w <ws>` command Just Works. Live-verified GREEN against a real IBM Cloud account (run-id `20260521-031343`); the round-1 hermetic cycle caught two design bugs (file written to the wrong path; the Sprint 16 actionable-error gate didn't honor the new persistence path) which the round-2 fixes resolved. See [PLAN.md §"Sprint 19"](docs/PLAN.md), [`issues/issue_sprint19_staff.md`](issues/issue_sprint19_staff.md), [`issues/issue_sprint19_architect.md`](issues/issue_sprint19_architect.md), and [`issues/issue_sprint19_validator.md`](issues/issue_sprint19_validator.md).

### Added

- **`roksbnkctl init --var-file <path>`** ([`issues/issue_sprint19_staff.md` Issue 1](issues/issue_sprint19_staff.md)) — point `init` at an existing tfvars file (shaped like `terraform.tfvars.example`) to skip the interactive interview for every field the file already answers AND persist the file at the workspace root as `terraform.tfvars.user` (mode `0600`, sibling to `config.yaml`). Every subsequent bare `roksbnkctl up`/`plan`/`apply`/`down -w <ws>` auto-layers the persisted file — no need to re-pass `--var-file` on every command. The single workspace-root copy serves **both** the trial and cluster phases (the lifecycle's `tf.Workspace.UserTFVarsPath()` resolves to `<workspace-dir>/terraform.tfvars.user` regardless of which phase opened the workspace, so one file covers both). Maps the interview-targeted fields (`ibmcloud_cluster_region`, `ibmcloud_resource_group`, `openshift_cluster_name`, `openshift_cluster_version`, `roks_workers_per_zone`, `create_roks_cluster`) into `config.yaml`; `ibmcloud_api_key` lands verbatim on disk via the file copy but is not mapped into `config.yaml` (owned by the cred resolver). A re-`init` with a different `--var-file` overwrites the prior copy with a brief `note: replacing existing …` stderr line. The Sprint 16 `roksbnkctl: this workspace has no terraform.applied.tfvars snapshot …` actionable-error gate now honors the `init --var-file`-seeded `terraform.tfvars.user` as a valid input source, and its error message names BOTH remedies (the existing `--var-file` flag + the new `init --var-file` flow). Live-verified end-to-end: `init --var-file ./terraform.tfvars` → bare `plan -w <ws>` exits 0 with the `→ Layering user tfvars from <path>` line in stderr confirming the codepath fired. See the new §"Skip the interview: `init --var-file`" in [book chapter 6](book/src/06-workspaces.md) and the auto-generated reference in [book chapter 27](book/src/27-command-reference.md).

## v1.6.3 — 2026-05-20

Sprint 18 — first regular work cycle post-`v1.6.2`. Two scope items captured from the former GitHub Issues backlog into local issue ledgers (the two GitHub issues were then deleted; the local Sprint 18 ledger is the source of truth for in-flight work), plus two pre-existing defects in the shared `cos` command path that manual testing surfaced after the cos work landed. All four user-visible items live-verified GREEN against a real IBM Cloud account. See [PLAN.md §"Sprint 18"](docs/PLAN.md), [`issues/issue_sprint18_staff.md`](issues/issue_sprint18_staff.md), and [`issues/issue_sprint18_architect.md`](issues/issue_sprint18_architect.md).

### Added

- **`roksbnkctl cos bucket get <bucket> <local-dir> --instance <name|CRN>`** ([`issues/issue_sprint18_staff.md` Issue 1](issues/issue_sprint18_staff.md)) — recursive streaming download of every object in a COS bucket to a local directory. Symmetric with the existing `cos bucket {create,list,delete}` group and `cos object get`. Object keys that contain `/` map to nested subdirectories under the destination (`foo/bar/baz.json` lands at `<dest>/foo/bar/baz.json`). Streaming all the way down — works for binaries of any size, no in-memory buffering. `--no-clobber` skips objects whose local target already exists (default overwrites). Empty bucket exits 0 with an informational stderr line, no filesystem changes. End-of-run stderr counters: `N objects, M bytes, K skipped`. JSON output (`--output json`) emits one JSON object per file completed (key, local path, size, etag, outcome). Honors the existing global flags (`--workspace`, `--output`, `--quiet`, `--verbose`, `--on`, `--backend`). Live-verified GREEN against `bnk-schematics-resources` (us-south): 9 objects round-tripped byte-identical (sha256 match per file). See the new `### cos bucket get` example in [book chapter 25](book/src/25-cos-supply-chain.md) and the auto-generated reference in [book chapter 27](book/src/27-command-reference.md).

### Fixed

- **`roksbnkctl cos object list/get` no longer 404s on a populated cross-region bucket** ([`issues/issue_sprint18_staff.md` Issue 3](issues/issue_sprint18_staff.md)) — pre-fix, `cos object list bnk-schematics-resources --instance bnk-orchestration` (and every other `cos` verb against a bucket that lived in a region different from the workspace's cluster region) returned `NoSuchBucket 404` from IBM Cloud even though the bucket clearly contained objects. The shared `internal/cos/client.go` constructed a single S3 handle pinned to the workspace cluster region and routed every per-bucket operation through it; the IBM COS S3 API is endpoint-scoped, so `s3.ca-tor.cloud-object-storage.appdomain.cloud` genuinely has no idea about a us-south bucket. The Client now carries a `BucketRegionResolver` seam + a per-bucket region cache + a lazy per-region S3-handle map: the first time the Client touches a bucket it discovers the bucket's actual region via a parallel HeadBucket fan-out across candidate regions (matching the shape IBM's own `ibmcloud cos` CLI uses internally per `bucket_class_location.go::getBucketLocationCoordinator`), caches the result, and routes that bucket's subsequent operations through the right regional endpoint. Same-region workspaces are byte-identical to before. Live-verified: the failing reproducer now returns the 9-object listing.
- **`roksbnkctl cos *` commands are ~47× faster** ([`issues/issue_sprint18_staff.md` Issue 2](issues/issue_sprint18_staff.md)) — `cos object list bnk-schematics-resources --instance bnk-orchestration` was taking **~88 seconds** wall-clock vs `ibmcloud cos objects`'s 1.4 seconds. After three rounds of hardening the shared `internal/cos/client.go` (single client per invocation; shared IAM credentials across regional handles; per-region handle cache; parallel HeadBucket fan-out for region resolution) the live wall-clock was still ~88s — round-5's profiler-driven investigation found the cost was upstream of the COS client entirely: `internal/ibm/cos_instance.go::ListCOSInstances` was paginating the IBM Cloud Resource Controller v2 over **every resource in the account** to find the CRN matching the `--instance <name>` flag. Round-6 added the server-side COS service filter (Resource Controller's `resource_id` parameter set to the COS catalog offering UUID) so the listing returns only COS instances. **Live re-verify**: same command, same bucket, same fresh-IAM state — **1.86 seconds** (under the 1.4s `ibmcloud cos` baseline). Workspaces that pass `--instance <CRN>` directly were always fast (~1.27s); this fix makes the friendlier `--instance <name>` path equally fast.
- **Mermaid diagrams in the PDF book now render with their label text** ([`issues/issue_sprint18_architect.md` Issue 1](issues/issue_sprint18_architect.md)) — pre-fix, PDF readers of the book saw mermaid diagrams' shapes and arrows but the text inside nodes and on edges was missing (e.g. page 120's chapter-17 backend-dispatch diagram). The PDF backend's Lua filter (`tools/docker/mdbook/render-mermaid.lua` inside the bundled `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` image) was pre-rendering mermaid blocks to **SVG** via mermaid-cli; mermaid-cli v11 emits node/edge labels via SVG `<foreignObject>` + embedded XHTML, which librsvg (pandoc's SVG-to-PDF rasteriser) does not implement, so geometry survived but labels rendered as empty. The filter now pre-renders to **PNG** via mermaid-cli's Puppeteer + Chromium path, which natively renders `<foreignObject>` and bakes in the mermaid browser-font stack. Retina-grade `-s 2`, white background, sized for readable embed at print resolution. Re-running `make book-pdf BOOK_BACKEND=docker` produces a PDF with full label text in all three mermaid-bearing chapters (7, 17, 21). A new regression smoke check (`scripts/check-pdf-mermaid-labels.sh`) fails the build if a future contributor regresses the docker image's font set or SVG-conversion stack. HTML book at `/book/` is unaffected (client-side `mermaid.min.js`).

## v1.6.2 — 2026-05-19

Sprint 16 follow-up — post-`v1.6.1` get-well. A live `!` verify after the `v1.6.1` cut surfaced a regression in the two-phase `up` flow that the hermetic test suite could not see: the bnk/testing phase was re-creating the cluster-shared network the cluster phase had just built, and IBM Cloud rejected the run with duplicate-name errors. A first fix attempt addressed the cluster VPC alone via per-resource "use existing" toggles; live verify caught it incomplete (the second phase still duplicated subnets, public gateways, the transit gateway, the testing client VPC, and jumphost networking). This patch ships the corrected architectural fix, verified GREEN against a real account (run-id `20260519-202202`). See [PLAN.md §"Sprint 16"](docs/PLAN.md) and [`issues/issue_sprint16_validator.md` Issue 2](issues/issue_sprint16_validator.md).

### Fixed

- **`up` no longer fails partway through with an IBM Cloud duplicate-name error when the bnk/testing phase runs after the cluster phase** ([`issues/issue_sprint16_validator.md` Issue 2](issues/issue_sprint16_validator.md)) — a full `roksbnkctl up` first provisions the cluster (creating the cluster VPC, cluster subnets, cluster public gateways, the transit gateway, the client VPC, and the jumphost network) and then deploys the bnk/testing layer. The second phase was attempting to **re-create** that entire cluster-shared network rather than reuse what the cluster phase had just made, so IBM Cloud rejected the run with `Provided Name … is not unique` / `A gateway with the same name already exists` and left the workspace half-deployed. The bnk/testing phase now writes a forced `bnk-phase-override.tfvars` (only when the workspace's `cluster-outputs.json` exists — i.e. only as the second phase of a `up`, never on a fresh workspace, never on the cluster-only or bnk-only sub-flows) that turns cluster-shared creation off (`create_roks_cluster=false` + `roks_cluster_id_or_name`, `use_existing_cluster_vpc=true` + `existing_cluster_vpc_id` sourced from `cluster-outputs.json`, `create_roks_transit_gateway=false`, `testing_create_client_vpc=false`, `testing_create_cluster_jumphosts=false`, `testing_create_tgw_jumphost=false`) and is appended last to the var-file chain so it overrides anything user-supplied. Symmetric with the existing `cluster-phase-override.tfvars` mechanism. The cluster-only and bnk-only sub-flows are unchanged; `up` against a fresh workspace is unchanged (no `cluster-outputs.json` ⇒ no override ⇒ byte-identical to `v1.6.1`). This is the live-verify gap the [PLAN.md §"Sprint 16"](docs/PLAN.md) phase-1b boundary work was blind to — the hermetic parity gate stayed green because no test exercised a workspace that had already completed the cluster phase, which is exactly the discipline the `live-verify-high-issues` rule exists to enforce.
- **`scripts/e2e-phase-handoff.sh` teardown no longer strands cluster-phase infrastructure** ([`issues/issue_sprint16_validator.md` Issue 4](issues/issue_sprint16_validator.md)) — the live-verify driver's EXIT trap previously ran `roksbnkctl down` (trial phase only), so any run that progressed past the cluster phase left a billing ROKS cluster + VPCs + transit gateway alive. Teardown now runs the trial `down` **then** `cluster down`, each tolerating a no-op, with a loud post-teardown assertion that no `canada-*` VPC / `canada-roks-tgw` / `canada-roks` cluster remains.
- **`roksbnkctl down` / `plan` / `apply` against `-w <ws>` alone now work, without re-supplying the original `--var-file`** ([`issues/issue_sprint16_validator.md` Issue 3](issues/issue_sprint16_validator.md)) — pre-fix, lifecycle ops against a successfully-applied workspace deterministically failed on required no-default variables (`ibmcloud_api_key`, `testing_*` / `roks_*` / `f5_*`) because the small `config.yaml`-derived auto-rendered `state/terraform.tfvars` doesn't carry them; the `terraform.applied.tfvars` snapshot PRD 07 / Sprint 11 wrote after each successful apply (a captured replay of the var-files terraform consumed, with `ibmcloud_api_key` redacted) was **write-only** — nothing read it back. `down` / `plan` / `apply` (both the trial and the cluster phase) now **auto-layer a deduped, secret-stripped replay of the phase's `terraform.applied.tfvars` as the lowest-precedence var-file** when a snapshot exists, restoring the var-file environment the prior apply ran with. The replay is derived on each lifecycle op (written to `<phase state dir>/.applied-replay.tfvars`) — necessary because the canonical snapshot is intentionally multi-section and the same key can appear in more than one section (terraform would reject that as `Each argument may be set only once`); the snapshot itself stays unchanged for audit. The API key is never persisted (it is redacted in the snapshot for audit visibility and dropped from the replay) and must keep coming from env / `--var-file` (intentional). User `--var-file` flags still win (replay is lowest precedence), and a fresh / never-applied workspace is byte-identical to before. A loud `→ Replaying applied tfvars from <path>` stderr line names the file so the contract stays visible — no implicit magic. When a workspace has neither a snapshot to replay *nor* an explicit `--var-file` (a fresh / never-applied or snapshot-deleted workspace), `roksbnkctl` now refuses with a clean actionable message that names the file to pass — instead of letting terraform bubble up a stack of unrelated-looking `No value for required variable` lines.

## v1.6.1 — 2026-05-19

Sprint 16 — internal consolidation **phase-1b**, post-`v1.6.0`. **No user-visible behavior change**: a user upgrading from `v1.6.0` sees identical `up` / `--on` / `terraform` / `targets` behavior and output. This completes the `internal/cli` god-package decomposition begun in Sprint 15: the lifecycle / cluster / remote-passthrough RunE orchestration (~1,655 LOC) is relocated out of `internal/cli/{lifecycle,cluster}.go` into the `internal/orchestration` service layer, leaving `internal/cli` a thin cobra adapter. Behavior is preserved byte-for-byte (verified: zero pre-existing test-file diffs, full hermetic `go test -race ./...` green, Sprint 14 `--on` + Sprint 15 chokepoint guards green & unedited). See [PLAN.md §"Sprint 16"](docs/PLAN.md) for the design surface and gate.

### Changed

- **`internal/cli` decomposition — phase-1b (no user-visible behavior change).** The lifecycle (`up`/`plan`/`apply`/`down` family + `openTF`/`applyWithRetry`/`tryAuto*`/docker-terraform helpers) and cluster/remote-passthrough (`shell`/`exec`/`kubeconfig`/`kubectl|oc|ibmcloud` passthroughs + `dispatchBackend`/`ensureIBMCloudLoggedIn`/…) orchestration moved from `internal/cli/{lifecycle,cluster}.go` into `internal/orchestration/{lifecycle,cluster}.go`; `internal/cli` is now a thin cobra adapter (command defs, flag binding, the Sprint-15 chokepoint wrappers, RunE shims). `cli`-resident collaborators are injected as function fields on `orchestration.LifecycleInputs`/`ClusterInputs` so `internal/orchestration` never imports `internal/cli` (one-directional boundary, asserted). Completes the Sprint-15 phase-1a chokepoint work; the remaining ~27 `cli` files are a tracked phase-2 follow-up. Strictly internal — no flag, output, or error-text change. See [PLAN.md §"Sprint 16"](docs/PLAN.md).

## v1.6.0 — 2026-05-18

Sprint 15 — internal consolidation / debt-paydown cycle, post-`v1.5.0`. **No user-visible behavior change**: a user upgrading from `v1.5.0` sees identical `up` / `--on` / `terraform` / `targets` behavior and output. This cycle collapses the recurring "a path/env value correct in the invocation context is wrong once it crosses a boundary" defect class (Sprint 12 Issues 1/2 `--var-file`/`--tf-source`; Sprint 13 Issue 1 `KUBECONFIG` leak — each previously patched per-instance and already user-correct) to a **single invocation-time chokepoint** so the class cannot reopen, and begins phase-1 decomposition of the `internal/cli` god-package. The bug class was already fixed per-instance in `v1.4.1`/`v1.5.0`; this changes *how* those fixes hold structurally, not *whether* — there is no new user-facing fix or feature. See [PLAN.md §"Sprint 15"](docs/PLAN.md) for the design surface and gate.

### Changed

- **Path/env normalization is now a single invocation-time chokepoint** ([PLAN.md §"Sprint 15" code deliverable 1](docs/PLAN.md)) — the per-`RunE` `--var-file` / `--tf-source` relative-path resolution (previously wired at 8+ command call sites) and the `--on <target>` remote-vs-local env composition are now produced once by a single resolved-invocation context (`cli.ResolvedFlags`) at command entry; no `RunE` and no `dispatchRemote` caller re-derives a path or env. Behavior is **identical to `v1.5.0`** — this is structural hardening so the recurring shell-CWD / SSH-boundary bug class cannot reopen on the next path/env-valued flag, not a behavior change.
- **`internal/cli` decomposition — phase 1** ([PLAN.md §"Sprint 15" code deliverable 2](docs/PLAN.md)) — lifecycle and remote/passthrough dispatch orchestration moved out of `internal/cli` (`lifecycle.go` + `cluster.go`) into a new `internal/orchestration` service layer; `cli` becomes a thin cobra adapter. Behavior-preserving move only. Phases 2+ (the remaining ~27 `cli` files) are an explicitly tracked post-`v1.6.0` follow-up — see `### Deferred`.

### Removed

- **Scattered `localPathEnvKeys` scrub list + duplicated `workspaceEnv` split** — the Sprint 13 per-instance fix for the `KUBECONFIG`-leak class (a `localPathEnvKeys` list and a `workspaceEnv`/`workspaceEnvCore`/`remoteSafeEnv` split living in `internal/cli`) is obviated by the chokepoint: env is classified into machine-portable core vs. local-only exactly once, by the single `internal/orchestration.LocalOnlyEnvKeys` / `ScrubLocalOnly` / `WorkspaceEnv[Core]` definitions. As-landed disposition (integrator-reconciled against the staff §Closure / as-landed code): the scattered `localPathEnvKeys` list is **deleted**; `remoteSafeEnv`, `workspaceEnv`, and `workspaceEnvCore` are **demoted to one-line delegating boundary wrappers** over `internal/orchestration` (a single source of truth, asserted by the chokepoint guard test), not deleted outright. No user-visible effect: the local `KUBECONFIG` path still never crosses the SSH boundary, exactly as in `v1.5.0`.

### Deferred (v1.x roadmap, post-v1.6.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). The carry list is unchanged from [v1.5.0 §"Deferred"](#deferred-v1x-roadmap-post-v150), plus two structural follow-ups tracked out of this cycle:

- **`internal/cli` decomposition phases 2+** ([PLAN.md §"Sprint 15" carry-overs](docs/PLAN.md)) — phase 1 extracts only `lifecycle.go` + `cluster.go` into `internal/orchestration`; the remaining ~27 `cli` files are a deliberate tracked post-`v1.6.0` follow-up, not this cycle.
- **Per-AZ jumphost stale-target reconcile (option (b))** ([PRD 09 §"Open questions"](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md#open-questions)) — carried forward unchanged from `v1.5.0`; still a post-release follow-up needing a `config.TargetCfg` ownership-marker schema change out of scope here.
- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — carry-forward from v1.5.0 / v1.4.1.
- All prior-cycle deferred items from [v1.5.0 §"Deferred"](#deferred-v1x-roadmap-post-v150), [v1.4.1 §"Deferred"](#deferred-v1x-roadmap-post-v141), [v1.4.0 §"Deferred"](#deferred-v1x-roadmap-post-v140), and [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.5.0 — 2026-05-18

Sprints 13–14 — minor feature cycle plus its get-well fold-in. Closes the post-v1.4.0 per-AZ-jumphost user-testing thread in one coherent release: the headline is that `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc …` finally works **end-to-end** against a private cluster. That required fixing **two** independent causes of the same `connection to the server localhost:8080 was refused` symptom — the local `KUBECONFIG` path leaking across the SSH boundary (Sprint 13; the bug disclosed as the `v1.4.1` known issue and originally designated a `v1.4.2` fast-follow) **and** the jumphost having no kubeconfig at all because cloud-init swallowed provisioning failures (Sprint 14 get-well, option C). Because the two causes are indistinguishable to a user, the integrator held `v1.5.0` open and merged the Sprint 14 fix into the same release rather than ship the headline fix still reproducible. The cycle also lands two ergonomic features (a read-only `roksbnkctl terraform` escape hatch and automatic registration of the per-AZ cluster jumphosts) and the book docs that tie them together — all surfaced in one user session, all about reaching/operating the deployed cluster from the workstation. See [PLAN.md §"Sprint 13"/"Sprint 14"](docs/PLAN.md), [PRD 08](docs/prd/08-TERRAFORM-READONLY.md), [PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md), [`issues/issue_sprint13_staff.md`](issues/issue_sprint13_staff.md) Issues 1–3, [`issues/issue_sprint13_architect.md`](issues/issue_sprint13_architect.md) Issue 2, and [`issues/issue_sprint14_staff.md`](issues/issue_sprint14_staff.md) Issue 1 for the design surface.

### Added

- **Read-only `roksbnkctl terraform` escape hatch** (alias `tf`) ([PRD 08](docs/prd/08-TERRAFORM-READONLY.md); [`issues/issue_sprint13_staff.md` Issue 2](issues/issue_sprint13_staff.md)) — a gated, **read-only-by-allowlist** passthrough to terraform against a workspace's managed state, replacing the fragile, undocumented `cd ~/.roksbnkctl/<ws>/state[-cluster] && TF_DATA_DIR=$PWD/terraform terraform …` workaround. Permitted subcommands: `output`, `show`, `state list`, `state show`, `state pull`, `providers`, `version`, `graph`, `validate`, `fmt -check`. Everything else is rejected **before terraform runs** — every mutating subcommand (`apply`/`destroy`/`init`/`import`/`taint`/…), mutating sub-verbs of `state` (`state rm`/`mv`/`push`/`replace-provider` are rejected even though top-level `state` is allowlisted), mutation flags (`-auto-approve`/`-replace`/`-target`/`-destroy`/write-mode `fmt`), and `--on` (managed state is workstation-local). Phase-correct cwd + `TF_DATA_DIR` are reused from the existing `tf.Open` plumbing — the CLI layer never re-derives them (the bug class this whole cycle addresses). Against a never-applied workspace phase it errors with `run roksbnkctl up first` and produces **no** source fetch / `init` side effect. `--phase cluster` selects the cluster-phase state. Mutations remain the exclusive domain of `up`/`plan`/`apply`/`down` (and `cluster`/`bnk` up/down) — this gate is permanent by design.
- **Per-AZ cluster-jumphost auto-registration** ([PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md); [`issues/issue_sprint13_staff.md` Issue 3](issues/issue_sprint13_staff.md)) — when `testing_create_cluster_jumphosts = true`, `roksbnkctl up`'s post-apply hook now also auto-registers one target per cluster-VPC availability zone, named `jumphost-<zone>` (e.g. `jumphost-ca-tor-1`), alongside the existing singular `jumphost`. The hook reads the `testing_cluster_jumphost_ips` terraform output (a `{zone => floating-IP}` map) and reuses the shared `jumphost_shared_key` output, so every per-AZ jumphost is a first-class `--on` target (full `kubectl`/`oc`/`ibmcloud`/`shell` passthrough, no SSH hop) with no manual `targets add`. Registration is best-effort/non-fatal (a parse or write failure logs one `warning:` and does not fail `up`, mirroring the singular `jumphost` seed); a `testing_create_cluster_jumphosts = false` / absent / empty-map deploy is a silent no-op with no behavior change. Idempotent — re-running `up` after a floating-IP rotation refreshes each `jumphost-<zone>` host in place. **Caveat (option (a) upsert-only, decided by the integrator):** if a zone is removed or `testing_create_cluster_jumphosts` is flipped to `false`, the now-orphaned `jumphost-<oldzone>` target lingers until you run `roksbnkctl targets remove jumphost-<oldzone>` by hand. A reconcile mode that prunes orphans automatically (option (b)) is a tracked post-`v1.5.0` follow-up — see `### Deferred`. Documented in [Chapter 15 §"Auto-discovery from terraform outputs"](book/src/15-ssh-targets.md) and [Chapter 16](book/src/16-on-flag-ssh-jumphosts.md).

### Fixed

- **Local `KUBECONFIG` path no longer leaks into the `--on <target>` remote environment** ([`issues/issue_sprint13_staff.md` Issue 1](issues/issue_sprint13_staff.md)) — after any successful local `roksbnkctl up` (which writes the admin kubeconfig to the local `~/.kube/config`), a subsequent `roksbnkctl --on <target> kubectl|oc …` deterministically failed with `connection to the server localhost:8080 was refused`. Root cause: `workspaceEnv()` appended `KUBECONFIG=<local filesystem path>` and `runPassthrough` forwarded the *same* env slice across the SSH boundary, where the local path is meaningless on the target *and* shadowed the cloud-init-provisioned `/home/ubuntu/.kube/config`. The env that crosses the SSH boundary is now machine-portable only — value-grade vars (`IBMCLOUD_API_KEY` / `IC_API_KEY` / `IBMCLOUD_REGION` / `IBMCLOUD_VERSION_CHECK`) still forward; the local-only `KUBECONFIG` path does not. Local `roksbnkctl kubectl` (no `--on`) is unchanged — it still resolves `KUBECONFIG` via the local chain. Correctness comes from never *sending* the local path, so the fix is independent of the target sshd's `AcceptEnv`. This is one half of restoring the canonical private-cluster workflow documented in [Chapter 16](book/src/16-on-flag-ssh-jumphosts.md) and [Chapter 9](book/src/09-registering-existing-cluster.md) — see the jumphost-kubeconfig fix below for the other half; together they make `--on jumphost kubectl|oc` work end-to-end. (This symptom was disclosed as the `v1.4.1` known issue and is fully resolved in `v1.5.0` together with the jumphost kubeconfig provisioning fix below.)
- **Jumphost kubeconfig is now reliably provisioned end-to-end** ([`issues/issue_sprint13_architect.md` Issue 2](issues/issue_sprint13_architect.md); [`issues/issue_sprint14_staff.md` Issue 1](issues/issue_sprint14_staff.md)) — after the `KUBECONFIG`-leak fix above, `roksbnkctl --on jumphost kubectl|oc …` could *still* fail with `connection to the server localhost:8080 was refused` because the jumphost had **no kubeconfig at all**: cloud-init's `ibmcloud login` + `ibmcloud ks cluster config --cluster <id> --admin` were guarded by `|| true`, so any boot-time failure was swallowed with no retry, no log, and no failure marker — `/home/ubuntu/.kube/config` was simply never written. Fixed in two layers (option C): (A) the cloud-init provisioning in the upstream HCL now wraps the `ibmcloud login` + `ks cluster config --admin` path in a bounded retry/readiness loop and writes a loud failure marker (`/var/log/jumphost-setup.log` + a `/var/log/jumphost-kubeconfig-FAILED` sentinel) on exhaustion instead of silently continuing — new deploys reliably produce `/home/ubuntu/.kube/config`; and (B) `roksbnkctl --on <target> kubectl|oc` self-heals — if the target has no usable kubeconfig it runs `ibmcloud login` (with the workspace's API key + region/resource-group, so an already-broken jumphost whose cloud-init login fork failed silently is re-authenticated too, not just re-configured) followed by `ibmcloud ks cluster config --admin` on the target before the wrapped command (distinguishing "no kubeconfig → heal" from "cluster genuinely down or bad/expired credentials → surface the real error after bounded retry, never silently fall back to the broken state"), so an already-running/already-broken jumphost is repaired with no `terraform` recreate. Together with the `KUBECONFIG`-leak fix above, this makes `roksbnkctl up` → `roksbnkctl --on jumphost kubectl|oc …` work **end-to-end**: the leak fix stops the local path shadowing the remote kubeconfig, and this fix guarantees the remote kubeconfig exists. A new `up → --on` e2e + `-tags integration` test (`internal/cli/lifecycle_e2e_test.go`) makes both the env-composition and the heal-vs-outage paths fail a test rather than a human.

### Deferred (v1.x roadmap, post-v1.5.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Carried forward from [v1.4.1 §"Deferred"](#deferred-v1x-roadmap-post-v141), plus one new follow-up from this cycle:

- **Per-AZ jumphost stale-target reconcile (option (b))** ([PRD 09 §"Open questions"](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md#open-questions); [`issues/issue_sprint13_architect.md`](issues/issue_sprint13_architect.md)) — `v1.5.0` ships option (a) upsert-only: orphaned `jumphost-<oldzone>` targets linger after a zone removal until manually removed. A reconcile mode that prunes them automatically needs unambiguous ownership semantics (a constrained zone-pattern match or a `config.TargetCfg` `auto:`/`managed_by:` schema marker, so a user's hand-named `jumphost-mybox` is never deleted) — a config-schema change deliberately out of `v1.5.0` scope. Tracked as a post-`v1.5.0` follow-up.
- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — carry-forward from v1.4.1 / v1.4.0.
- All prior-cycle deferred items from [v1.4.1 §"Deferred"](#deferred-v1x-roadmap-post-v141), [v1.4.0 §"Deferred"](#deferred-v1x-roadmap-post-v140), and [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.4.1 — 2026-05-18

Sprint 12 closure cycle — `v1.4.1`. Focused patch closing **two** sibling relative-path-resolution bugs surfaced post-v1.4.0, both instances of the same shell-CWD-vs-state-dir trap. The headline fix: when a user passed `--var-file=./terraform.tfvars` from a directory containing that file, terraform reported `Failed to read variables file. Given variables file ./terraform.tfvars does not exist.` because the flag value was forwarded verbatim to a terraform invocation whose working directory is the per-phase state dir, not the user's shell PWD. Relative `--var-file` paths now resolve against the invocation CWD before reaching either backend. The second fix (pulled forward from the Sprint 13 backlog per integrator decision) closes the analogous trap for a relative `--tf-source=./...` local path, which was persisted relative into `config.yaml` at `init` and detonated on a later `up` / `plan` / `apply` run. No new PRDs this cycle. See [PLAN.md §"Sprint 12"](docs/PLAN.md), [`issues/issue_sprint12_staff.md` Issue 1](issues/issue_sprint12_staff.md), and [`issues/issue_sprint12_validator.md` Issue 5](issues/issue_sprint12_validator.md) for the design surface.

### Fixed

- **`--var-file` relative paths now resolve against the invocation CWD** ([`issues/issue_sprint12_staff.md` Issue 1](issues/issue_sprint12_staff.md)) — `roksbnkctl up --var-file=./terraform.tfvars`, `cluster up --var-file=./...`, `bnk up --var-file=./...`, `plan` / `apply` / `down` with the same flag, all now resolve relative `--var-file` paths against the user's shell CWD (the directory they invoked `roksbnkctl` from), matching terraform's own `-var-file=./...` semantics. Prior to v1.4.1 the value was passed verbatim to terraform; terraform's CWD is the per-phase state dir (`~/.roksbnkctl/<workspace>/state[-cluster]/`), so a relative path resolved there and produced `Failed to read variables file. Given variables file ./<path> does not exist.` Absolute paths continue to work unchanged. The pre-flight error message when the resolved file is missing now names both the user-supplied path and the absolute path it resolved to, so typos and wrong-CWD invocations are distinguishable. The docker backend's prior absolute-only requirement (introduced in v1.0.x because docker bind-mounts need absolute host paths) is now redundant for the common case — every reachable `--var-file` is absolute by the time it reaches the backend dispatch — and remains in place as a defensive guard. Implementation lands in `internal/cli/` via a small `resolveVarFiles` helper called at each `--var-file`-consuming command's `RunE` entry point.
- **`--tf-source` relative local paths are now resolved to absolute before being persisted** ([`issues/issue_sprint12_validator.md` Issue 5](issues/issue_sprint12_validator.md)) — `roksbnkctl init --tf-source=./mytf` (and `up --tf-source=./...`) with a relative local-directory value now records an absolute path in the workspace's `config.yaml`, so the source still resolves on a later `up` / `plan` / `apply` run regardless of the directory those commands are invoked from. Prior to v1.4.1 the relative value passed the existence check at `init` time (checked against the shell CWD) but was stored verbatim; a subsequent lifecycle command handed it to terraform, whose CWD is the per-phase state dir, so the source directory could no longer be found — the same shell-CWD-vs-state-dir trap as the `--var-file` case, but worse because it survived into `config.yaml` and detonated on a *later* run rather than the same invocation. Absolute `--tf-source` paths, and the URL / GitHub source forms, are unchanged. This fix was pulled forward from the Sprint 13 backlog per integrator decision so v1.4.1 closes both siblings of the path-resolution trap together.

### Deferred (v1.x roadmap, post-v1.4.1)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). The carry list is unchanged from v1.4.0:

- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — carry-forward from v1.4.0.
- All prior-cycle deferred items from [v1.4.0 §"Deferred"](#deferred-v1x-roadmap-post-v140) and [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.4.0 — 2026-05-14

Sprint 11 closure cycle. Lands PRD 07's `terraform.applied.tfvars` snapshot per workspace phase — after every successful `terraform apply`, `roksbnkctl` writes a canonical-HCL var-file capturing the effective inputs that produced the current state. Re-create / audit / handoff scenarios that previously required `config.yaml` or memory now become file-driven: the snapshot is on disk at a predictable path, mode `0600`, with `ibmcloud_api_key` redacted and every other variable verbatim. The file is never read back by `roksbnkctl` (it's an output, not an input); `cluster down` / `bnk down` leave it in place so the prior `up`'s snapshot stays available for re-apply or audit. See [PRD 07](docs/prd/07-DEPLOYED-TFVARS.md) for the design rationale and [PLAN.md §"Sprint 11"](docs/PLAN.md) for the cycle's deliverables.

### Added

- **`terraform.applied.tfvars` snapshot per workspace phase** ([PRD 07](docs/prd/07-DEPLOYED-TFVARS.md)) — after every successful `terraform apply`, the effective var-file inputs land at `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` (cluster phase) or `~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` (trial phase, and the union file on `ShapeLegacySingle`). Canonical HCL — one assignment per line, alphabetic within each source section, source-attribution comments for `config.yaml`-derived vars / `terraform.tfvars.user` / cluster-phase override. `ibmcloud_api_key` is rendered as `<redacted>`; every other variable is verbatim. File mode is `0600`. The file is **not** read back by `roksbnkctl` — it's an output for the user (re-create / audit / handoff workflows), never an input the tool depends on. Implementation lands in `internal/config/applied_tfvars.go` (`WriteAppliedTFVars`) and hooks into `internal/tf/terraform.go::Workspace.Apply` after a successful apply (log-and-continue on write failure — the apply's exit code reflects the apply, not the snapshot's bookkeeping). See [Chapter 6 §"`terraform.applied.tfvars` — what's deployed right now"](book/src/06-workspaces.md) for the user-facing description with a worked example.

### Deferred (v1.x roadmap, post-v1.4.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.4.0:

- **`ops install` / `ops uninstall` snapshot** ([PRD 07 §"Open questions" item 1](docs/prd/07-DEPLOYED-TFVARS.md#open-questions)) — `ops install` and `ops uninstall` change cluster-side state (Kubernetes objects, IAM trusted profile bindings) but don't run Terraform, so the tfvars-shaped snapshot doesn't apply. A future cycle may add a parallel record (SA annotations, Secret state, the `--trusted-profile=…` value used). File a follow-up PRD if there's user demand.
- All prior-cycle deferred items from [v1.3.0 §"Deferred"](#deferred-v1x-roadmap-post-v130) remain deferred.

## v1.3.0 — 2026-05-14

Sprint 10 closure cycle. Closes the runtime side of PRD 04's trusted-profile flow (the in-pod `ibmcloud login` wrap Sprint 9 deferred), lands PRD 06's `roksbnkctl status` per-phase integration (Sprint 10 scope addition), and folds four of the five tech-writer polish issues deferred from Sprint 9 (the fifth — chapter 14 §"What's new in v1.2" section position — is deferred again as a v1.x polish item; see `### Deferred` below). The headline reframe: `roksbnkctl ops install --trusted-profile=auto` followed by `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` now returns a fresh IAM token end-to-end — the v1.2.x partial-closure callout in chapter 19 comes out. See [PLAN.md §"Sprint 10"](docs/PLAN.md) for cycle deliverables and [PRD 04 §"Resolved in Sprint 9"](docs/prd/04-CREDENTIALS.md#resolved-in-sprint-9) + [PRD 06 §"`status` command integration"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#status-command-integration-sprint-10-scope-addition) for the design surface.

### Added

- **`roksbnkctl status` per-phase deployment** ([PRD 06 §"`status` command integration"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#status-command-integration-sprint-10-scope-addition)) — `runStatus` consumes `config.DetectShape` and each phase's `terraform.tfstate` mtime, emitting two per-phase deployment lines instead of the v1.0.x single `Last apply` line that conflated the cluster and trial phases. Output by shape: `ShapeEmpty` reports both phases as `not deployed`; `ShapeClusterOnly` reports the cluster phase as `deployed (last apply <ts>)` and the trial as `not deployed`; `ShapeSplit` reports both phases independently; `ShapeLegacySingle` preserves the v1.0.x `Last apply:` line verbatim plus a `Shape:` callout so the reader sees they're on a legacy workspace at a glance. See [Chapter 24 §"`roksbnkctl status`"](book/src/24-day-2-ops.md) for per-shape samples. Implementation lands in [`internal/cli/inspect.go`](internal/cli/inspect.go) with a four-shape table test against the `internal/config/testdata/` fixtures from Sprint 8.

### Changed

- **In-pod `ibmcloud login` wrap is now trusted-profile-aware** ([PRD 04 §"Resolved in Sprint 9" carry-over](docs/prd/04-CREDENTIALS.md#trusted-profile-auto-provisioning-k8s-backend); closes the v1.2.x partial-closure documented in [v1.2.0 §"Deferred"](#deferred-v1x-roadmap-post-v120)) — `runOnOpsPod`'s ibmcloud login wrap detects whether the ops pod's ServiceAccount carries the `iam.cloud.ibm.com/trusted-profile` annotation. Trusted-profile-annotated pods run `ibmcloud login -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet`; the `--cr-token @<path>` form reads a projected SA token (audience `iam`, mounted at the cited path) and IBM IAM validates that JWT against the trusted profile's `ROKS_SA` compute-resource link. Static-key pods continue the v1.0.x `--apikey "$IBMCLOUD_API_KEY"` path. The `IAM_PROFILE_ID` env var and the projected SA-token volume are injected into the pod spec at `ops install` time via the manifest renderer when the trusted profile is provisioned (`internal/cli/ops.go`, `internal/exec/k8s_install.yaml`). Under `--trusted-profile=auto` success, `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` now returns a fresh IAM token end-to-end; the static API key never transits the pod env. The first invocation may take 30–60 seconds while IBM IAM picks up the cluster's OIDC issuer URL; the wrap includes a brief retry to absorb this propagation window.
- **`roksbnkctl status` output for non-Legacy workspaces** replaces the v1.0.x single `Last apply` line with per-phase `Cluster phase:` / `BNK trial:` lines. Scripts that parse `Last apply` continue to work on `ShapeLegacySingle` workspaces (where the line is preserved verbatim) but will need to switch to the per-phase lines on `ShapeEmpty`, `ShapeClusterOnly`, and `ShapeSplit` workspaces. Anyone on a non-legacy workspace running such a script was already affected by Sprint 8's phase split (the v1.1+ `Last apply` line, when emitted at all, would have been the trial-only mtime, not the cluster's) — this release makes the change visible rather than silently misleading.
- **`make release` now runs `-tags integration` tests against an ephemeral kind cluster** ([PLAN.md §"Sprint 10 → Code deliverable 3"](docs/PLAN.md)) — closes the v1.2.0 → v1.2.1 cascade gap where the local pre-tag gate compile-checked the integration-tagged code but didn't execute it. New [`scripts/integration-test.sh`](scripts/integration-test.sh) brings up a kind cluster, runs `go test -tags integration` for `internal/exec/...` + `internal/remote/...`, tears down on exit. Contributors without `kind` installed see a warning + confirmation prompt instead of a hard fail (so a doc-only change on a workstation without kind isn't blocked); `SKIP_INTEGRATION_TEST=1` bypasses explicitly. The `Makefile` step also detects a missing or unreachable docker daemon and aborts with a remediation hint. See `make integration-test` for the standalone invocation.

### Fixed

- **In-pod `ibmcloud login` wrap closure** — closes the [v1.2.0 §"Deferred"](#deferred-v1x-roadmap-post-v120) "In-pod `ibmcloud login` wrap for the trusted-profile path (Sprint 10)" bullet. The v1.2.x partial-closure admonition in chapter 19 §"Trusted-profile flow (v1.2+)" is gone; the documented behavior matches the binary end-to-end. Staff Issue 2 from Sprint 9 resolved.
- **Chapter 19 `ops show` shape under `--trusted-profile=auto`** (Sprint 9 tech-writer Issue 4) — the section now documents the two-line `trusted-profile:` + `secret:` shape that `runOpsShow` actually emits, with both static-key and trusted-profile cases called out.
- **Chapter 19 `<workspace>` vs `sandbox-roks` placeholder consistency** (Sprint 9 tech-writer Issue 13) — all concrete sample names standardized on `canada-roks` (matching the v1.1 release-notes / Chapter 9 workspace convention); abstract `<workspace>` reserved for prose generalizations.
- **Chapter 19 §"Credential propagation" v1.2 callout placement** (Sprint 9 tech-writer Issue 9) — step 4 ("Create or update the credential Secret") in §"`roksbnkctl ops install`" now opens with a `v1.2+ note` mirroring the existing one in §"Credential propagation"; readers skimming the install walkthrough see the trusted-profile cross-link without having to scroll past §"Wait for readiness".
- **Chapter 14 "warning block" → "warning line" wording** (Sprint 9 tech-writer Issue 7) — the §"Compatibility note" paragraph now says "one extra stderr warning line", matching the single-line shape of all three fallback warnings in `internal/cli/ops.go`.

### Deferred (v1.x roadmap, post-v1.3.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.3.0:

- **Workspace-config customisation of trusted-profile policies** — v1.2+ ships with no default policies attached; a future cycle will surface `ibmcloud.trusted_profile.policies` as a workspace-config block.
- **Trusted-profile path for the SSH backend** — out of scope; the SSH backend's cred model (SetEnv + wrapper-script fallback) doesn't have a projected SA token to trade.
- **`--trusted-profile` flag on `roksbnkctl up` / `cluster up`** — out of scope; the terraform-driven lifecycle commands still use the workspace's resolved API key directly for HCL provider auth.
- **Long-running ops pod with kubeconfig refresh on token rotation** — the trusted-profile path makes this mostly moot (IAM tokens are short-lived and refreshed by the SDK transparently), but a multi-day pod lifetime against a projected SA token whose own rotation cadence shifts is on the v1.x watch-list.
- **Chapter 14 §"What's new in v1.2" section position** (Sprint 9 tech-writer Issue 8) — restructuring would extensively reshape an otherwise version-agnostic chapter; deferred as a v1.x polish item.
- **Chapter 19 §"5. Create the Pod" YAML — `env:` block** (Sprint 10 tech-writer Issue 14) — the pod-spec sample doesn't show the actual `env:` block (`HOME: /tmp` since v1.2.1, conditional `IAM_PROFILE_ID` under `--trusted-profile=auto|on` success). The prose at the same section's "What just happened, in order" step 5 mentions `IAM_PROFILE_ID` so the reader gets the concept; expanding the YAML to show the conditional shape requires either two side-by-side samples or a comment-annotated conditional block, neither of which is local. Deferred to a v1.4 chapter-polish pass.

## v1.2.1 — 2026-05-13

CI-recovery patch on top of `v1.2.0`. The `v1.2.0` cut passed the now-extended local pre-tag gate (build / vet / fmt / test / staticcheck / `-tags integration` build) but CI runs `go test -tags integration` against a live kind cluster, which surfaced an image-level gap the local gate doesn't exercise: the Sprint 9 image switch on `TestIntegration_K8sBackend_JobMode_Echo` (busybox → tools-ibmcloud, adding `USER 1000` for `RunAsNonRoot` admission) left uid 1000 without a writable `$HOME`. The ibmcloud CLI's first-run config write to `$HOME/.bluemix/` failed with `Configuration error: mkdir /.bluemix: permission denied` even for `ibmcloud --version`. Functionally identical to `v1.2.0` for the release binaries (the failure was in the tools image used by integration tests; goreleaser-built end-user binaries are unaffected). **End users should install v1.2.1**; the `v1.2.0` Release page is retained as a historical artifact only.

### Fixed (CI recovery)

- **`tools/docker/ibmcloud/Dockerfile`**: provision `/home/runner` owned by uid 1000 before the `USER 1000` drop; `ENV HOME=/home/runner`; `WORKDIR /home/runner`. ibmcloud's first-run config dir creation now lands at `/home/runner/.bluemix/` (writable) instead of `/.bluemix/` (root-only). The Build tools images workflow republishes `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v1.2.1` automatically on tag push; `TestIntegration_K8sBackend_JobMode_Echo` passes against the rebuilt image.
- **Local pre-tag gate gap noted**: `make release` runs `go build -tags integration ./...` (compile check) but not `go test -tags integration` (which requires a kind cluster + docker daemon). Adding a kind-bringup step to the local gate is non-trivial and not yet planned; the CI-side gate continues to catch image-level integration gaps that the local gate can't. Tracked as a Sprint 10 candidate.

## v1.2.0 — 2026-05-13 — SUPERSEDED by v1.2.1

Intended as the headline Sprint 9 release (PRD 04 cred-passing closure + CI polish) but the local pre-tag gate passed while CI's live integration-test job surfaced a tools-image gap. See `v1.2.1` above for the corrected cut. v1.2.0 release binaries are functionally identical to v1.2.1; only the tools image used by `--backend k8s` integration tests differs.

Sprint 9 closure cycle — closes the two PRD 04 §"Open questions" items that have been open since the v0.9 cycle (the cred-tmpfile-bind-mount pattern for the docker backend, and the trusted-profile auto-provisioning for the k8s backend), plus the CI / Makefile polish that prevents another v1.1.0 → v1.1.1 → v1.1.2 cascade. The headline reframe: from v1.0.x-style "static API key in env / Secret" to "no static API key on the wire when it can be avoided". Both backends get sane fallbacks for environments where the new pattern doesn't apply. See [PRD 04 §"Resolved in Sprint 9"](docs/prd/04-CREDENTIALS.md#resolved-in-sprint-9) for the design rationale and [PLAN.md §"Sprint 9"](docs/PLAN.md) for the cycle's deliverables.

### Added

#### Sprint 9 — PRD 04 closure (cred tmpfile + trusted profile) + CI polish

- **Cred tmpfile-bind-mount pattern for the docker backend** ([PRD 04 §"Resolved in Sprint 9" → "Cred tmpfile-bind-mount pattern (docker backend)"](docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend))
  - The resolved `IBMCLOUD_API_KEY` is written to a per-run `0600` tempfile under `$TMPDIR/roksbnkctl-creds-<rand>/api-key`, bind-mounted read-only at `/run/secrets/ibmcloud_api_key` in the container.
  - Container env carries only `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key`; the legacy `IBMCLOUD_API_KEY=<value>` form is gone. `docker inspect <id>` shows the path and the bind-mount entry, never the key value.
  - Container command is wrapped in `sh -c 'export IBMCLOUD_API_KEY="$(cat "$IBMCLOUD_API_KEY_FILE")" && exec …'` so tools that read from env (the existing `dockerImageBinary["ibmcloud"]` login wrap, terraform's IBM provider, ad-hoc `ibmcloud` invocations) continue to find the value at process-spawn time.
  - Tempfile cleanup runs via `defer` on backend `Run` exit, with a `context.AfterFunc` backstop so interrupted runs still scrub the file. Long-running invocations (e.g., `roksbnkctl up --backend docker` with a 30-min terraform apply) hold the file open via the bind mount for the duration; cleanup fires after the container exits.
  - Closes the v1.0.x → v1.1.0 trade-off documented at [`internal/exec/docker.go`](internal/exec/docker.go) `buildMountsAndEnv` and unblocks `TestIntegration_DockerBackend_NoLeakInInspect` (was `t.Skip`'d on commit `776fe56`).
  - Sample (no flag change required — the pattern is the default for `--backend docker` on v1.2 and up):
    ```bash
    roksbnkctl --backend docker ibmcloud iam oauth-tokens
    # docker inspect on the spawned container shows IBMCLOUD_API_KEY_FILE only, never the value
    ```
- **Trusted-profile auto-provisioning for the k8s backend** ([PRD 04 §"Resolved in Sprint 9" → "Trusted-profile auto-provisioning (k8s backend)"](docs/prd/04-CREDENTIALS.md#trusted-profile-auto-provisioning-k8s-backend); closes PRD 04 §"Implementation tasks" task 8)
  - New `--trusted-profile=auto|on|off` flag on `roksbnkctl ops install`, default `auto`, validated at flag-parse time.
  - `auto`: probe the resolved API key for IAM `iam-identity` perms; on present, provision `roksbnkctl-ops-<workspace>` trusted profile linked to the ops pod's ServiceAccount via its projected SA token. On perm-missing (`403` from the IAM probe), fall back to the v1.0.x static-key Secret with a single stderr warning line naming the missing perm and how to silence (`--trusted-profile=off`).
  - `on`: try to provision, fail loudly on perm-missing with a non-zero exit. For CI / hardened environments where the static-key path is unacceptable.
  - `off`: skip the trusted-profile path; provision the v1.0.x static-key Secret. Compatibility / debugging / air-gapped clusters.
  - Profile name is namespaced per workspace (`roksbnkctl-ops-<workspace>`) so multiple workspaces against the same IBM Cloud account don't race for a single shared name.
  - ServiceAccount carries `iam.cloud.ibm.com/trusted-profile: <name>` (the IBM IAM CSI hook reads this) plus `roksbnkctl.io/trusted-profile-managed: "true"` (signals `ops uninstall --confirm` to delete the profile on teardown — best-effort, with a warning line if IAM perms have since changed).
  - New `internal/ibm/trusted_profile.go` package wraps the IBM IAM Identity SDK calls (`CreateProfile`, `CreateClaimRule`, `CreatePolicy`, `DeleteProfile`); reusable for future trusted-profile use cases beyond the ops pod.
  - Implementation lands in [`internal/exec/k8s.go`](internal/exec/k8s.go) (`installOpsPod` branch on flag value), [`internal/cli/ops.go`](internal/cli/ops.go) (cobra flag wiring + validation), and the new [`internal/ibm/trusted_profile.go`](internal/ibm/trusted_profile.go).
  - Sample:
    ```bash
    roksbnkctl ops install --trusted-profile=auto    # default; auto-falls-back
    roksbnkctl ops install --trusted-profile=on      # CI / fail-loud on perm-missing
    roksbnkctl ops install --trusted-profile=off     # v1.0.x static-key path
    ```
- **Book chapter edits** for the new surface:
  - **Chapter 14 (`Credentials and the resolver chain`)** — new §"What's new in v1.2: the cred-tmpfile and trusted-profile paths" with the one-paragraph docker pattern explainer + the three-row `--trusted-profile` flag table + compatibility note.
  - **Chapter 19 (`The in-cluster ops pod`)** — new §"Trusted-profile flow (v1.2+)" with the `ops install --trusted-profile=auto` sample output, the SA verification command, the auto-fallback warning shape, the `--trusted-profile=off` opt-out path, and the `ops uninstall` trusted-profile cleanup behaviour. Existing §"Credential propagation" + §"Rotation" sections gain v1.2+ pointer notes so they're not stale.

### Changed

- **`--backend docker` cred propagation** — the v1.0.x bare-name `Env: ["IBMCLOUD_API_KEY"]` form and the v1.1.0 explicit `IBMCLOUD_API_KEY=<value>` form are both gone. The container's env carries only `IBMCLOUD_API_KEY_FILE` (pointing at the bind-mounted tempfile); the value reaches tools that read from env via a `sh -c export …` shim. `docker inspect` is now clean per [PRD 04 §"Anti-patterns to avoid"](docs/prd/04-CREDENTIALS.md#docker-container) item 1. The user-facing invariant — set `IBMCLOUD_API_KEY` in your shell or workspace config and `roksbnkctl --backend docker` works — is unchanged.
- **`roksbnkctl ops install` defaults to `--trusted-profile=auto`** — previously the install always provisioned the v1.0.x static-key Secret with no trusted-profile path. Workspaces whose API key has IAM `iam-identity` perms now get the trusted-profile path transparently on first `ops install` after the upgrade; the static-key Secret is replaced. Workspaces whose key lacks the perms see one new warning line per `ops install` and otherwise continue to work as in v1.0.x.

### Fixed

- **`TestIntegration_DockerBackend_NoLeakInInspect`** re-enabled — the `t.Skip` marker landed on commit `776fe56` is removed. The test asserts that a known `IBMCLOUD_API_KEY` value never appears in `docker inspect` output for a container spawned by `--backend docker`. Closed by the cred tmpfile-bind-mount pattern (this release's headline cred work).
- **`TestIntegration_K8sBackend_JobMode_Echo`** re-enabled — the `t.Skip` marker landed on commit `776fe56` is removed. The Job-mode echo test now runs against `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` (already runs as uid 1000) instead of `busybox:1.36` (default USER root, which collided with `runAsJob`'s `RunAsNonRoot: true` SecurityContext). Picked option 1 from the test body's two-options TODO so the production `runAsJob` SecurityContext stays unchanged.
- **`TESTCONTAINERS_RYUK_DISABLED=true`** in the CI integration job — kills the docker-hub `testcontainers/ryuk` pull that produced the intermittent `429 too many requests` flake on `TestIntegration_Connect_Whoami` (the v1.1.2 §"Not fixed" carry-over). Ephemeral CI runners don't need the testcontainers reaper. Implementation lands in [`.github/workflows/ci.yml`](.github/workflows/ci.yml).
- **`Makefile` pre-tag checklist** (the v1.1.2 carry-over) — the `release` target now runs `staticcheck ./...` AND `go build -tags integration ./...` AND the default-build sweep, closing the three-configuration gap that produced the v1.1.0 → v1.1.1 → v1.1.2 cascade. The new gate matches what CI runs and surfaces all three build configs' failures locally before the tag goes out.

### Deferred (v1.x roadmap, post-v1.2.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). Not in v1.2.0:

- **In-pod `ibmcloud login` wrap for the trusted-profile path** (Sprint 10) — Sprint 9 lands the **provisioning** side of `--trusted-profile=auto` (profile creation, SA annotation, manifest rendering with empty Secret data when auto-success). The existing `runOnOpsPod` login wrap at [`internal/exec/k8s.go`](internal/exec/k8s.go) still does `ibmcloud login --apikey "$IBMCLOUD_API_KEY"` regardless of mode, so under `auto`-success the wrap fails with `missing API key` when stateful `ibmcloud` subcommands actually run inside the pod (the Secret data is empty by design). Sprint 10 ships the conditional wrap (`ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` when the SA carries `iam.cloud.ibm.com/trusted-profile`, with `IAM_PROFILE_ID` injected into the pod spec at install time). Tracked in [staff Issue 2](issues/issue_sprint9_staff.md). The v1.2.0 security-side win is real but **partial** — no static API key sits at rest in any Kubernetes Secret in etcd under `auto`-success; the runtime cred flow still uses static-key. Pass `--trusted-profile=off` if you need the runtime wrap to actually work today.
- **Workspace-config customisation of trusted-profile policies** — v1.2 ships with minimal defaults (Viewer on container-registry, Operator on cloud-object-storage). A future cycle will surface `ibmcloud.trusted_profile.policies` as a workspace-config block so users can layer custom IAM policies onto the provisioned profile.
- **Trusted-profile path for the SSH backend** — out of scope; the SSH backend ships its own cred-passing model (SetEnv + wrapper-script fallback) and the trusted-profile path requires a projected k8s SA token, which the SSH-target side doesn't have.
- **`--trusted-profile` flag on `roksbnkctl up` / `cluster up`** — out of scope; the terraform-driven lifecycle commands still use the workspace's resolved API key directly for HCL provider auth. The trusted-profile path is exclusively for the ops pod.

## v0.9.0 — 2026-05-10 (M3 milestone)

The four-backend, GSLB-validation, in-cluster-ops release. Cumulative surface across Sprints 3–5.

### Added

#### Sprint 5 — DNS probe + terraform docker (v0.9 gate sprint)

- **GSLB-aware DNS probe** (`roksbnkctl test dns`)
  - `miekg/dns`-based `Probe` (replaces the std-lib `net.Resolver` impl) with full record-type coverage (A / AAAA / CNAME / MX / NS / TXT / SRV / SOA / PTR / CAA / DS / DNSKEY / ANY plus everything else `dns.StringToType` accepts)
  - New flags: `--target`, `--type`, `--server`, `--iterations`, `--timeout`, `--gslb-compare`, `--require-divergence`
  - Server resolution: literal `<ip>[:<port>]`, `system` (host `/etc/resolv.conf`), `cluster` (in-pod CoreDNS, k8s-backend only), or named-from-workspace-config (`test.dns.resolvers`)
  - RTT distribution (`p50`/`p95`/`p99`) when `--iterations > 1`
  - JSON output: `roksbnkctl.dns.v1.vantage` (single-vantage) and `roksbnkctl.dns.v1` (`--gslb-compare`)
  - `--gslb-compare` fans the probe across `local` + `k8s` (when a kubeconfig is reachable) + every `ssh:<target>` registered in workspace targets; emits `gslb_divergence` boolean
  - `--require-divergence` flips the exit code when no divergence is observed (CI assertion that GSLB is doing something)
  - In-cluster path runs as a one-shot Job re-execing the bundled tools image (no separate `roksbnkctl-cli` image)
  - Workspace config: new `test.dns.resolvers` (named resolver map) and `test.dns.default_target` fields
- **Terraform via docker** (`roksbnkctl up/plan/apply/down --backend docker`)
  - `hashicorp/terraform:1.5.7` pinned upstream image
  - Workspace state directory bind-mounted at `/state` (read-write); embedded HCL materialised under `/state/tf-source/<source>/`
  - `--user $(id -u):$(id -g)` keeps state-file ownership aligned with the host user (Linux/WSL2; macOS Docker Desktop transparent)
  - `--backend k8s` and `--backend ssh:<target>` for terraform deferred to v1.x with a clear error pointing at PRD 03 §"State concerns"
- **Doctor extensions** (`roksbnkctl doctor`)
  - DNS-probe sanity check (when workspace has `test.dns.default_target`)
  - K8s ops-pod env runtime probe (`kubectl exec -- printenv`, value redacted in output)
  - Cred rotation freshness warning when the Secret's `roksbnkctl.io/rotated-at` annotation is more than 30 days old
- **Book chapters**: 20 (Connectivity testing), 21 (DNS testing for GSLB — flagship), 22 (Throughput testing); chapter 17 expanded with terraform-via-docker subsection

#### Sprint 4 — k8s + SSH backends, in-cluster ops pod

- **`--backend k8s`** (`internal/exec/k8s.go`)
  - Long-lived ops pod path for ad-hoc commands (`ibmcloud`, future interactive shells); SPDY-channel `kubectl exec` with redactor-wrapped stdout/stderr
  - One-shot Job path for ephemeral tools (iperf3 client, future probes); `ttlSecondsAfterFinished: 60` auto-cleanup; logs streamed via `client-go`
  - `roksbnkctl ops install/show/uninstall` — install/inspect/teardown of namespaces, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret, Pod
  - Embedded RBAC manifests (`internal/exec/k8s_install.yaml`) — least-privilege ClusterRole with `resourceNames`-restricted `secrets/get`
- **`--backend ssh:<target>`** (`internal/exec/ssh.go`)
  - File materialisation to `/tmp/roksbnkctl.<rand>/` on the remote with `trap … EXIT` cleanup
  - Env propagation: SetEnv (preferred, requires sshd `AcceptEnv`) → wrapper-script-with-trap fallback (silent `set +x` source from a 0700 env-file)
  - Per-tool apt-bootstrap behind `--bootstrap` opt-in (Ubuntu only); 126/127 split for sudo / non-Ubuntu / repo-unreachable failures
  - Doctor `--backend k8s` / `--backend ssh:<target>` checks
- **iperf3 SCC fix** for OpenShift `restricted-v2` (`runAsNonRoot`, `runAsUser: 1000`, `seccompProfile: RuntimeDefault`, `capabilities.drop: [ALL]`)
- **Per-tool default backend map**: iperf3 → `k8s`, ibmcloud → `local`, terraform → `local`
- **126/127 backend-failure split** — `127` for "couldn't start" (daemon down, target unreachable), `126` for "started then failed" (container OOMKilled, ssh session died mid-run)
- **Book chapters**: 17 (Execution backends — full deep-dive), 18 (Choosing a backend per tool), 19 (The in-cluster ops pod)

#### Sprint 3 — credential abstraction + first backends

- **`internal/cred.Resolver`** — single-source-of-truth API key resolution chain (env → keychain → config-b64 → prompt)
- **`internal/exec.Backend` interface** + `RunOpts` + `Credentials` shared shape across all backends
- **`--backend local`** + **`--backend docker`** — first two backends; `--backend` persistent root flag wins over workspace-config default
- **Output stream redactor** (`internal/exec/redact.go`) — wraps `io.Writer` to mask the IBM API key value if it ever appears in stream content; defense-in-depth across all backends
- **Vendored tool images** — `ghcr.io/jgruberf5/roksbnkctl-tools-{ibmcloud,iperf3}:<v>`; tag pinned to the binary's `internal/version.Version` value at runtime (release tag → matching image tag)
- **Workspace config `exec:` block** — per-tool default backend selection
- **`tools-images.yml` GitHub Actions workflow** — builds + pushes the tools images on tag (Sprint 5 added `:dev` push on `main` for `go install ./cmd/roksbnkctl@main` UX)
- **Book chapters**: 12 (Workspace config), 13 (Terraform variables), 14 (Credentials and the resolver chain), 15 (SSH targets), 17 intro (Execution backends)

### Changed

- **`hashicorp/terraform:1.5.7`** is the literal pin for the terraform docker backend (not version-resolved like the per-tool tools images)
- **DNS probe schema strings** are now namespaced: `roksbnkctl.dns.v1.vantage` for single-vantage, `roksbnkctl.dns.v1` for multi-vantage `--gslb-compare`
- **`tools/docker/iperf3/Dockerfile`** ships `USER 1000` so the bundled image satisfies `runAsNonRoot: true` policies on plain k8s clusters
- **K8s Job names** now sanitise docker-style argv[0] image refs (colons / slashes / `@`) so the test fallback path doesn't trip k8s label-validation regex

### Deferred (post-v1.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark:

- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open; v1.x)
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only in v0.9)
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> covered the v0.9 surface in **22 published chapters**: 0 (Preface) through 22 (Throughput testing). Sprint 6 landed chapters 23-32 (E2E plan, COS supply chain, troubleshooting, command + config reference, glossary, building from source); Sprint 7 launched the polished book alongside the v1.0 tag.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, etc.) lives under [`docs/prd/`](docs/prd/).

## v1.0.0 — 2026-05-11 (M4 milestone)

The first stable release. roksbnkctl bundles seven sprints of work (M1 → M4) into a single-binary CLI: a 4-command lifecycle (`init` → `up` → `test` → `down`), four execution backends (`local` / `docker` / `k8s` / `ssh:<target>`), a GSLB-aware DNS probe, terraform-via-docker, an in-cluster ops pod, and a full kubectl-internalised cluster-ops surface — all in one statically linked binary with terraform as the only required host install. The published book at <https://jgruberf5.github.io/roksbnkctl/book/> ships alongside the binary as the canonical user documentation.

Milestone history: **v0.7** (M1) landed `--on jumphost` for customer-firewalled environments. **v0.8** (M2) internalised kubectl + oc via client-go. **v0.9** (M3) added the four-backend matrix, the GSLB-aware DNS probe, and terraform-via-docker. **v1.0** (M4) closes out with full E2E coverage, doctor green-by-default on a stock dev box with only `terraform` installed, the polished book launch, and the release artifacts (signed binaries deferred to v1.x — see Deferred below).

### Added

#### Sprint 7 — book launch + v1.0 release artifacts

- **Book published** at <https://jgruberf5.github.io/roksbnkctl/book/> — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_. 32 chapters + preface + worked-example walkthroughs in each Part, Mermaid diagrams for architecture / lifecycle / GSLB cross-vantage / execution-backend matrix, foreword/preface rewrite, every code example re-verified in a fresh workspace. Dogfooded by ≥1 external user against a real IBM Cloud account before the tag cut (per PLAN.md §"v1.0 (M4)" gate).
- **`roksbnkctl --version` / `roksbnkctl version`** now emits a second line `Docs: https://jgruberf5.github.io/roksbnkctl/book/` pointing at the canonical user-documentation surface. The first line ("`roksbnkctl <ver> (commit <c>, built <d>)`") is byte-identical to the pre-v1.0 shape so scripts that grep on it continue to parse. The shape is pinned by `internal/cli/meta_test.go::TestVersionCmd_OutputShape`. Constant of record: `internal/cli/meta.go::DocsURL`.
- **GitHub Release artifacts** — Linux / macOS / Windows × amd64 / arm64 archives + `checksums.txt` + offline **`roksbnkctl-book-v1.0.0.pdf`** (the same book that ships at GitHub Pages, packaged for offline reading via mdbook-pandoc + XeLaTeX). The release page header links at the book and the footer at `CHANGELOG.md`. Archives now include `LICENSE`, `README.md`, `CHANGELOG.md`, and `MIGRATING.md` alongside the binary so the downloaded tarball is self-contained.
- **PDF release pipeline** — `make release` from the repo root drives a docker-containerised build (via `tools/docker/mdbook/Dockerfile` — bundles mdbook + mdbook-mermaid + mdbook-pandoc + pandoc + texlive-xetex + mermaid-cli) that produces both the HTML (for GitHub Pages) and the PDF (for the GitHub Release page) in one shot. Mermaid diagrams pre-render to SVG via mermaid-cli so the PDF embeds real diagrams rather than literal source text. Local dev iteration on HTML stays lightweight via `make book` + `make book-serve` (host install, no docker required).
- **README rewritten** for the v1.0 narrative — single-line status, terraform-only prereq table, install options (go install / pre-built binary / from-source / self-update), pointer block to the book + CHANGELOG + MIGRATING + PLAN + per-PRD design rationale. Trimmed from 700+ lines to ~90; the book is the canonical documentation surface.

#### Sprint 6 — testing build-out + reference chapter coverage

- **Full e2e Phases I + M + N** — `scripts/e2e-test-backends.sh` expanded with Phase I (SSH backend, 12 steps I0-I11), full Phase M (cred audit including the SSH-side M5/M6 steps), and Phase N (mixed-mode lifecycle N1-N6). LD9 (SSH vantage for DNS probe) wired alongside.
- **`scripts/e2e-test-full.sh`** — combined A-H + I-N + L-DNS runner (~4-6 hour wall time); designed for release branches + manual-trigger CI.
- **`.github/workflows/e2e-full.yml`** — manual-trigger + release-branch CI workflow for the combined runner.
- **`TestProbe_TruncatedFlag`** — dual-stack UDP+TCP mock server pins the TC=1 projection through the TCP retry path (closes Sprint 5 validator Issue 4).
- **`tools/refgen/cobra-md`** + **`tools/refgen/tfvars-md`** — Go-based auto-generators for chapters 27 (Command reference) and 29 (Terraform variable reference). Re-run on every CLI / variables.tf change.
- **`MIGRATING.md`** — top-level migration guide for users coming from v0.6.x `bnkctl` or from manual BNK deployments.
- **`internal/cred/resolver_invariance_test.go`** — pins the cred-resolver contract across all four backends (Phase N Go-side contract).
- **`internal/doctor/doctor_test.go`** — pins the green-by-default contract.
- **EDNS Client Subnet surfacing** — `DNSProbeResult.EDNSClientSubnet` is populated from the resolver's RFC 7871 echo (when present); `omitempty` so non-ECS resolvers don't pollute the JSON.
- **Book chapters 23, 25, 26, 28, 30, 31, 32** — hand-written reference / troubleshooting / glossary; chapters 27 and 29 auto-generated.

### Changed

- **`roksbnkctl doctor`** is **green-by-default on a stock dev box with only `terraform` installed**. The historical checks for `kubectl`, `oc`, `ibmcloud`, `iperf3`, and `dig` are now **informational** rather than warnings/errors — the binary has internalised those surfaces (chapter 2 / chapter 17 for backends; chapter 21 for DNS). Exit code semantic (0 on green / 1 on red) unchanged.
- **`tools/docker/ibmcloud/Dockerfile`** dropped `ENTRYPOINT ["ibmcloud"]`. The docker backend's dispatch layer now prepends the tool binary name explicitly via a new `dockerImageBinary` map; the k8s `jobToolCmdOverride` map mirrors it. Sprint 5's `jobToolCmdOverride` shim for `roksbnkctl` self-exec dns-probe is now unnecessary — the cross-backend invariant is pinned in `TestDockerImageBinary_MirrorsK8sOverrides`.
- **Chapter 22** reordered to surface the bundled-image / SCC story before sample output (Sprint 5 tech-writer Issue 14 carry-over).

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> launched alongside the v1.0 tag with **32 chapters + preface + worked-example walkthroughs**. Sprint 6 landed chapters 23-32 (E2E plan, day-2 ops, COS supply chain, troubleshooting, command + config + terraform variable reference, glossary, building from source, extending). Sprint 7 added Mermaid diagrams (architecture, lifecycle, GSLB cross-vantage, execution-backend matrix), rewrote the preface, added per-Part worked-example walkthroughs, re-verified every code example against a fresh workspace, and refreshed PRD 05 §"Phase I" + §"Phase N" step matrices to match the shipped surface.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, DNS probe, lifecycle, …) lives under [`docs/prd/`](docs/prd/). Sprint-by-sprint development history lives in [`docs/PLAN.md`](docs/PLAN.md).

### Deferred (v1.x roadmap)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark v1.x items the v1.0 cut explicitly does NOT ship:

- **Cosign / sigstore release signing** — the `.goreleaser.yml` has a placeholder; the signing infra in `.github/workflows/release.yml` lands in v1.x.
- **Homebrew formula / tap repo** — the `brews:` block is wired but commented out pending an `homebrew-tap` repo.
- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open).
- `--truncated` user-facing CLI flag for the DNS probe (Sprint 6 validator carry-over).
- Cross-driver cluster-sharing for `scripts/e2e-test-full.sh`.
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only).
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker.
- F5 corporate theming for the book.

## v1.0.1 — 2026-05-11

Re-cut of the v1.0 release. The original `v1.0.0` tag landed on an earlier commit than intended, so the sprint 7 polish (32-chapter book pass, Mermaid diagrams, release-pipeline containerisation, README v1.0 rewrite, `--version` book URL, `make release` driver) never made it into the `v1.0.0` binaries on the GitHub Release page. `v1.0.1` is the corrected cut — everything the `v1.0.0` CHANGELOG entry above describes plus the two deltas below. **End users should install v1.0.1**; the `v1.0.0` Release page is retained as a historical artifact only.

### Added

- **`install_build_dependencies.sh`** — per-OS prereq installer (Linux apt / macOS brew / Windows WSL2). Drives the same toolchain the book chapter 4 walks readers through (Go, terraform, docker, mdbook stack for contributors). Idempotent — skips anything already present.
- **Book chapter 4 (`Installing roksbnkctl`)** expanded with per-OS prereq install steps mirroring the installer script, so the path from "fresh box" to "first `roksbnkctl up`" is one block of commands per platform.

### Changed

- **Book CI shifted from build-and-deploy to validate-only.** `.github/workflows/book.yml` no longer publishes to GitHub Pages from CI — the pandoc backend required for the PDF output isn't present on the runner, and pulling the multi-GB `tools/docker/mdbook` image on every push is wasteful. The workflow now runs `mdbook test` + `mdbook build` for syntax and link validation on PRs and pushes to main; publishing is driven locally by the release integrator.
- **New publish targets** in the Makefile: `make book-publish` pushes the locally-built `book/book/html/` tree to the `gh-pages` branch under `/book/` via a `git worktree` round-trip (preserves `.nojekyll`, CNAME, anything else on the branch). `make release-publish VERSION=v1.0.1` runs `book-publish` AND uploads the PDF to the GitHub Release as `roksbnkctl-book-v1.0.1.pdf` via `gh release upload`. The combined effect: a single command from the integrator's machine handles both publish surfaces, with no CI image pull.
- **`book/book.toml`** marks `[output.pandoc]` as `optional = true` so host-install mdbook (no pandoc on PATH) skips PDF rendering with a warning instead of failing the entire build. Fixes the underlying CI failure that prompted this re-cut.
- **`.gitignore`** excludes `.env`, `.env.local`, `.env.*.local` — local-secrets files sourced by `scripts/e2e-test-full.sh`. Never commit (contain `IBMCLOUD_API_KEY`).

### Fixed (CI recovery)

The first v1.0.1 tag-cut surfaced two latent CI bugs that the previous PR-only validate gate had hidden. Both fixed in this same v1.0.1 cut:

- **`.goreleaser.yml`** no longer references `./book/book/pandoc/pdf/book.pdf` via `release.extra_files`. The previous comment claimed goreleaser would warn-and-continue on a missing path; in practice it fail-stops the release. The PDF is now uploaded separately by `make release-publish` (which runs `gh release upload` from the integrator's machine after the CI workflow finishes), so the `extra_files` reference had no remaining purpose.
- **`mdbook test` dropped from `.github/workflows/book.yml`'s validate job.** mdbook's test step invokes rustdoc on every untagged code fence, treating it as Rust by default. This book contains zero Rust (it's a Go project's operator-facing docs; the actual languages used are bash / go / hcl / json / yaml / text / mermaid / powershell), so the test step generated only false positives. The `mdbook build` step still validates markdown rendering, link integrity, and structural correctness.
- **Chapter 31 (`Building from source`)** — three untagged code fences (Go version snippet, `tools/docker/` tree, `dist/` tree) explicitly tagged as `text` so they render identically and don't trip any future code-fence-aware tooling.

### Release-flow documentation

Integrator tag-cut sequence is now:

```sh
make release                 # stamp, build HTML+PDF, lint, snapshot, verify Pages
git add -A && git commit -m "chore: prep v1.0.1 release"
git tag v1.0.1 && git push origin main --tags
# wait for .github/workflows/release.yml to publish the GitHub Release
make release-publish VERSION=v1.0.1
```

The old `.github/workflows/book.yml build-deploy` step is gone. See `Makefile`'s `release-publish` target and the `book-publish` target it composes.

## v1.0.2 — 2026-05-13

Live-run validation pass. The first chained `scripts/e2e-test-full.sh` run (baseline `A-H` followed by the backend matrix `I-N`) against a real IBM Cloud ROKS cluster surfaced ten latent bugs ranging from binary correctness to test-orchestration to terraform cloud-init prep. All fixed in this release.

### Fixed

#### Binary correctness

- **`roksbnkctl test dns` exits non-zero on any non-NOERROR Rcode.** `internal/cli/test.go::runDNSSingleVantage` now treats NXDOMAIN, SERVFAIL, REFUSED, NOTAUTH as failures (exit 1), not just transport-layer TIMEOUT/ERROR. The text rendering already classified them as `⚠` distinct from `✓`; the exit code now mirrors that classification, matching PRD 03's CI-assertion contract.
- **SSH backend strips local-only env vars before propagation.** `internal/exec/ssh.go::mergeSSHEnv` no longer forwards `HOME`, `USER`, `LOGNAME`, `PWD`, `OLDPWD`, `SHELL`, `PATH`, `TMPDIR` from the caller's local shell to the remote shell. These are per-user / per-session values that don't make sense on a different machine — the remote sshd sets them from `/etc/passwd`. Without the filter, the remote `ibmcloud` CLI tried to `mkdir /home/<caller-local-user>` and fail-stopped with `permission denied`.

#### Tools-image architecture

- **`tools/docker/ibmcloud` Dockerfile bundles the `roksbnkctl` binary.** Sprint 5's k8s-backend DNS-probe Job design assumed the bundled tools image carried `/usr/local/bin/roksbnkctl` (per the inline comment at `internal/cli/test.go::runDNSProbeK8s`), but the Dockerfile until now only installed `ibmcloud`. Added a multi-stage build: Stage 1 compiles roksbnkctl from the repo source (so the image's bundled binary matches the host binary's version), Stage 2 copies it into the runtime image alongside `ibmcloud`. `tools/docker/Makefile` shifts the build context to the repo root with `--build-arg ROKSBNKCTL_VERSION/COMMIT/BUILD_DATE` so the bundled binary's `--version` output matches the host's.

#### Terraform / cloud-init

- **Jumphost cloud-init now logs `ibmcloud` in as the `ubuntu` user.** `terraform/modules/testing/main.tf::jumphost_user_data` ran `ibmcloud login --apikey` only as root, leaving the `ubuntu` user's `~/.bluemix/` empty. When `roksbnkctl --on jumphost ibmcloud …` SSHed in as ubuntu, ibmcloud reported `No API endpoint set` and aborted. Added a `su - ubuntu -c "ibmcloud login …"` step (plus container-service + vpc-infrastructure plugin installs under ubuntu's profile).

#### E2E orchestration scripts

- **`scripts/e2e-test.sh` Phases D8 and H are now env-flag-gated.** `SKIP_PHASE_D_DOWN=1` skips the `D8 down` (cluster teardown at end of Phase D); `SKIP_PHASE_H=1` skips the final workspace-delete. Defaults preserve historical behaviour (both phases run). `scripts/e2e-test-full.sh::run_baseline_AtoG` sets both flags when chaining baseline → backends so the cluster + workspace survive the transition — without this the backends driver hit Phase L (`ops install`) against a destroyed cluster.
- **`preflight_ssh_target` in `scripts/e2e-test-backends.sh` seeds `~/.roksbnkctl/known_hosts` via `ssh-keyscan -t ecdsa`** before any SSH-using phase runs. Without this, the first SSH connection in Phase I fail-stopped with `unknown host` because the binary's `--insecure-host-key` flag is silently dropped by `exec --on jumphost` (DisableFlagParsing interaction — see Known v1.0.3 candidates below).
- **LD3 and LD10 capture patterns fixed.** Both were `out=$(cmd || true); rc=$?` which always read `rc=0` regardless of the binary's actual exit code (the `|| true` makes the command substitution return 0 unconditionally). Switched to `set +e; out=$(cmd); rc=$?; set -e`. Side effect: these tests had been silently always-failing since they were written; this is the first release where they actually validate the binary.
- **LD5 assertion string matches the binary's actual JSON output format.** Was `"\"backend\":\"k8s\""` (compact); the binary uses `json.Encoder.SetIndent("", "  ")` and emits `"backend": "k8s"` (with a space). Added the space.
- **Chapter 31 (`Building from source`) — three untagged code fences explicitly tagged as `text`** so `mdbook test` doesn't try to compile them as Rust.

#### CI

- **`.github/workflows/book.yml` no longer runs `mdbook test`.** The step invoked rustdoc on every untagged code fence in the book; this book has zero Rust and the step generated only false positives. The `mdbook build` step still validates markdown rendering, link integrity, and structural correctness.
- **`.goreleaser.yml` no longer references the PDF book via `release.extra_files`.** The previous comment claimed goreleaser would warn-and-continue on a missing path; in practice it fail-stops the release publish. The PDF is now uploaded separately by `make release-publish` (which runs `gh release upload` from the integrator's machine after the CI workflow finishes).
- **`book/book.toml` marks `[output.pandoc]` as `optional = true`** so host-install mdbook (no pandoc on PATH) skips PDF rendering with a warning instead of failing the build.

### Known v1.0.3 candidates

Surfaced during this validation pass; not fixed in v1.0.2 because they require deeper changes:

- **SSH backend `ibmcloud` session refresh.** IBM Cloud IAM tokens expire after ~60 min. Cloud-init's `ibmcloud login` happens at instance-boot time; by the time a 70+ minute cluster bring-up finishes and tests start, the jumphost's ubuntu session is past its TTL. The SSH backend doesn't currently auto-relogin from `IBMCLOUD_API_KEY` before each invocation. Workaround: trigger backend-matrix tests within the session lifetime of cluster bring-up, or manually `ibmcloud login` on the jumphost before each phase.
- **`--insecure-host-key` flag silently dropped by `exec --on jumphost`.** `internal/cli/cluster.go::runExec` sets `DisableFlagParsing` so cobra doesn't grab flags meant for the wrapped binary; this also discards `--insecure-host-key` as a persistent flag. `extractOnFlag` pulls `--on` out manually; needs an analogous `extractInsecureHostKey` to plumb the flag through. Workaround for v1.0.2: the e2e script seeds `~/.roksbnkctl/known_hosts` via `ssh-keyscan` in preflight, sidestepping the binary path entirely.

### Release-flow

Integrator sequence is unchanged from v1.0.1:

```sh
make release VERSION=v1.0.2
git tag v1.0.2 && git push origin main --tags
# wait for .github/workflows/release.yml to publish the GitHub Release
make release-publish VERSION=v1.0.2
```

## v1.1.2 — 2026-05-13

Second CI-recovery patch on top of `v1.1.0` / `v1.1.1`. The `v1.1.1` cut fixed staticcheck (the only CI signal visible at the time) but the fix — removing the unused `ptrInt64` helper — broke a second CI job: `internal/exec/k8s_integration_test.go` uses `ptrInt64` under the `//go:build integration` tag, which staticcheck and the default-tag `go test ./...` don't compile. Functionally identical to `v1.1.0` / `v1.1.1` — release binaries are byte-near-identical (the helper's source-or-no-source state doesn't affect linker output). **End users should install v1.1.2**; `v1.1.0` and `v1.1.1` Release pages are retained as historical artifacts only.

### Fixed (CI recovery, take 2)

- **Restored `ptrInt64` inside `internal/exec/k8s_integration_test.go`** (its sole caller) instead of in `k8s.go`. Lives under the `//go:build integration` tag now, so staticcheck on the default build doesn't see it AND the integration test compiles. Tighter scoping than the v1.1.1 deletion.
- **`Makefile` pre-tag checklist** should grow a `go build -tags integration ./...` step alongside the `staticcheck` step from v1.1.1's note. CI runs three build configurations (default, integration, plus the staticcheck inheritance) and the local gate only ran one — this gap is what produced the v1.1.0 → v1.1.1 → v1.1.2 cascade. Documented here as the lesson; mechanical Makefile update tracked separately.

### Not fixed in v1.1.2

- **Flaky `TestIntegration_Connect_Whoami`** (`internal/remote/`) — the test pulls an sshd container via testcontainers-go, which hits Docker Hub. The runner's anonymous pull was rate-limited (`429 too many requests`) during the v1.1.1 CI run. Not a code regression and not solvable from the source side; tracked as a known intermittent on shared CI infra. Will re-run cleanly when the rate-limit window clears.

## v1.1.1 — 2026-05-13 — SUPERSEDED by v1.1.2

Intended as the CI-recovery patch for `v1.1.0` but turned out to be incomplete — the fix (removing unused `ptrInt64`) broke a second CI job (`internal/exec/k8s_integration_test.go` references the helper under the `//go:build integration` tag). See `v1.1.2` above for the corrected cut. v1.1.1 binaries are functionally identical to v1.1.0 / v1.1.2; only CI plumbing differs.

### Fixed (CI recovery — incomplete)

- **Removed unused `ptrInt64` helper** in `internal/exec/k8s.go` (staticcheck `U1000`). v1.1.2 restored the helper inside the integration test file, the only place that uses it.

## v1.1.0 — 2026-05-13

The first post-v1.0 feature cycle (Sprint 8). Ships the cluster/trial phase split as a first-class command surface — `roksbnkctl bnk up/down` lets you iterate on a BNK trial without destroying its cluster, and the unscoped `roksbnkctl up/down` become shape-aware composites that preserve v1.0.x behaviour byte-for-byte on legacy single-state workspaces. See [PRD 06](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md) for the design rationale and [PLAN.md §"Sprint 8"](docs/PLAN.md) for the cycle's deliverables.

> **CI note**: the `v1.1.0` tag-cut commit failed staticcheck (unused `ptrInt64` helper in `internal/exec/k8s.go`). Functionally inert; `v1.1.1` is the corrected cut. v1.1.0 binaries on the GitHub Release page work, but new installs should use v1.1.1.

### Added

#### Sprint 8 — `bnk` command group + shape-aware lifecycle

- **`roksbnkctl bnk` command group** ([PRD 06 §"Scope"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#scope)) — the trial-layer counterpart to `roksbnkctl cluster`, so the BNK trial can be torn down and re-deployed without destroying the cluster underneath.
  - `roksbnkctl bnk up` — applies the trial phase against an existing cluster (~7 minutes vs ~50 for a full from-scratch deploy). On an empty workspace it offers to bootstrap the cluster phase first (`~30 min ROKS provision + transit gateway + registry COS + cert-manager + jumphost`) with a confirmation prompt; `--auto` threads through both prompts.
  - `roksbnkctl bnk down` — destroys the trial phase only; the cluster persists for the next iteration. Headline win: a `bnk down` / `bnk up` round-trip is the 5-10 minute trial-apply window, not the 30-minute cluster rebuild that a v1.0.x `down` / `up` cost.
  - Flag surface mirrors `cluster up` / `cluster down`: `--auto`, `--var-file` (repeatable), `--no-kubeconfig` on `bnk up`.
- **`config.DetectShape` workspace-shape classifier** ([PRD 06 §"Shape detection"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#shape-detection)) — on-disk-only (no `terraform` calls), parses the workspace's tfstate files and emits one of:
  - `ShapeEmpty` — neither phase has resources.
  - `ShapeClusterOnly` — cluster phase applied, trial empty.
  - `ShapeSplit` — both phases applied independently (the v1.1.0 default for new workspaces).
  - `ShapeLegacySingle` — trial state contains cluster modules (`module.roks_cluster`, `module.cert_manager`, `module.testing`) from a pre-split `roksbnkctl up`. Verified against the real `canada-roks` workspace (135 resources).
  - Missing tfstate files → treated as "no resources". Malformed JSON → surfaced as error so dispatch doesn't silently misroute.
- **Shape-aware refusal messages** on the phase-scoped commands. Every refusal names the verb that would actually work. The full catalogue is in [Chapter 11 §"Refusal messages catalogue"](https://jgruberf5.github.io/roksbnkctl/book/11-tearing-down.html#refusal-messages-catalogue); the highlights:
  - `cluster up` / `bnk up` / `bnk down` refuse on `ShapeLegacySingle` — there's no way to isolate the cluster or trial phase when both share one tfstate. Points readers at `roksbnkctl up` / `down` for the in-place v1.0.x behaviour.
  - `cluster down` refuses on `ShapeSplit` with a hard error pointing at `bnk down` first (replaces the v1.0.x warning-but-prompt — see §"Changed" below).
  - `bnk down` refuses on `ShapeEmpty` and `ShapeClusterOnly` ("no BNK trial state to destroy in this workspace").
- **Book chapter edits** for the new surface:
  - **Chapter 8** — reframed from "opt-in two-phase mode" to "the default for new workspaces", with a new §"Legacy single-state workspaces" subsection that helps v1.0.x users identify their shape.
  - **Chapter 10** — new §"The `bnk up` / `bnk down` command group" with the bootstrap-prompt sample output, the four-shape dispatch matrix (user-facing simplification of [PRD 06 §"Dispatch table"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table)), and a worked iteration example showing the explicit time savings.
  - **Chapter 11** — new §"The phase-aware decision tree" at the top + §"Refusal messages — catalogue" near the middle; "two destroys" → "three destroys" with `bnk down` documented alongside `down` and `cluster down`.

### Changed

- **`roksbnkctl up` and `roksbnkctl down` are now shape-aware composites** ([PRD 06 §"Dispatch table"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#dispatch-table)). Their semantics shift from "monolithic apply/destroy against the trial state" to "detect the workspace shape and dispatch to the right phase commands in the right order":
  - **Split / Empty / ClusterOnly**: `up` runs `cluster up` (provision or refresh) then trial up; `down` runs trial down then `cluster down`.
  - **LegacySingle**: `up` and `down` run the v1.0.x monolithic trial apply / destroy **byte-for-byte** — same plan output, same resource count. v1.0.x workspaces continue to work without migration.
  - **Empty** + `down`: errors `nothing to destroy in this workspace` (was: same error, semantics unchanged).
  - The composites are pure dispatchers — no business logic of their own. The leaf commands (`runTrialUp`, `runTrialDown`, `runClusterUp`, `runClusterDown`) carry the apply / destroy logic.
  - Implementation: `internal/cli/lifecycle.go` renames the existing `runUp` / `runDown` bodies to `runTrialUp` / `runTrialDown` (the v1.0.x behaviour, factored out) and introduces the composite `runUp` / `runDown` keyed on `config.DetectShape`. `internal/cli/cluster_phase.go` and `internal/cli/bnk_phase.go` add the refusal logic.
- **`roksbnkctl cluster down` enforces trial-then-cluster ordering with a hard refusal**, replacing the v1.0.x warning-but-prompt copy. Previously, `cluster down` would warn `Any BNK trial state on top of this cluster will be orphaned — run roksbnkctl down first if needed` and proceed on confirm; with `--auto` it would proceed silently. v1.1.0 instead refuses with ``BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)`` — and `--auto` does **not** bypass it (correctness, not confirmation, is the issue). The motivating case: `scripts/e2e-test.sh` runs that destroyed the cluster while trial finalisers were still pending now fail loudly instead of silently leaking resources.

### Fixed

Carry-in fixes to the `--backend docker` and `--backend k8s` paths, folded into v1.1.0 alongside the phase-split work rather than cut as a separate `v1.0.3` patch (no functional change to the cluster/trial split itself; these touch `internal/exec/` only):

- **`--backend docker` for `ibmcloud` was silently broken** — the docker SDK path materialised `IBMCLOUD_API_KEY` as a defined-but-empty env var in the container (the v1.0.x `Env: ["IBMCLOUD_API_KEY"]` bare-name form, which works for the docker CLI's `--env VAR` but not the SDK). Phase K e2e tests false-positive-matched the ibmcloud help banner. v1.1.0 passes `IBMCLOUD_API_KEY=<value>` (and `IC_API_KEY`, `TF_VAR_ibmcloud_api_key`) explicitly. Trade-off noted in [`internal/exec/docker.go`](internal/exec/docker.go) `buildMountsAndEnv` doc: the api key is now visible in `docker inspect` output until the Phase M2 cred audit closes the cred-tmpfile-bind-mount design (deferred per PLAN.md).
- **Host env vars (`HOME`, `USER`, `PATH`, `SHELL`, …) no longer leak into the container.** `internal/exec/docker.go::buildContainerEnv` now filters a host-only set. Previously the bundled `ibmcloud` image's plugin lookup landed at `/home/<user>/.bluemix/plugins/` (host path) instead of `/root/.bluemix/plugins/` (image's `$HOME`) and the plugin list came back empty.
- **`ibmcloud` invocations now self-prime with `ibmcloud login` inside the container.** Both backends apply a `sh -c 'ibmcloud login … --quiet >/dev/null 2>&1 && exec ibmcloud "$@"'` wrap before stateful subcommands (`iam`, `ks`, `account`, `target`, …) so the container's cold-start `$HOME/.bluemix` doesn't error with "Not logged in". `login` / `logout` skip the wrap. Region defaults to `$IBMCLOUD_REGION` or `us-south`. Docker applies the wrap via `dockerImageBinary["ibmcloud"]`; k8s applies the same wrap dynamically in `runOnOpsPod` (no static `jobToolCmdOverride` entry needed).
- **K8s Job `Container.Command` / `Args` shape corrected** for tools without a `jobToolCmdOverride`. v1.0.x set `Command = argv[1:]`, which **overrides** the image's ENTRYPOINT — the kubelet then tried to exec the first arg (e.g., `-c` for an iperf3 client) as the binary, producing `CreateContainerError`. v1.1.0 sets `Args = argv[1:]` so the image's ENTRYPOINT picks the binary (which is what the inline comment had always claimed). Fixes the L2 throughput Job's `--backend k8s` execution.
- **`iperf3` tool image switched to `networkstatic/iperf3:latest`** (public on Docker Hub) from `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<tag>` (private — ROKS workers can't pull without an image-pull-secret). The bundled image returns in v1.x once the ghcr package is flipped public or a per-pod pull-secret is wired per PRD 03 §"K8s backend image pull".
- **`-w` / `--workspace` flag no longer leaks through `roksbnkctl kubectl` / `oc` / `ibmcloud` passthroughs.** `internal/cli/cluster.go::extractWorkspaceFlag` mirrors the existing `extractOnFlag` and strips the root persistent flag from passthrough argv (cobra's `DisableFlagParsing` couldn't see it).
- **Unit tests refreshed to match the new shapes**: `TestResolveDockerImageAndArgv` covers the sh-c wrap for `ibmcloud` and the public iperf3 image; `TestDockerImageBinary_MirrorsK8sOverrides` adds a `mirrorExempt` set for `ibmcloud` (the docker static wrap and k8s dynamic wrap are equivalent at exec time but the map shapes diverge by design); `TestRunOpts_TFVarsEnvPassthrough` asserts host-only vars are filtered in addition to TF_VAR_* being passed through.

### Deferred (v1.x roadmap, post-v1.1.0)

Not in v1.1.0 — see [PRD 06 §"Out of scope"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#out-of-scope) for full rationale.

- **`roksbnkctl migrate`** — splitting a legacy single-state workspace's tfstate into separate `state/` + `state-cluster/` trees via `terraform state mv`. Real engineering work and one-shot state surgery. Deferred until a real legacy user asks; refusal messages reference it as future work so the wording stays valid when it lands.
- **`roksbnkctl bnk plan` / `bnk apply` / `cluster plan` / `cluster apply`** — top-level `plan` / `apply` already operate on the trial state and that behaviour is unchanged. Symmetry additions deferred to a later cycle.
- **Docker-backend composition** for the composite `up` / `down` on empty/split workspaces — `cluster up` has no docker shortcut today, so composing it with a docker-backend trial apply would mix backends mid-run. The composite explicitly disables itself on non-local backends for the empty/split paths; legacy single-state and the direct `cluster up` / `bnk up` calls retain v1.0.x docker behaviour. Full multi-phase docker composition is a follow-up PRD.
- **Multi-trial UX** — a cluster can host multiple BNK trials in principle (different workspaces sharing `cluster-outputs.json`); polish around naming trials and "which trial is current" prompts is deferred.
