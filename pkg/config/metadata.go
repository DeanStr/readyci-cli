package config

import "github.com/DeanStr/readyci-cli/pkg/preflightapi"

type FieldScope string

const (
	ScopeProject       FieldScope = "project"
	ScopeSync          FieldScope = "sync"
	ScopeCache         FieldScope = "cache"
	ScopeArtifacts     FieldScope = "artifacts"
	ScopeAffected      FieldScope = "affected"
	ScopeAffectedRule  FieldScope = "affected.rule"
	ScopeAffectedTest  FieldScope = "affected.test"
	ScopeNetwork       FieldScope = "network"
	ScopeResources     FieldScope = "resources"
	ScopeResource      FieldScope = "resource"
	ScopeService       FieldScope = "service"
	ScopeServiceHealth FieldScope = "service.health"
	ScopeCheck         FieldScope = "check"
	ScopeStep          FieldScope = "step"
	ScopeMatrix        FieldScope = "matrix"
	ScopeWhen          FieldScope = "when"
	ScopeWhenChanges   FieldScope = "when.changes"
)

type ConfigField struct {
	Scope       FieldScope `json:"scope"`
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Description string     `json:"description"`
	Default     string     `json:"default,omitempty"`
}

func ConfigFieldMetadata() []ConfigField {
	fields := []ConfigField{
		field(ScopeProject, "version", "version", "Configuration file version.", "1"),
		field(ScopeProject, "project_id", "project_id", "Ready CI project identifier used for submitted runs.", "proj_dev"),
		field(ScopeProject, "region", "region", "Default worker region for runs.", "default"),
		field(ScopeProject, "image", "image", "Default worker image for checks.", preflightapi.DefaultImage),
		field(ScopeProject, "runner_size", "runner_size", "Default compute size for checks.", "standard"),
		field(ScopeProject, "timeout_seconds", "timeout_seconds", "Default run timeout in seconds.", ""),
		field(ScopeProject, "timeout", "timeout", "Default run timeout as a duration string.", ""),
		field(ScopeProject, "retries", "retries", "Default retry count for checks.", "0"),
		field(ScopeProject, "concurrency_group", "concurrency_group", "Default project-wide concurrency group.", ""),
		field(ScopeProject, "concurrency_limit", "concurrency_limit", "Default project-wide concurrency limit.", ""),
		field(ScopeProject, "cache_mode", "cache_mode", "Legacy default cache mode alias.", "cache-only"),
		field(ScopeProject, "sync", "sync", "Project-wide source bundle sync settings.", ""),
		field(ScopeProject, "cache", "cache", "Project-wide cache settings.", ""),
		field(ScopeProject, "artifacts", "artifacts", "Project-wide artifact collection settings.", ""),
		field(ScopeProject, "affected", "affected", "Changed-file routing rules and audit fixtures.", ""),
		field(ScopeProject, "network", "network", "Project-wide network policy.", ""),
		field(ScopeProject, "resources", "resources", "Legacy resource defaults and per-check overrides.", ""),
		field(ScopeProject, "services", "services", "Named service definitions available to checks.", ""),
		field(ScopeProject, "checks", "checks", "Named Ready CI checks.", ""),

		field(ScopeSync, "include_untracked", "sync.include_untracked", "Include untracked workspace files in source bundles.", "true"),
		field(ScopeSync, "respect_gitignore", "sync.respect_gitignore", "Respect .gitignore when building source bundles.", "true"),
		field(ScopeSync, "exclude", "sync.exclude", "Additional source bundle exclude globs.", ""),

		field(ScopeCache, "mode", "cache.mode", "Cache mode for this scope.", "cache-only"),
		field(ScopeCache, "size", "cache.size", "Requested persistent cache lane size, such as 10GiB or 30GB.", ""),
		field(ScopeCache, "paths", "cache.paths", "Paths retained in persistent cache lanes.", ""),
		field(ScopeCache, "key_files", "cache.key_files", "Files that contribute to cache keys.", ""),

		field(ScopeArtifacts, "paths", "artifacts.paths", "Artifact path globs retained after a run.", ""),

		field(ScopeAffected, "profiles", "affected.profiles", "Built-in affected-rule profiles to expand.", ""),
		field(ScopeAffected, "rules", "affected.rules", "Explicit changed-file rules selecting checks.", ""),
		field(ScopeAffected, "unmatched_checks", "affected.unmatched_checks", "Checks selected only when a changed path matches no affected rule.", ""),
		field(ScopeAffected, "subsumes", "affected.subsumes", "Aggregate checks that prune redundant child checks.", ""),
		field(ScopeAffected, "tests", "affected.tests", "Audit examples for affected-rule behavior.", ""),
		field(ScopeAffected, "audit_ignore", "affected.audit_ignore", "Tracked paths intentionally ignored by strict affected audit.", ""),
		field(ScopeAffectedRule, "paths", "affected.rules[].paths", "Path globs that select this rule.", ""),
		field(ScopeAffectedRule, "paths_from", "affected.rules[].paths_from", "Manifest file entries expanded into path globs for this rule.", ""),
		field(ScopeAffectedRule, "exclude", "affected.rules[].exclude", "Negative path globs for this rule.", ""),
		field(ScopeAffectedRule, "checks", "affected.rules[].checks", "Checks selected by this rule.", ""),
		field(ScopeAffectedTest, "path", "affected.tests[].path", "Single example path for affected audit.", ""),
		field(ScopeAffectedTest, "paths", "affected.tests[].paths", "Example path set for affected audit.", ""),
		field(ScopeAffectedTest, "checks", "affected.tests[].checks", "Expected selected checks for this audit example.", ""),

		field(ScopeNetwork, "mode", "network.mode", "Network mode for submitted checks.", preflightapi.DefaultNetworkMode),
		field(ScopeNetwork, "allowlist", "network.allowlist", "Allowed destinations when network mode is allowlist.", ""),
		field(ScopeNetwork, "warm_mode", "network.warm_mode", "Explicit outbound network mode used only for dependency warm runs.", ""),
		field(ScopeNetwork, "warm_allowlist", "network.warm_allowlist", "Allowed destinations for dependency warm runs when warm_mode is allowlist.", ""),

		field(ScopeResources, "default", "resources.default", "Legacy default resource override.", ""),
		field(ScopeResources, "checks", "resources.checks", "Legacy per-check resource overrides.", ""),
		field(ScopeResource, "size", "resources.*.size", "Legacy runner size alias.", ""),
		field(ScopeResource, "timeout", "resources.*.timeout", "Legacy timeout override.", ""),
		field(ScopeResource, "docker", "resources.*.docker", "Legacy Docker enablement override.", ""),

		field(ScopeService, "type", "services.<name>.type", "Service type.", ""),
		field(ScopeService, "version", "services.<name>.version", "Service version.", ""),
		field(ScopeService, "image", "services.<name>.image", "Container image for a generic service. The worker must already have this image preloaded; Ready CI does not pull service images during runs.", ""),
		field(ScopeService, "ports", "services.<name>.ports", "Container ports exposed to the check.", ""),
		field(ScopeService, "env", "services.<name>.env", "Literal service environment variables.", ""),
		field(ScopeService, "health", "services.<name>.health", "Service readiness probe.", ""),
		field(ScopeService, "logs", "services.<name>.logs", "Service log paths to retain.", ""),
		field(ScopeServiceHealth, "command", "services.<name>.health.command", "Health-check command.", ""),
		field(ScopeServiceHealth, "timeout", "services.<name>.health.timeout", "Health-check timeout.", ""),
		field(ScopeServiceHealth, "interval", "services.<name>.health.interval", "Health-check polling interval.", ""),

		field(ScopeCheck, "command", "checks.<name>.command", "Shell command for a check.", ""),
		field(ScopeCheck, "steps", "checks.<name>.steps", "Ordered step definitions for a check.", ""),
		field(ScopeCheck, "prepare", "checks.<name>.prepare", "Commands run before the main check command.", ""),
		field(ScopeCheck, "post", "checks.<name>.post", "Commands run after the main check command.", ""),
		field(ScopeCheck, "always", "checks.<name>.always", "Commands run even when earlier phases fail.", ""),
		field(ScopeCheck, "changes", "checks.<name>.changes", "Changed-file source for this check.", "working"),
		field(ScopeCheck, "region", "checks.<name>.region", "Worker region override for this check.", ""),
		field(ScopeCheck, "image", "checks.<name>.image", "Worker image override for this check.", ""),
		field(ScopeCheck, "runner_size", "checks.<name>.runner_size", "Compute size override for this check.", ""),
		field(ScopeCheck, "size", "checks.<name>.size", "Legacy runner size alias for this check.", ""),
		field(ScopeCheck, "timeout_seconds", "checks.<name>.timeout_seconds", "Check timeout in seconds.", ""),
		field(ScopeCheck, "timeout", "checks.<name>.timeout", "Check timeout as a duration string.", ""),
		field(ScopeCheck, "retries", "checks.<name>.retries", "Retry count for this check.", ""),
		field(ScopeCheck, "concurrency_group", "checks.<name>.concurrency_group", "Concurrency group for this check.", ""),
		field(ScopeCheck, "concurrency_limit", "checks.<name>.concurrency_limit", "Concurrency limit for this check.", ""),
		field(ScopeCheck, "matrix", "checks.<name>.matrix", "Matrix variables submitted as separate runs.", ""),
		field(ScopeCheck, "when", "checks.<name>.when", "Conditional check selection rules.", ""),
		field(ScopeCheck, "sync", "checks.<name>.sync", "Check-level source sync override.", ""),
		field(ScopeCheck, "cache_mode", "checks.<name>.cache_mode", "Legacy check-level cache mode alias.", ""),
		field(ScopeCheck, "cache", "checks.<name>.cache", "Check-level cache settings.", ""),
		field(ScopeCheck, "network_mode", "checks.<name>.network_mode", "Check-level network mode.", ""),
		field(ScopeCheck, "network_allowlist", "checks.<name>.network_allowlist", "Check-level network allowlist.", ""),
		field(ScopeCheck, "docker", "checks.<name>.docker", "Enable Docker for this check.", ""),
		field(ScopeCheck, "browser", "checks.<name>.browser", "Install browser runtime dependencies before this check.", ""),
		field(ScopeCheck, "services", "checks.<name>.services", "Named services attached to this check.", ""),
		field(ScopeCheck, "worker_selector", "checks.<name>.worker_selector", "Worker labels required by this check.", ""),
		field(ScopeCheck, "keep_failed", "checks.<name>.keep_failed", "Retain failed workspaces/artifacts for inspection.", "false"),
		field(ScopeCheck, "artifact_paths", "checks.<name>.artifact_paths", "Check-level artifact path globs.", ""),
		field(ScopeCheck, "outputs", "checks.<name>.outputs", "Check outputs that Ready CI should preserve for review.", ""),

		field(ScopeStep, "name", "checks.<name>.steps[].name", "Step name.", ""),
		field(ScopeStep, "command", "checks.<name>.steps[].command", "Step command.", ""),
		field(ScopeStep, "always", "checks.<name>.steps[].always", "Run this step even after earlier failures.", "false"),
		field(ScopeStep, "env", "checks.<name>.steps[].env", "Step environment variables.", ""),
		field(ScopeStep, "timeout", "checks.<name>.steps[].timeout", "Step timeout.", ""),

		field(ScopeMatrix, "axes", "checks.<name>.matrix.axes", "Matrix axes submitted as separate runs.", ""),
		field(ScopeMatrix, "include", "checks.<name>.matrix.include", "Additional matrix variants.", ""),
		field(ScopeMatrix, "exclude", "checks.<name>.matrix.exclude", "Matrix variants to suppress.", ""),
		field(ScopeMatrix, "fail_fast", "checks.<name>.matrix.fail_fast", "Stop scheduling later matrix variants after the first failure.", "false"),
		field(ScopeMatrix, "max_parallel", "checks.<name>.matrix.max_parallel", "Maximum matrix variants to run concurrently.", ""),

		field(ScopeWhen, "event", "checks.<name>.when.event", "Events that can select this check.", ""),
		field(ScopeWhen, "branch", "checks.<name>.when.branch", "Branches that can select this check.", ""),
		field(ScopeWhen, "tag", "checks.<name>.when.tag", "Tag glob that can select this check.", ""),
		field(ScopeWhen, "env", "checks.<name>.when.env", "Environment values required to select this check.", ""),
		field(ScopeWhen, "status", "checks.<name>.when.status", "Required previous status for this check.", ""),
		field(ScopeWhen, "changes", "checks.<name>.when.changes", "Changed-file condition.", ""),
		field(ScopeWhenChanges, "include", "checks.<name>.when.changes.include", "Path globs that enable this check.", ""),
		field(ScopeWhenChanges, "exclude", "checks.<name>.when.changes.exclude", "Path globs that suppress this check.", ""),
	}
	return append([]ConfigField(nil), fields...)
}

func field(scope FieldScope, name, path, description, defaultValue string) ConfigField {
	return ConfigField{Scope: scope, Name: name, Path: path, Description: description, Default: defaultValue}
}
