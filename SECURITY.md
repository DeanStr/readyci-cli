# Security Policy

Ready CI runs user commands and moves source bundles, logs, cache data, and
artifacts. Treat CLI tokens as secrets and prefer the narrowest scopes that fit
the workflow.

## Reporting

Report suspected vulnerabilities privately to security@readyci.com. Include the
CLI version, operating system, affected command, and a minimal reproduction when
possible. Do not open public issues for active token, isolation, or data exposure
bugs until we have coordinated a fix.

## Supported Surface

During prerelease, supported security reports cover:

- `readyci` CLI authentication, workspace sync, logs, cache, artifacts, and
  project secret handling.
- Ready CI API token and web session flows.
- Worker isolation, Firecracker runtime setup, and artifact/upload storage.

The CLI and its shared libraries are licensed under Apache-2.0 and published
with a clean CLI-only history. Publication does not include the proprietary
service or imply an independent security audit of the CLI or hosted platform.
