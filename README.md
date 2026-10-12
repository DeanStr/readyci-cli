# Ready CI CLI

This is the public Ready CI CLI and shared protocol library module. The hosted
platform and dashboard are separate, proprietary components and are not included.

Official source and signed downloads:
[DeanStr/readyci-cli](https://github.com/DeanStr/readyci-cli).
We accept bug reports, but not external code contributions at this time.

Ready CI is the verification protocol for coding agents: plan the right check,
run it remotely, classify failures, preview generated artifacts, and report
proof without scraping logs.

License: Apache-2.0. See [NOTICE](NOTICE), [TRADEMARKS.md](TRADEMARKS.md),
[CONTRIBUTING.md](CONTRIBUTING.md), [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md),
and [SECURITY.md](SECURITY.md) for the public repository policies.

Installation and source-build instructions live in [docs/install.md](docs/install.md).
The public documentation site has the complete
[CLI reference](https://readyci.com/docs/cli),
[configuration reference](https://readyci.com/docs/configuration),
[workflow guides](https://readyci.com/docs/workflows), and
[troubleshooting guide](https://readyci.com/docs/troubleshooting).

Ready CI is pre-release and does not preserve obsolete configuration aliases.
Use `.readyci.yml`, `.readyciignore`, `checks:`, and the `READYCI_*` environment
namespace; `.preflight.yml`, `.preflight.yaml`, `.preflightignore`, and
`commands:` are rejected.

## Agent Verification Workflow

Run an arbitrary command against the current Git worktree:

```bash
readyci run --changes working "make check-touched"
readyci run --changes staged "make check-staged"
readyci run --runner-size large --timeout 75m "make check-all"
readyci run --docker --network-mode none --runner-size xlarge --timeout 90m "make check-ci-db"
```

`--changes` controls the uploaded changed-file manifest:

- `working`: unstaged, staged, and untracked files.
- `staged`: staged files only.
- `all`: `git diff HEAD` plus untracked files.

Inside the runner, Ready CI writes that manifest to Git metadata and exports `CHECK_TOUCHED_FILES_FROM`, `READYCI_CHANGED_FILES`, and `READYCI_CHANGE_MODE`, so scripts such as `scripts/check-touched.sh` can use the same narrow-check flow they use locally. Check-level `sync.include_untracked` can override the project default, so staged/pre-commit checks can avoid uploading untracked workspace files while edit-loop checks still include them. `readyci affected` treats untracked files as part of the working/all edit loop unless a check explicitly sets `sync.include_untracked: false`. `readyci sync --dry-run` shows the untracked-file count and sample, and real runs warn before upload when untracked files are included. Check-level `sync.exclude` adds to the project exclude list.

Use `readyci affected` for the standard changed-file workflow. If `affected.rules` or `affected.profiles` is configured, Ready CI maps changed path globs to one or more configured checks and runs only those checks. Local built-in profiles are the conservative baseline `docs`, `go`, `readyci`, and `shell`; use `readyci plan --recommend --suggest-config` for hosted frontend, OpenAPI, DB, ops, Rust, and other richer packs. Profiles only select checks that already exist in the config, so explicit `affected.rules` can always refine the shape. Rules can use `paths_from` to derive path globs from a repository manifest, for example `file: scripts/targets.txt` with `template: tests/${line}.rs`, which keeps generated fixture/test-target lists and affected routing in sync. `readyci affected explain` and `readyci affected audit --json` include the manifest file and line number behind derived patterns. Use rule-level `exclude` to keep broad catch-alls from overlapping precise rules, and use `affected.subsumes` to say an aggregate check replaces narrower checks when both match. Set `affected.unmatched_checks` to one or more comprehensive checks when unknown paths should run a safe default; unlike a `"**"` rule, this applies only when no ordinary rule matched, so it preserves selective routing. Leaving it unset keeps strict fail-closed coverage. Without rules, Ready CI auto-selects a configured `affected` check, falls back to the common `touched` check used by existing projects, then tries narrow standard checks such as `go` or `web`. Use `readyci affected --staged` for staged/pre-commit verification, pass `--check NAME` to choose a specific configured check, run `readyci affected explain --why-not PATH` to see why a path selects or skips checks, and run `readyci affected audit --examples --duplicates` to test fixtures, find uncovered source-like files, and catch redundant aggregate+narrow check selections. Add `affected.audit_ignore` for tracked legacy/vendor/data paths that should be intentionally exempt from strict audit coverage without changing uploads or affected execution. `readyci affected suggest` prints a starter affected-rule/test/ignore snippet and includes Cargo dependency-graph rules when matching `rust-*` package checks exist; `readyci affected cargo` prints full Cargo package check/rule scaffolding from `cargo metadata`. `readyci affected audit --fix` can append missing fixture tests to the config; add `--fix-ignore-unmatched` only for files you intentionally want to add to `affected.audit_ignore`.

Solo developers and small teams can use `readyci check` directly and treat its process exit as the check result; no registered policy is required. Use `readyci verify` when a coding agent or protected workflow needs a service-authorized, workspace-bound verdict. Registered policy remains an explicit advanced control until Ready CI has a provider-verified default-branch source; candidate workspace bytes are never promoted automatically.

Named checks can live in `.readyci.yml`:

```yaml
project_id: airline
region: default
cache:
  paths:
    - ~/.cargo
    - ~/.cache/ms-playwright
    - apps/web/.next/cache
  key_files:
    - Cargo.lock
    - apps/web/package.json
    - apps/web/pnpm-lock.yaml
artifacts:
  paths:
    - coverage/**
    - test-results/**
    - apps/web/playwright-report/**
affected:
  profiles:
    - shell
  unmatched_checks:
    - affected
  audit_ignore:
    - legacy/**
    - vendor/**
  subsumes:
    web:
      - web-typecheck
      - web-test
  rules:
    - paths:
        - apps/web/src/**
      exclude:
        - apps/web/src/routes/**
      checks:
        - web-typecheck
        - web-test
    - paths:
        - apps/web/package.json
        - apps/web/src/routes/**
      checks: web
    - paths:
        - docs/**
      checks: docs-lint
  tests:
    - path: apps/web/src/app.tsx
      checks:
        - web-typecheck
        - web-test
checks:
  affected:
    command: make check-touched
    prepare:
      - scripts/bootstrap-web-deps.sh
    changes: working
    retries: 1
    matrix:
      NODE_VERSION: ["22", "24", "26"]
  staged:
    command: make check-staged
    changes: staged
    sync:
      include_untracked: false
  web-typecheck:
    command: npm run typecheck
  web-test:
    command: npm test
  web:
    command: npm run web-check
  docs-lint:
    command: make docs-lint
  shell-syntax:
    command: bash scripts/readyci-check-shell-syntax.sh
  db:
    command: make check-ci-db
    region: default
    always:
      - scripts/collect-db-logs.sh
    docker: true
    network_mode: none
    concurrency_group: db
    concurrency_limit: 1
    keep_failed: true
```

For Playwright browser tests, set `browser: chromium` on the check to run
`playwright install --with-deps chromium` before the main command, and cache
`~/.cache/ms-playwright` so each project can choose its own Playwright version.
Use `outputs.reviewable` for generated fixtures or reports that should be
retained as artifacts without duplicating them under `artifact_paths`.

Then run:

```bash
readyci check --list
readyci affected --dry-run --json
readyci affected --summary
readyci affected --json
readyci affected explain apps/web/src/App.tsx
readyci affected explain --why-not legacy/old.sql
readyci affected audit --examples --duplicates
readyci affected suggest
readyci affected
readyci affected --staged
readyci warm --check web
readyci warm --profile dependencies
readyci check affected
readyci check staged
readyci check db
```

`readyci affected --json` creates runs and emits an execution report. Configured dependency preparation is automatic and included in the same session; `readyci verify --json --progress=status` performs that step automatically when the repository defines the explicit `dependencies` check. All warmed targets must use the same cache-lane size and key namespace. The dependency phase produces one cache-bound warm node per selected runner size instead of inferring checks or expanding nodes for unrelated cache quotas. Each mixed-runner node mounts the union of target cache paths because cache-lane affinity is project/worker scoped rather than runner-size scoped. A native warm node is placed on a Docker-capable worker when its targets need Docker, and parallel warm or target matrices are rejected because one warm node cannot provision multiple worker-local lanes. Dependency warming has no implicit unrestricted-network fallback: configure `network.warm_mode: allowlist` and `network.warm_allowlist` for public deployments. `network.warm_mode: default` is an explicit local/private-only option. The service rebuilds authorized warm nodes from the active registered policy instead of trusting their commands from the client proposal. An explicit `readyci verify --check dependencies` is itself a scoped warm operation: it targets the consumer checks selected by affected-file policy for the current workspace, including their runner sizes and cache paths, and does not run the dependency command a second time as a no-network verify node.

Configured `cache.paths` are linked into the persistent `/cache` lane before the command runs. Optional `cache.key_files` hash lockfiles and toolchain files into the cache target, which gives projects fresh dependency caches when files such as `Cargo.lock`, `go.sum`, `pnpm-lock.yaml`, `package-lock.json`, `poetry.lock`, or `uv.lock` change. Checks may also define `prepare`, `post`, and `always` shell phases around the main command. Use `concurrency_group` and `concurrency_limit` for checks that touch shared external state, such as DB/Testcontainers suites that should run one at a time. Concurrency is scoped per team, project, and group; `concurrency_group` defaults to a limit of 1, higher limits allow bounded parallelism, and `concurrency_limit: 0` opts a check out of an inherited project-level group. `keep_failed: true` or `--keep-failed` uploads `.readyci/failed-workspace.tar.gz` as a downloadable artifact on command failure, and also writes a copy to `/cache/failed-runs/$READYCI_RUN_ID/workspace.tar.gz` when a persistent cache lane is attached.

Use `region` in `.readyci.yml`, check-level `region`, or `--region` to pin a run to a worker location. Workers with no configured location advertise the `default` region. Use `runner_size` in `.readyci.yml`, check-level `runner_size`, or `readyci run --runner-size` for ad hoc commands that need a larger runner than the project default. Use `timeout` in `.readyci.yml`, check-level `timeout`, or `readyci check --timeout` / `readyci run --timeout` when a run needs a longer deadline than the project default.

Configured `artifacts.paths` are collected from `/workspace` after the command exits and uploaded as a gzip tar archive. Missing artifact paths are ignored. Download retained artifacts with:

```bash
readyci artifacts ls RUN_ID
readyci artifacts download RUN_ID
readyci runs artifacts --download RUN_ID
readyci artifacts apply RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
readyci fixtures update --from latest --paths 'tests/fixtures/**'
```

Use `readyci artifacts apply` for generated fixture workflows where the remote
run produced files that should come back into your local working tree. It
requires at least one `--paths` pattern, validates archive paths with the same
workspace-relative rules used by artifact upload, supports `--dry-run` to
preview the files before writing them, and supports `--json` for structured
results. The command exits non-zero when no archive files match unless
`--allow-empty` is set. Archive validation finishes before any file is applied,
but the final rename step is not transactionally atomic across multiple files:

```bash
readyci artifacts apply RUN_ID --paths 'tests/fixtures/**' --dry-run
readyci artifacts apply RUN_ID --paths 'tests/fixtures/**' --dry-run --json
readyci artifacts apply RUN_ID --paths 'tests/fixtures/**' --allow-empty
readyci artifacts apply RUN_ID --paths 'tests/fixtures/**' --paths 'docs/calibration-artifacts/**'
readyci fixtures update --paths 'tests/fixtures/**' --dry-run
```

Agents should prefer `readyci agent artifacts plan/apply`. The plan command
returns artifact metadata, selected path patterns, a dry-run apply command, and
a confirm command in `readyci.verification.v0alpha1` JSON. Agent apply previews by default
and only writes files when `--confirm` is present:

```bash
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
```

Helpful workflow commands:

```bash
readyci setup --write-config --check affected
readyci setup --write-config --write-makefile --write-helpers --profile strict
readyci setup --makefile
readyci setup --helper shell-syntax --write-helper
readyci setup --helper ops-config --write-helper
readyci setup --print
readyci config validate
readyci config explain
readyci affected --dry-run
readyci affected explain apps/web/src/App.tsx
readyci affected explain --why-not legacy/old.sql
readyci affected audit --strict --duplicates
readyci affected suggest
readyci affected cargo
readyci warm --check ci
readyci affected --watch
readyci sync --dry-run
readyci doctor
readyci doctor --json
readyci usage
readyci project set --project airline --github-repo owner/airline --default-branch main
readyci secrets list --project airline
readyci secrets set --project airline --from-env READYCI_TOKEN READYCI_TOKEN
readyci runs latest --project airline
readyci runs status RUN_ID
readyci runs cancel RUN_ID
readyci runs status --admin-token "$READYCI_ADMIN_TOKEN" RUN_ID
readyci runs explain --json RUN_ID
readyci runs logs --tail 200 --json RUN_ID
readyci runs logs --errors-only --jsonl RUN_ID
readyci groups latest --project airline
readyci groups status GROUP_ID
readyci groups logs GROUP_ID
readyci groups cancel GROUP_ID
readyci agent setup
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --idempotency-key "$TASK_ID"
readyci verify --detach --json --intent edit-loop
readyci sessions retry --json SESSION_ID
readyci sessions rerun --json SESSION_ID
readyci agent instructions doctor --json
readyci affected --watch
readyci check --retry 2 --open ci
```

`readyci agent setup` installs Ready CI verification as a repo skill at
`.agents/skills/readyci-verify/SKILL.md` and, by default, writes the Claude Code
compatibility copy at `.claude/skills/readyci-verify/SKILL.md`, then runs the
instructions doctor. It does not write `AGENTS.md` unless you pass
`--agents-md`; use that opt-in for agents that do not support skills or for
teams that want always-loaded repo guidance. See the public
[workflow guides](https://readyci.com/docs/workflows) and
[CLI reference](https://readyci.com/docs/cli) for the portable agent protocol.

Run groups provide read and cancellation views for matrix and affected runs.
Use `readyci groups latest`, `readyci groups status`, `readyci groups logs`, and
`readyci groups cancel` to inspect or stop grouped child runs. Client-side group
retry and rerun were removed because reconstructing execution from an old
`RunSpec` cannot reproduce the exact workspace and policy contract. Durable
verification retries and successors belong to the sealed session API.

Use `readyci sessions retry SESSION_ID` to create a server-owned successor from
the exact retained source. Use `readyci sessions rerun SESSION_ID` to package
the current workspace with current local configuration and active registered
source policy while reusing the predecessor recipe's logical defaults. A rerun
does not fall back to historical source exclusions. Both require a terminal predecessor; retry
fails closed when the retained source or frozen policy revision is unavailable.

`readyci sync --dry-run` previews the bundle that would be uploaded without creating a run. Use `--check NAME` to inherit that check's `changes` mode, `--changes MODE` to override it, and `--json` when an agent or script needs exact counts and paths. `readyci check --dry-run --json NAME` adds the resolved command phases, image, runner size, network mode, cache paths, cache key files, artifacts, retries, and matrix variants to that sync preview. `readyci check --summary NAME` and `readyci affected --summary` print a compact dry-run view with command, image, services, timeout, artifacts, changed-file counts, and warnings such as artifact paths that are not mentioned by any command phase. After real runs, the worker also emits a stderr warning for any configured concrete artifact path that matched no files in the completed workspace.

`readyci setup` is the safe first-run assistant. It guides you through sign-in, reviews detected configuration, asks before saving it, previews the selected check, and asks before running it remotely. Use `--yes` with existing credentials for automation, or `--preview` / `--json` for a report without writes or execution. Add `--write-config` to create `.readyci.yml` from the same detection logic as `readyci setup --print`, `--write-makefile` to write `readyci.mk`, `--write-helpers` to install the standard shell/ops helper scripts, `--makefile` to print stable Makefile wrapper targets for `readyci affected`, staged affected checks, warm runs, release runs with explicit network/runner/timeout flags, and affected audit, or `--helper shell-syntax --write-helper` / `--helper ops-config --write-helper` to install one helper script. Use `--profile strict` when you want generated configs to default to `network: none` after dependency warm-up, and use `readyci setup --print` when you only want to inspect the generated config. Rust workspaces can run `readyci affected cargo` to print package-aware affected-rule scaffolding from `cargo metadata`, including reverse dependents.

`readyci doctor --check NAME` verifies local Git state, API health, auth, project settings, and fresh upload-capable worker matching when an admin token is available. Add `--json` when an agent or script needs structured diagnostics and exact next commands.

`readyci usage` prints the authenticated team's current plan limits and usage counters in text or JSON.

`readyci verify`, `readyci sessions get/watch/cancel/retry/rerun`, `readyci agent plan/explain`, `readyci agent artifacts plan/apply`, `readyci runs logs/artifacts`, and `readyci artifacts download` are the agent-friendly surface for a scoped API token. They avoid admin-only endpoints for normal project workflows, so coding agents can submit a sealed verification session, reconnect to it, read structured failure diagnostics, preview generated artifact handoff, and download artifacts without receiving operator credentials. JSON reports follow `schema/readyci.verification.v0alpha1.schema.json` and include snapshot, plan, coverage, node, attempt, verdict, and repair-oriented fields.

Ordinary `run`, `check`, and `affected` executions exclude project secrets unless `--request-secrets` is supplied; server scope and secret-release policy still apply, and automatic dependency preparation always excludes secrets. Human execution displays live test stdout/stderr; `--json` emits only the final result document and `--detach` does not watch. Project secrets are write-only in API/dashboard responses and are injected into the worker environment only for claimed runs in that project. Use `readyci login` for human CLI access; it prints the login URL and opens a browser when possible. Use `readyci login --no-browser` when you want to copy the URL into another browser on the same workstation, including WSL setups where the Windows browser can reach the WSL localhost callback. For SSH or remote terminals, use SSH port forwarding for the callback or prefer an explicit scoped token via `READYCI_API_TOKEN`. Use explicit scoped `READYCI_API_TOKEN` values for automation or AI coding agents.

Operator tokens can manage worker inventory with `readyci admin workers`,
`readyci admin workers drain`, `readyci admin workers undrain`, and
`readyci admin workers delete`. Delete is for retired workers: the worker must
already be draining and have no active leased runs. Drain and delete require an
operator-authored `--reason`, for example
`readyci admin workers delete --reason "retired host" WORKER_ID`.

Agent token baseline:

```bash
export READYCI_API_URL="https://api.example.com"
export READYCI_PROJECT_ID="owner/repo"
export READYCI_API_TOKEN="pfl_..."

readyci auth status
readyci agent setup
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --idempotency-key "$TASK_ID"
readyci verify --json --progress=status --intent final-handoff --idempotency-key "$TASK_ID"
readyci sessions watch --json "$SESSION_ID"
readyci agent instructions doctor --json
readyci runs latest --project "$READYCI_PROJECT_ID" --json
```

For a normal project verification agent, prefer `readyci agent token create --preset verify-only --ttl 30d`, which requests `runs:create,runs:read,cache:read,projects:read,account:read` and excludes secrets/cache purge by default. Add `verify-with-cache-purge`, `verify-with-secrets`, or broader write scopes only for agents that intentionally need those capabilities; `verify-with-secrets` must be paired with `readyci verify --request-secrets` before a submitted run requests authorized project secrets. Agent verify run reports include `classification`, `retryable`, `agent_action`, and `summary`. API run summaries/details can include `failure_summary` and `primary_file`; `readyci agent explain --json RUN_ID` includes matched evidence lines and a `repair` packet for test, lint, typecheck, build, auth, sync, worker, network, dependency, timeout, and cancellation failures.
