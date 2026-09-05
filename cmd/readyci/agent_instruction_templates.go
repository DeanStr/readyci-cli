package main

import (
	"strings"
)

func readyCIAgentContractSection(heading string) string {
	return `
` + heading + `

1. Plan the smallest relevant verification with ` + "`readyci agent plan --json`" + `.
2. Prefer affected checks during edit loops.
3. Keep ` + "`--task-id`" + ` stable for the task. Use a fresh ` + "`--idempotency-key`" + ` after every edit, preserve that key only for retries of the same workspace, and pass the prior ` + "`session_id`" + ` as ` + "`--predecessor`" + `.
4. Never request secrets unless the task requires them.
5. Use ` + "`readyci agent explain --json RUN_ID`" + ` when verification fails.
6. Preview generated artifacts before applying them.
7. Report check name, run_id, status, classification, and exit code before claiming completion.`
}

func readyCISlashCommandsSection(heading string) string {
	return `
` + heading + `

If the agent host supports slash commands, use these names as command
templates. If it does not, run the shell commands directly. Keep ` + "`TASK_ID`" + `
stable, set ` + "`SESSION_KEY`" + ` to a fresh value after each edit, and set
` + "`PREVIOUS_SESSION_ID`" + ` to the last session ID (or empty for the first session).

- ` + "`/readyci-plan`" + `: ` + "`readyci agent plan --json --intent edit-loop`" + `
- ` + "`/readyci-verify`" + `: ` + "`readyci verify --json --progress=status --intent edit-loop --task-id \"$TASK_ID\" --idempotency-key \"$SESSION_KEY\" --predecessor \"$PREVIOUS_SESSION_ID\"`" + `
- ` + "`/readyci-final`" + `: ` + "`readyci verify --json --progress=status --intent final-handoff --task-id \"$TASK_ID\" --idempotency-key \"$SESSION_KEY\" --predecessor \"$PREVIOUS_SESSION_ID\"`" + `
- ` + "`/readyci-explain RUN_ID`" + `: ` + "`readyci agent explain --json RUN_ID`" + `
- ` + "`/readyci-fixtures RUN_ID PATHS`" + `: run ` + "`readyci agent artifacts plan --json RUN_ID --paths PATHS`" + `, preview with ` + "`readyci agent artifacts apply --json RUN_ID --paths PATHS`" + `, then add ` + "`--confirm`" + ` only after paths are expected.`
}

func readyCISlashCommandsSnippet() string {
	return strings.TrimLeft(readyCISlashCommandsSection("## Ready CI Slash Commands"), "\n")
}

func readyCIAgentsSnippet() string {
	return agentInstructionsStart + `
## Verification

Use Ready CI as the verification protocol for coding agents. It plans the right
check, runs it remotely, classifies failures, and returns structured repair and
artifact handoff data without log scraping.
` + readyCIAgentContractSection("Contract:") + `
` + readyCISlashCommandsSection("Shortcut commands:") + `

During the edit loop, run:

` + "```bash" + `
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

Before final handoff, run the configured broad check when practical:

` + "```bash" + `
readyci verify --json --progress=status --intent final-handoff --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

When ` + "`fixture-update`" + ` produces generated fixtures or snapshots, use agent artifact handoff:

` + "```bash" + `
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
` + "```" + `

If ` + "`readyci verify`" + ` is not available, use:

` + "```bash" + `
readyci check --list --json
# Pick CHECK from the configured list. Prefer affected or touched if present;
# otherwise choose the narrowest relevant configured check.
readyci check --dry-run --json CHECK
readyci check CHECK
` + "```" + `

Do not use browser login. Use ` + "`READYCI_API_URL`" + `, ` + "`READYCI_PROJECT_ID`" + `, and ` + "`READYCI_API_TOKEN`" + `.
Do not request admin tokens. Do not request secret-bearing tokens unless the task explicitly requires project secrets, and pass ` + "`--request-secrets`" + ` only for those runs.

When a run fails, inspect:

` + "```bash" + `
readyci agent explain --json RUN_ID
readyci runs logs --tail 200 --include-lifecycle --json RUN_ID
` + "```" + `

Use the ` + "`repair`" + ` object from ` + "`readyci agent explain --json`" + ` as the next edit packet.
Report the check name, run_id, status, classification, and exit code before saying the work is complete.
` + agentInstructionsEnd + `
`
}

