# Ready CI CLI Install

The Apache-2.0 CLI and shared contracts are published at
[DeanStr/readyci-cli](https://github.com/DeanStr/readyci-cli).
After installation, use the public [CLI reference](https://readyci.com/docs/cli),
[configuration reference](https://readyci.com/docs/configuration), and
[troubleshooting guide](https://readyci.com/docs/troubleshooting).

## Requirements

- Git, so workspace sync can build an accurate dirty-worktree bundle.
- A Ready CI account, or a scoped bearer token for automation.
- [Minisign](https://jedisct1.github.io/minisign/) for signed binary installation.

## Release Artifact

Official binaries are published as signed assets on
[GitHub Releases](https://github.com/DeanStr/readyci-cli/releases).
Use the pinned beta below; GitHub's `latest` endpoint does not select prereleases.
Verify the installer signature before running it:

```bash
(
  set -eu
  export READYCI_RELEASE_BASE_URL="https://github.com/DeanStr/readyci-cli/releases/download/v0.1.0-beta.9"
  export READYCI_RELEASE_MINISIGN_PUBKEY="RWSiWC6F4tE+M458zvmqGzOcQZvYD/Y6ww1/eo3Q88S+d3HuiXGK5wAE"
  installer_dir="$(mktemp -d)"
  trap 'rm -rf "$installer_dir"' EXIT
  curl -fsSLo "$installer_dir/install.sh" "$READYCI_RELEASE_BASE_URL/install.sh"
  curl -fsSLo "$installer_dir/install.sh.minisig" "$READYCI_RELEASE_BASE_URL/install.sh.minisig"
  minisign -Vm "$installer_dir/install.sh" -x "$installer_dir/install.sh.minisig" -P "$READYCI_RELEASE_MINISIGN_PUBKEY"
  bash "$installer_dir/install.sh"
)
```

The generated release installer defaults to its own release version, embeds the Ready CI minisign public key, verifies the signed `SHA256SUMS`, detects Linux/macOS and amd64/arm64, verifies the archive checksum, and installs to `$HOME/.local/bin` by default. Override the destination with `READYCI_INSTALL_DIR`. For manual macOS installs, use `shasum -a 256 readyci_darwin_arm64.tar.gz` or the installer instead of `sha256sum`.

On Windows PowerShell, install `minisign.exe`, then use the signed PowerShell
installer from the same release directory:

```powershell
& {
    $ErrorActionPreference = 'Stop'
    $env:READYCI_VERSION = 'v0.1.0-beta.9'
    $env:READYCI_RELEASE_BASE_URL = 'https://github.com/DeanStr/readyci-cli/releases/download/v0.1.0-beta.9'
    $readyciMinisignPublicKey = 'RWSiWC6F4tE+M458zvmqGzOcQZvYD/Y6ww1/eo3Q88S+d3HuiXGK5wAE'
    $bootstrapDir = Join-Path ([IO.Path]::GetTempPath()) ('readyci-bootstrap-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $bootstrapDir | Out-Null
    try {
        $installer = Join-Path $bootstrapDir 'install-readyci.ps1'
        $installerSignature = "$installer.minisig"
        Invoke-WebRequest "$env:READYCI_RELEASE_BASE_URL/install.ps1" -OutFile $installer
        Invoke-WebRequest "$env:READYCI_RELEASE_BASE_URL/install.ps1.minisig" -OutFile $installerSignature
        $minisign = Get-Command minisign.exe -CommandType Application -ErrorAction Stop
        & $minisign.Source -Vm $installer -x $installerSignature -P $readyciMinisignPublicKey
        if ($LASTEXITCODE -ne 0) { throw 'Ready CI installer signature verification failed' }
        & $installer
        readyci version
    } finally {
        Remove-Item -LiteralPath $bootstrapDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
```

The generated `install.ps1` defaults to its own release version, detects Windows amd64/arm64, verifies the signed
`SHA256SUMS` with Minisign, validates the ZIP checksum, installs
`readyci.exe` under `%LOCALAPPDATA%\Programs\ReadyCI`, and adds that directory
to the user `PATH`. Override the destination with `READYCI_INSTALL_DIR`, or set
`READYCI_INSTALL_UPDATE_PATH=0` to leave `PATH` unchanged. Like the shell
template, the source `scripts/install-readyci.ps1` fails closed until a
release key is embedded or explicitly supplied.

Do not curl the raw `scripts/install-readyci.sh` source script as the normal install path. The source template intentionally has no embedded release key and will fail closed unless you provide `READYCI_MINISIGN_PUBLIC_KEY` or explicitly set `READYCI_ALLOW_UNSIGNED_INSTALL=1` to accept checksum-only verification from the same release origin.

All official release archives, checksums, manifests, installers, and the generated
Homebrew formula have Minisign signatures. The trust anchor is
[`release/minisign.pub`](https://github.com/DeanStr/readyci-cli/blob/main/release/minisign.pub) in the public source repository.
Do not disable signature verification for official downloads.

The release public key is:

```text
RWSiWC6F4tE+M458zvmqGzOcQZvYD/Y6ww1/eo3Q88S+d3HuiXGK5wAE
```

## Development Checkout

Clone the public repository and build without the private monorepo:

```bash
git clone https://github.com/DeanStr/readyci-cli.git
cd readyci-cli
```

From that checkout:

```bash
GOWORK=off GOTOOLCHAIN=go1.25.13+auto go install ./cmd/readyci
```

If `$GOBIN` is not set, Go installs the binary in `$(go env GOPATH)/bin`. Add that directory to `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
readyci --help
```

For a local binary without modifying `PATH`:

```bash
GOWORK=off GOTOOLCHAIN=go1.25.13+auto go build -o ./bin/readyci ./cmd/readyci
./bin/readyci --help
```

## Configuration

For a human workstation, prefer browser login:

```bash
export READYCI_API_URL="https://api.readyci.com"
readyci login
readyci auth status
```

For a team with an operator-provisioned enterprise connection, select that
connection explicitly:

```bash
readyci login --sso acme
```

The resulting token remains bound to the SSO connection and is revoked when an
operator disables that connection.

`readyci login` always prints the login URL and opens a browser when possible. Use
`readyci login --no-browser` when you want to copy the URL into another browser on
the same workstation, including WSL setups where the Windows browser can reach
the WSL localhost callback. For SSH or remote terminals, use SSH port forwarding
for the callback or prefer an explicit scoped token via `READYCI_API_TOKEN`;
copying the URL into a browser on a different machine will not reach the CLI's
`127.0.0.1` listener by itself.

The login command opens the Ready CI/ZITADEL browser flow, listens on a `127.0.0.1` callback, exchanges a one-time code for a scoped Ready CI API token, and stores it under your user config directory. `readyci auth logout` revokes the stored token and removes the local credential.

For CI jobs, AI coding agents, and non-interactive automation, use an explicit scoped API token instead of browser login:

Set the values once in your shell profile or CI environment:

```bash
export READYCI_API_URL="https://api.readyci.com"
export READYCI_API_TOKEN="pfl_..."
export READYCI_PROJECT_ID="owner/repo"
```

Agent-friendly verification commands:

```bash
readyci auth status
readyci setup --json
readyci agent setup
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
readyci verify --json --progress=status --intent final-handoff --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
readyci sessions watch --json "$SESSION_ID"
readyci agent instructions doctor --json
readyci runs latest --project "$READYCI_PROJECT_ID" --json
readyci runs explain --json RUN_ID
readyci runs logs --tail 200 --json RUN_ID
readyci runs logs --errors-only --jsonl RUN_ID
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
readyci artifacts download RUN_ID
readyci feedback --run latest --category bug --message "The latest run failed in a confusing way"
```

`readyci agent setup` writes `.agents/skills/readyci-verify/SKILL.md` and the
Claude Code adapter at `.claude/skills/readyci-verify/SKILL.md`, then verifies
the install. It leaves `AGENTS.md` unchanged unless you pass `--agents-md`.
For a manual fallback, run
`readyci agent instructions print --target skill > .agents/skills/readyci-verify/SKILL.md`
from the repo root after creating the directory.

See the public [workflows guide](https://readyci.com/docs/workflows) for agent
setup and verification workflows.

Feedback submitted with a logged-in CLI token uses the authenticated user's email/name as the reply contact. Automation that uses a static or agent token can add `--contact-email` and `--contact-name` so the support inbox has a reply target.

Use the narrowest token that fits the agent's job. `readyci agent token create --preset verify-only --ttl 30d` creates the normal verification shape: `runs:create,runs:read,cache:read,projects:read,account:read`. The command uses the logged-in CLI token when `READYCI_API_TOKEN` is unset, but that parent token must have `tokens:write`; owner/admin users can also create equivalent scoped tokens from the dashboard API tokens panel. Use `readyci agent token create --preset verify-with-feedback --ttl 30d` when the agent should also submit product feedback, use `verify-with-cache-purge` only when the agent should purge cache lanes, use `verify-with-secrets` only when submitted runs should receive authorized project secrets and pair those runs with `readyci verify --request-secrets`, add `projects:write` only when it should update repository metadata, and add `secrets:write` only when it should manage project secrets. Keep `READYCI_ADMIN_TOKEN` out of agent environments; admin tokens are only for operator diagnostics such as `readyci doctor --admin-token TOKEN` or `readyci runs status --admin-token TOKEN RUN_ID`.

Agent JSON reports follow `schema/readyci.verification.v0alpha1.schema.json` and include `schema_version`, `intent`, and policy fields for the planned network mode, requested secrets, required token scopes, and sync preview command. Use `fixture-update` when generated fixtures/snapshots need artifact handoff, then run `readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'` and preview with `readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'` before adding `--confirm`. Use `security-sensitive` when verification should force no-network/no-secrets planning. Agent verify reports include `classification`, `retryable`, `agent_action`, and `summary`; API run summaries/details can include `failure_summary` and `primary_file`. `readyci agent explain --json RUN_ID` adds matched evidence lines and a compact `repair` packet so agents can distinguish code failures from auth, sync, worker, network, dependency, timeout, and cancellation failures without scraping terminal logs.

Useful command defaults:

- `READYCI_API_URL`: API origin. Released Ready CI CLI builds default to `https://api.readyci.com`; source/dev builds default to `http://127.0.0.1:8080`.
- `READYCI_API_TOKEN`: user-route bearer token.
- `READYCI_PROJECT_ID`: project id for runs and cache commands, default `proj_dev`.
- `READYCI_NETWORK_MODE`: `default`, `none`, or `allowlist`.
- `READYCI_NETWORK_ALLOWLIST`: comma-separated host/IP allowlist used with `READYCI_NETWORK_MODE=allowlist`.

## First Run

Prepare the local checkout and let Ready CI infer the first useful remote check:

```bash
readyci check --dry-run
readyci check --dry-run --why
readyci check --why --json
readyci plan --repo-map --json
readyci doctor --repo
readyci check
```

Use the guided start flow when you want Ready CI to show the detected repo signals before running:

```bash
readyci start
readyci start --write-config
```

`readyci check` works with or without `.readyci.yml`. Without config, it runs a conservative in-memory starter plan. Add `--why` to see why Ready CI selected or skipped checks; combine it with `--dry-run --json` to get the typed verification plan as `plan`, or use `--why --json` on a real run to get the plan, created run IDs, dashboard URLs, and final classification. `readyci start` previews that plan and can write the starter config with `--write-config`. `readyci setup --write-config --detect` and `readyci init --detect --print` now keep local generation conservative; use `readyci plan --recommend --suggest-config` for hosted smart config suggestions.

Use `readyci plan --repo-map --json` when you want the typed local repo facts behind the planner: Make wrappers, docs/AGENTS references, GitHub Actions jobs, Cargo package facts, Vite/React and OpenAPI signals, undefined check references, and unreferenced checks. Use `readyci plan --recommend --print-inventory --json` to inspect the redacted hosted-analyzer payload, then `readyci plan --recommend --json` when you want to explicitly send that inventory for versioned recommendation packs, or `readyci plan --recommend --suggest-config --json` when you want a hosted `.readyci.yml` suggestion. Use `readyci doctor --repo` as a local-only cross-file drift check before relying on Make/docs/GitHub Actions verification instructions.

Use `readyci affected --warm-dependencies` or `readyci touched --warm-dependencies` when a repo has a dedicated check named `dependencies`. Ready CI does not infer dependency work from command text, language files, Docker, cache paths, or Make wrappers. When warming is requested, it runs that explicit check before the selected persistent-cache checks; every warmed target must use the same cache-lane size and key namespace. Targets on different runner sizes receive separate warm nodes so each node can be claimed by a compatible worker, and every such node mounts the union of target cache paths so project-level lane reuse cannot select a partially warmed lane. A native dependency check targeting Docker work is placed on a worker that advertises Docker and the target image, while a warm or target matrix with `max_parallel > 1` fails fast because one warm node cannot provision multiple worker-local lanes. Warming requires an explicit `network.warm_mode`: use `allowlist` with `network.warm_allowlist` on public deployments, or `default` only on local/private deployments. Missing warm policy and inconsistent cache sizes fail before a run is submitted; Ready CI does not widen `network.mode` or fan out quota-specific warm nodes as a fallback. Add `--json` on an actual run to emit the plan, phase-tagged warm/verify run IDs, dashboard URLs, and final classification. `readyci verify --json --progress=status` applies the same dependency warming automatically for affected-rule verification, and an active registered policy reconstructs the warm command and egress profile server-side. `readyci verify --check dependencies` is a scoped warm operation; it uses the registered warm network policy, derives runner/cache targets from consumer checks selected by affected-file policy for the current workspace, and does not execute the dependency command again as a verify node. `security-sensitive` verification never introduces a networked warm node; warm the dedicated check separately before starting that no-network proof when its cache lane is cold.

Run a trusted command from a Git checkout:

```bash
readyci run "go test ./..."
```

Docker/Testcontainers runs default to no network unless explicitly overridden:

```bash
readyci run --docker "go test ./..."
readyci run --docker --network-mode default "go test ./..."
```

Inspect cache lanes for the configured project:

```bash
readyci cache ls
readyci cache purge
```

If the run config declares `artifacts.paths`, list and download the retained archive:

```bash
readyci artifacts ls RUN_ID
readyci artifacts download RUN_ID
```

## Building release artifacts

From a clean public checkout:

```bash
READYCI_RELEASE_VERSION="v0.1.0-local.1" scripts/build-release-artifacts.sh
```

The builder produces Linux/macOS amd64/arm64 tarballs and Windows amd64/arm64
ZIPs, plus checksums, a manifest, installers, and a Homebrew formula under
`dist/readyci-cli-$READYCI_RELEASE_VERSION`. Unsigned local builds are not
official releases. The source installer templates fail closed without an
embedded or explicitly supplied signing key.

Maintainers build official releases in a protected signing pipeline, verify the
signed bytes on Linux, macOS, and Windows, then publish them on GitHub Releases.
The public source snapshot is the CLI subtree only; private service code and
monorepo history are not published. See [CONTRIBUTING.md](https://github.com/DeanStr/readyci-cli/blob/main/CONTRIBUTING.md)
for the maintenance and reporting policy.

No Homebrew tap is published yet; use the verified installer above.
