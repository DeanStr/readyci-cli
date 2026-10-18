package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

func writeJSONOutput(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultAdminToken() string {
	return envDefault("READYCI_ADMIN_TOKEN", envDefault("READYCI_API_TOKEN", ""))
}

type secretStringValue struct {
	value string
}

func (v *secretStringValue) Set(value string) error {
	v.value = value
	return nil
}

func (v *secretStringValue) String() string {
	return ""
}

func secretStringFlag(fs *flag.FlagSet, name, value, usage string) *string {
	holder := &secretStringValue{value: value}
	fs.Var(holder, name, usage)
	return &holder.value
}

func apiTokenFlag(fs *flag.FlagSet, usage string) *string {
	return secretStringFlag(fs, "token", envDefault("READYCI_API_TOKEN", ""), usage)
}

func apiFlags(fs *flag.FlagSet) (apiURL, token *string) {
	return fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL"),
		apiTokenFlag(fs, "bearer token for API requests")
}

func adminTokenFlag(fs *flag.FlagSet, usage string) *string {
	return secretStringFlag(fs, "token", defaultAdminToken(), usage)
}

func adminAPIFlags(fs *flag.FlagSet) (apiURL, token *string) {
	return fs.String("api-url", envDefault("READYCI_API_URL", defaultAPIURL()), "Ready CI API URL"),
		adminTokenFlag(fs, "bearer token for admin API requests")
}

func adminOnlyTokenFlag(fs *flag.FlagSet, usage string) *string {
	return secretStringFlag(fs, "admin-token", envDefault("READYCI_ADMIN_TOKEN", ""), usage)
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func boolPtr(value bool) *bool {
	return &value
}

func commandFromArgs(args []string) string {
	if len(args) == 1 {
		return args[0]
	}

	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func validateExitCode(code int) (int, error) {
	if code < 0 || code > 255 {
		return 1, fmt.Errorf("invalid remote exit code %d", code)
	}
	return code, nil
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			provided = true
		}
	})
	return provided
}

func newIdempotencyKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func normalizeIdempotencyKey(value string) (string, error) {
	if err := validateControlFreeValue("idempotency_key", value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func timeOrDash(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func oneLine(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Run tests remotely against your current workspace, including uncommitted changes.

usage:
  readyci login                           Sign in once
  readyci setup                           Set up and run your first test
  readyci setup --print                    Inspect generated config locally
  readyci check [--json] [NAME]            Run configured or affected checks
  readyci run [--detach] [--json] COMMAND  Run an ad hoc command
  readyci affected --dry-run               Preview changed-file selection
  readyci sessions get|watch|cancel ID     Inspect or stop a job
  readyci sessions retry|rerun ID          Retry saved source or test current edits
  readyci runs logs RUN_ID                 Read test output
  readyci doctor                          Diagnose setup
  readyci verify                          Advanced agent assurance
  readyci version                         Print the installed version

Use readyci COMMAND --help for flags, or readyci help --all for every command.`)
}

func printFullUsage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  readyci setup [--yes] [--preview] [--run] [--print] [--image IMAGE] [--api-url URL] [--project PROJECT] [--check NAME] [--profile remote-ci|strict] [--write-config] [--write-makefile] [--write-helpers] [--makefile] [--helper NAME] [--write-helper] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci login [--api-url URL] [--sso SLUG] [--no-browser] [--timeout DURATION]")
	fmt.Fprintln(os.Stderr, "  readyci auth status [--api-url URL]")
	fmt.Fprintln(os.Stderr, "  readyci auth logout [--api-url URL]")
	fmt.Fprintln(os.Stderr, "  readyci config validate")
	fmt.Fprintln(os.Stderr, "  readyci config explain [--json] [--fields]")
	fmt.Fprintln(os.Stderr, "  readyci plan --repo-map [--json]")
	fmt.Fprintln(os.Stderr, "  readyci plan --recommend [--print-inventory] [--suggest-config] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci plan --recommend [--print-inventory] [--api-url URL] [--token TOKEN] [--project PROJECT] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci import make|github-actions [--json]")
	fmt.Fprintln(os.Stderr, "  readyci setup --import make|github-actions [--candidate ID] [--preview|--yes]")
	fmt.Fprintln(os.Stderr, "  readyci sync --dry-run [--check NAME] [--changes MODE] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci doctor [--repo] [--api-url URL] [--token TOKEN] [--admin-token TOKEN] [--check NAME] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci doctor coverage [--json]")
	fmt.Fprintln(os.Stderr, "  readyci usage [--api-url URL] [--token TOKEN] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci feedback [--api-url URL] [--token TOKEN] [--project PROJECT] [--run RUN_ID|latest] [--category CATEGORY] [--message TEXT] [--include-logs] [--json] [message]")
	fmt.Fprintln(os.Stderr, "  readyci project get|set [--project PROJECT] [--trusted-secret-command COMMAND] [--trusted-secret-command-hash HASH]")
	fmt.Fprintln(os.Stderr, "  readyci project policy get|preview [--project PROJECT] [--file .readyci.yml] (activation is browser-only)")
	fmt.Fprintln(os.Stderr, "  readyci secrets list|set|delete [--project PROJECT]")
	fmt.Fprintln(os.Stderr, "  readyci run [--json] [--request-secrets] [--api-url URL] [--token TOKEN] [--retry N] [--detach] [--open] [--changes MODE] [--runner-size SIZE] [--timeout DURATION] [--cache-path PATH] [--keep-failed] [--concurrency-group GROUP] [--concurrency-limit N] [--network-mode MODE] [--network-allowlist HOSTS] [--docker] <command>")
	fmt.Fprintln(os.Stderr, "  readyci affected [--skip-warm] [--request-secrets] [--api-url URL] [--token TOKEN] [--check NAME] [--staged] [--timeout DURATION] [--dry-run] [--json] [--watch] [--open]")
	fmt.Fprintln(os.Stderr, "  readyci affected audit [--json] [--strict] [--examples] [--duplicates] [--fix] [--fix-ignore-unmatched]")
	fmt.Fprintln(os.Stderr, "  readyci affected explain [--json] [--why-not] <path> [path...]")
	fmt.Fprintln(os.Stderr, "  readyci affected cargo [--check-prefix PREFIX] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci affected suggest [--json]")
	fmt.Fprintln(os.Stderr, "  readyci warm --check NAME|--profile dependencies|docker|all [--skip-confirm] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  readyci verify [--json] [--detach] [--progress auto|status|quiet] [--check NAME] [--intent INTENT] [--goal TEXT] [--task-id ID] [--idempotency-key KEY] [--predecessor SESSION_ID] [--request-secrets]")
	fmt.Fprintln(os.Stderr, "  readyci sessions get|watch|cancel [--json] SESSION_ID")
	fmt.Fprintln(os.Stderr, "  readyci sessions retry [--detach] [--json] SESSION_ID")
	fmt.Fprintln(os.Stderr, "  readyci sessions rerun [--detach] [--json] [--idempotency-key KEY] SESSION_ID")
	fmt.Fprintln(os.Stderr, "  readyci agent plan [--json] [--check NAME] [--intent INTENT] [--goal TEXT] [--request-secrets]")
	fmt.Fprintln(os.Stderr, "  readyci agent explain [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci agent artifacts plan|apply [--json] RUN_ID --paths PATTERN [--confirm]")
	fmt.Fprintln(os.Stderr, "  readyci agent setup")
	fmt.Fprintln(os.Stderr, "  readyci agent instructions|skill print|install|doctor")
	fmt.Fprintln(os.Stderr, "  readyci agent token create [--preset PRESET] [--ttl 30d] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci check [--skip-warm] [--dry-run] [--why] [--request-secrets] [name]")
	fmt.Fprintln(os.Stderr, "  readyci check --list [--json]")
	fmt.Fprintln(os.Stderr, "  readyci check [--skip-warm] [--api-url URL] [--token TOKEN] [--dry-run] [--why] [--json] [--retry N] [--watch] [--open] [--changes MODE] [--timeout DURATION] [--cache-path PATH] [--keep-failed] [--concurrency-group GROUP] [--concurrency-limit N] <name>")
	fmt.Fprintln(os.Stderr, "  readyci groups latest [--api-url URL] [--token TOKEN] [--project PROJECT] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci groups status [--api-url URL] [--token TOKEN] [--json] GROUP_ID")
	fmt.Fprintln(os.Stderr, "  readyci groups logs [--api-url URL] [--token TOKEN] [--tail N] GROUP_ID")
	fmt.Fprintln(os.Stderr, "  readyci groups cancel [--api-url URL] [--token TOKEN] [--reason TEXT] [--json] GROUP_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs latest [--api-url URL] [--token TOKEN] [--project PROJECT] [--status STATUS] [--by-idempotency-key KEY] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci runs status [--api-url URL] [--token TOKEN] [--admin-token TOKEN] [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs explain [--api-url URL] [--token TOKEN] [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs logs [--api-url URL] [--token TOKEN] [--json|--jsonl] [--summary] [--tail N] [--errors-only] [--include-lifecycle] [--phase PHASE] [--step STEP] [--service SERVICE] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs artifacts [--api-url URL] [--token TOKEN] [--download] [--output FILE] [--force] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs tests [--api-url URL] [--token TOKEN] [--status passed|failed|error|skipped] [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci runs cancel [--api-url URL] [--token TOKEN] [--reason TEXT] [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci artifacts ls [--api-url URL] [--token TOKEN] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci artifacts download [--api-url URL] [--token TOKEN] [--output FILE] [--force] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci artifacts apply RUN_ID --paths PATTERN [--paths PATTERN] [--dry-run] [--json] [--allow-empty]")
	fmt.Fprintln(os.Stderr, "  readyci fixtures update --paths PATTERN [--from RUN_ID|latest] [--dry-run] [--json] [--allow-empty]")
	fmt.Fprintln(os.Stderr, "  readyci cache ls [--api-url URL] [--token TOKEN] [project]")
	fmt.Fprintln(os.Stderr, "  readyci cache purge [--api-url URL] [--token TOKEN] [project]")
	fmt.Fprintln(os.Stderr, "  readyci admin runs [--api-url URL] [--token TOKEN] [--project PROJECT] [--status STATUS] [--limit N]")
	fmt.Fprintln(os.Stderr, "  readyci admin inspect [--api-url URL] [--token TOKEN] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin events [--api-url URL] [--token TOKEN] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin logs [--api-url URL] [--token TOKEN] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin cancel [--api-url URL] [--token TOKEN] --reason TEXT RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin tokens list [--api-url URL] [--token TOKEN]")
	fmt.Fprintln(os.Stderr, "  readyci admin tokens create [--api-url URL] [--token TOKEN] [--name NAME] [--team TEAM] [--scopes LIST] [--email EMAIL]")
	fmt.Fprintln(os.Stderr, "  readyci admin tokens revoke [--api-url URL] [--token TOKEN] TOKEN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin teams list|disable|cancel-runs [--api-url URL] [--token TOKEN]")
	fmt.Fprintln(os.Stderr, "  readyci admin workers [list|drain|undrain|delete] [--api-url URL] [--token TOKEN]")
	fmt.Fprintln(os.Stderr, "  readyci admin queue status [--api-url URL] [--token TOKEN] [--json]")
	fmt.Fprintln(os.Stderr, "  readyci admin queue explain [--api-url URL] [--token TOKEN] [--json] RUN_ID")
	fmt.Fprintln(os.Stderr, "  readyci admin storage audit [--api-url URL] [--token TOKEN]")
	fmt.Fprintln(os.Stderr, "  readyci admin storage cleanup-expired-artifacts [--api-url URL] [--token TOKEN] [--dry-run] [--limit N] [--min-age-days N] [--include-malformed-artifacts] [--include-stale-uploads] [--include-run-logs] [--log-min-age-days N]")
	fmt.Fprintln(os.Stderr, "  readyci admin storage cleanup-cache-lanes [--api-url URL] [--token TOKEN] [--dry-run] [--limit N] [--min-age-days N] [--non-home-min-age-days N] [--keep-lanes-per-project-region N]")
	fmt.Fprintln(os.Stderr, "  readyci admin storage cleanup-status [--api-url URL] [--token TOKEN]")
	fmt.Fprintln(os.Stderr, "  readyci admin storage cleanup-cancel [--api-url URL] [--token TOKEN] --reason TEXT")
	fmt.Fprintln(os.Stderr, "  readyci admin privacy create|get|review|plan|export|complete|erase|provider-complete|restore-complete [options]")
	fmt.Fprintln(os.Stderr, "  readyci version")
}

type exitCodeError struct {
	code int
}

const (
	cliExitSuccess              = 0
	cliExitRemoteFailure        = 1
	cliExitUsageOrConfig        = 2
	cliExitAuthOrPolicy         = 3
	cliExitVerificationUnknown  = 4
	cliExitInternal             = 5
	cliExitNonAuthoritative     = 6
	cliExitTemporaryUnavailable = 7
)

func (e exitCodeError) Error() string {
	return fmt.Sprintf("remote command exited with code %d", e.code)
}

type terminalRunError struct {
	eventType string
	exitCode  int
}

func (e terminalRunError) Error() string {
	return exitCodeError{code: e.exitCode}.Error()
}

func (e terminalRunError) Unwrap() error {
	return exitCodeError{code: e.exitCode}
}