func readyCICodexSnippet() string {
	return agentInstructionsStart + `
## Ready CI Verification

Use Ready CI as the verification protocol for uncommitted work. Prefer JSON,
keep the task ID stable, rotate the session key after edits, and treat
` + "`readyci.verification.v0alpha1`" + ` reports as the source of truth.
` + readyCIAgentContractSection("Contract:") + `
` + readyCISlashCommandsSection("Shortcut commands:") + `

For normal Codex edit loops:

` + "```bash" + `
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

For staged/pre-commit work:

` + "```bash" + `
readyci verify --json --progress=status --intent pre-commit --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

For generated fixtures or security-sensitive edits:

` + "```bash" + `
readyci verify --json --progress=status --intent fixture-update --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
readyci verify --json --progress=status --intent security-sensitive --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

For generated fixture or snapshot artifacts, preview before writing:

` + "```bash" + `
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
` + "```" + `

For final handoff:

` + "```bash" + `
readyci verify --json --progress=status --intent final-handoff --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

Do not use browser login. Use ` + "`READYCI_API_URL`" + `, ` + "`READYCI_PROJECT_ID`" + `, and a scoped ` + "`READYCI_API_TOKEN`" + `.
Do not request admin tokens. Do not request secret-bearing tokens unless the task explicitly requires project secrets.

When verification fails, inspect the structured repair packet:

` + "```bash" + `
readyci agent explain --json RUN_ID
` + "```" + `

Use ` + "`repair.agent_action`" + `, ` + "`repair.primary_file`" + `, ` + "`repair.evidence`" + `, and ` + "`repair.rerun_command`" + ` for the next edit. Report check name, run_id, status, classification, and exit code before saying the work is complete.
` + agentInstructionsEnd + `
`
}

func readyCIGenericShellSnippet() string {
	return agentInstructionsStart + `
## Ready CI Shell Contract

Automation can use Ready CI through the shell without scraping human output.
Always request JSON and treat ` + "`readyci.verification.v0alpha1`" + ` reports as the source of truth.
` + readyCIAgentContractSection("Contract:") + `
` + readyCISlashCommandsSection("Shortcut commands:") + `

` + "```bash" + `
: "${TASK_ID:=readyci-$(date +%s)}"
: "${SESSION_KEY:=$TASK_ID:session-1}"
: "${PREVIOUS_SESSION_ID:=}"
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

For fixture artifact handoff, preview first and confirm only after the paths are expected:

` + "```bash" + `
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
` + "```" + `

If verification fails, run:

` + "```bash" + `
readyci agent explain --json RUN_ID
` + "```" + `

Consume ` + "`schema_version`" + `, ` + "`classification`" + `, ` + "`agent_action`" + `, and ` + "`repair`" + ` from JSON. Report check name, run_id, status, classification, and exit code.

Use scoped ` + "`READYCI_API_TOKEN`" + ` values only. Never store token values in repository files.
` + agentInstructionsEnd + `
`
}

func readyCIAgentSkill() string {
	return `---
name: readyci-verify
description: Verify code changes with Ready CI remote checks. Use after editing code, before opening a PR, before claiming completion, when local tests are too slow, or when fixture artifacts need preview/apply.
---

# Ready CI verification

Use Ready CI as the verification protocol for the current uncommitted working
tree. Ready CI plans the right check, runs it remotely, classifies failures, and
returns structured repair and artifact handoff data without log scraping.
` + readyCIAgentContractSection("## Contract") + `
` + readyCISlashCommandsSection("## Shortcut commands") + `

## Preconditions

The environment should provide:

- ` + "`READYCI_API_URL`" + `
- ` + "`READYCI_PROJECT_ID`" + `
- ` + "`READYCI_API_TOKEN`" + `

Do not use browser login. Do not ask for admin tokens.

## Run verification

Prefer:

` + "```bash" + `
readyci agent plan --json --intent edit-loop
readyci verify --json --progress=status --intent edit-loop --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

Before final handoff, run:

` + "```bash" + `
readyci verify --json --progress=status --intent final-handoff --task-id "$TASK_ID" --idempotency-key "$SESSION_KEY" --predecessor "$PREVIOUS_SESSION_ID"
` + "```" + `

For generated fixture or snapshot artifacts, preview before writing:

` + "```bash" + `
readyci agent artifacts plan --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --paths 'tests/fixtures/**'
readyci agent artifacts apply --json RUN_ID --confirm --paths 'tests/fixtures/**'
` + "```" + `

If that command is unavailable, choose and run a configured check:

` + "```bash" + `
readyci check --list --json
# Pick CHECK from the configured list. Prefer affected or touched if present;
# otherwise choose the narrowest relevant configured check.
readyci check --dry-run --json CHECK
readyci check CHECK
` + "```" + `

## Failure handling

- Test, lint, typecheck, or build failure: edit code and rerun the same check.
- Auth, config, or project error: report the missing setup; do not guess credentials.
- Worker, capability, or queue error: run ` + "`readyci doctor --json`" + ` and summarize the blockage.
- Network or dependency failure: retry once only if logs suggest transient infrastructure.
- Secret-required failure: ask for a scoped Ready CI token with explicit secret access.

When a run fails, inspect:

` + "```bash" + `
readyci agent explain --json RUN_ID
readyci runs logs --tail 200 --include-lifecycle --json RUN_ID
` + "```" + `

Use the ` + "`repair`" + ` object from ` + "`readyci agent explain --json`" + ` as the next edit packet.
Before claiming completion, include the check name, run_id, status, classification, and exit code.
`
}
