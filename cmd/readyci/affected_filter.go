package main

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func affectedTestPaths(test config.AffectedTest) []string {
	paths := append([]string(nil), test.Paths...)
	if strings.TrimSpace(test.Path) != "" {
		paths = append([]string{test.Path}, paths...)
	}
	return paths
}

func runAffectedAuditTest(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, test config.AffectedTest) (affectedAuditTestReport, bool, error) {
	paths := affectedTestPaths(test)
	expected := append([]string(nil), test.Checks...)
	actualSeen := make(map[string]bool)
	var actual []string
	for _, testPath := range paths {
		checks, err := selectChecksForAffectedPath(filterCache, cfg, testPath)
		if err != nil {
			return affectedAuditTestReport{}, false, err
		}
		for _, check := range checks {
			if actualSeen[check] {
				continue
			}
			actualSeen[check] = true
			actual = append(actual, check)
		}
	}
	report := affectedAuditTestReport{
		Paths:          paths,
		ExpectedChecks: expected,
		ActualChecks:   actual,
		Status:         "ok",
	}
	if !sameStringSet(expected, actual) {
		report.Status = "failed"
		report.Error = fmt.Sprintf("expected checks %s, got %s", strings.Join(expected, ", "), strings.Join(actual, ", "))
		return report, false, nil
	}
	return report, true, nil
}

func newAffectedPathFilterCache(ctx context.Context, cwd string, cfg config.ProjectConfig) *affectedPathFilterCache {
	return &affectedPathFilterCache{
		ctx:        ctx,
		cwd:        cwd,
		cfg:        cfg,
		changeMode: syncbundle.ChangeModeWorking,
		filters:    make(map[string]syncbundlebuild.PathFilter),
	}
}

func (cache *affectedPathFilterCache) includes(checkName string, file string) (bool, error) {
	check := cache.cfg.Checks[checkName]
	syncConfig := effectiveAffectedCheckSyncConfig(cache.cfg.Sync, check.Sync, cache.changeMode)
	key := affectedSyncConfigKey(syncConfig)
	filter, ok := cache.filters[key]
	if !ok {
		var err error
		filter, err = syncbundlebuild.NewPathFilter(cache.ctx, cache.cwd, syncbundlebuild.BuildOptions{
			ChangeMode:       cache.changeMode,
			ExcludePatterns:  append([]string(nil), syncConfig.Exclude...),
			IncludeUntracked: syncConfig.IncludeUntracked,
		})
		if err != nil {
			return false, err
		}
		cache.filters[key] = filter
	}
	return filter.Includes(file)
}

func selectChecksForAffectedPath(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, file string) ([]string, error) {
	selected, _, err := selectChecksForAffectedPathWithReasons(filterCache, cfg, file)
	return selected, err
}

func selectChecksForAffectedPathWithReasons(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, file string) ([]string, map[string][]affectedRuleMatch, error) {
	selected, reasons, _, err := selectChecksForAffectedPathWithSkipped(filterCache, cfg, file)
	return selected, reasons, err
}

func selectChecksForAffectedPathWithSkipped(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, file string) ([]string, map[string][]affectedRuleMatch, []affectedSkippedCheck, error) {
	changedByCheck := make(map[string][]string)
	for _, checkName := range affectedRoutingCheckNames(cfg) {
		included, err := filterCache.includes(checkName, file)
		if err != nil {
			return nil, nil, nil, err
		}
		if included {
			changedByCheck[checkName] = []string{file}
		}
	}
	return selectChecksForAffectedRulesWithSkipped(cfg, changedByCheck)
}

func explainAffectedPath(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, rawPath string, includeWhyNot bool) (affectedExplainPath, error) {
	normalized := normalizeAffectedPath(rawPath)
	checks, reasonsByCheck, skippedChecks, err := selectChecksForAffectedPathWithSkipped(filterCache, cfg, normalized)
	if err != nil {
		return affectedExplainPath{}, err
	}
	report := affectedExplainPath{
		Path:          normalized,
		Checks:        append([]string(nil), checks...),
		SkippedChecks: skippedChecks,
		Covered:       len(checks) > 0,
	}
	reasonSeen := make(map[affectedRuleMatch]bool)
	for _, check := range checks {
		for _, reason := range reasonsByCheck[check] {
			if reasonSeen[reason] {
				continue
			}
			reasonSeen[reason] = true
			report.Reasons = append(report.Reasons, reason)
		}
	}
	if includeWhyNot && len(report.Checks) == 0 {
		whyNot, ignored, ignoredBy, filterHits, err := explainAffectedWhyNot(filterCache, cfg, normalized)
		if err != nil {
			return affectedExplainPath{}, err
		}
		report.WhyNot = whyNot
		report.Ignored = ignored
		report.IgnoredBy = ignoredBy
		report.FilterHits = filterHits
	}
	return report, nil
}

func explainAffectedWhyNot(filterCache *affectedPathFilterCache, cfg config.ProjectConfig, file string) (string, bool, string, []string, error) {
	for _, pattern := range cfg.Affected.AuditIgnore {
		match, err := affectedPatternMatches(pattern, file)
		if err != nil {
			return "", false, "", nil, err
		}
		if match {
			return fmt.Sprintf("ignored by affected.audit_ignore pattern %s", pattern), true, pattern, nil, nil
		}
	}
	var matchedRulePatterns []string
	var filterHits []string
	for ruleIndex, rule := range cfg.Affected.Rules {
		for _, pattern := range rule.Paths {
			match, err := affectedPatternMatches(pattern, file)
			if err != nil {
				return "", false, "", nil, err
			}
			if !match {
				continue
			}
			matchedRulePatterns = append(matchedRulePatterns, fmt.Sprintf("rule %d pattern %s", ruleIndex+1, pattern))
			excluded, excludePattern, err := affectedRuleExcludes(rule, file)
			if err != nil {
				return "", false, "", nil, err
			}
			if excluded {
				filterHits = append(filterHits, fmt.Sprintf("rule %d matched path but exclude pattern %s removed it", ruleIndex+1, excludePattern))
				continue
			}
			for _, checkName := range rule.Checks {
				included, err := filterCache.includes(checkName, file)
				if err != nil {
					return "", false, "", nil, err
				}
				if !included {
					filterHits = append(filterHits, fmt.Sprintf("rule %d check %s matched path but sync filters exclude it", ruleIndex+1, checkName))
				}
			}
		}
	}
	if len(filterHits) > 0 {
		return "matched affected.rules path patterns, but no matching check remained after rule excludes or sync filters", false, "", filterHits, nil
	}
	if len(matchedRulePatterns) > 0 {
		return "matched affected.rules path patterns, but no configured checks were selectable", false, "", matchedRulePatterns, nil
	}
	return "no affected.rules path pattern matched", false, "", nil, nil
}

func affectedTestsIncludeExactPath(tests []config.AffectedTest, file string) bool {
	file = normalizeAffectedPath(file)
	for _, test := range tests {
		for _, testPath := range affectedTestPaths(test) {
			if normalizeAffectedPath(testPath) == file {
				return true
			}
		}
	}
	return false
}

func affectedRuleLintWarnings(cfg config.ProjectConfig) []string {
	var warnings []string
	webSpecific := false
	webGeneric := false
	for _, rule := range cfg.Affected.Rules {
		for _, rawPattern := range rule.Paths {
			pattern := normalizeAffectedPath(rawPattern)
			if strings.HasPrefix(pattern, "apps/web/src/") {
				webSpecific = true
				if strings.HasPrefix(pattern, "apps/web/src/**") {
					webGeneric = true
				}
			}
		}
	}
	if webSpecific && !webGeneric && len(cfg.Affected.UnmatchedChecks) == 0 {
		warnings = append(warnings, "affected.rules has apps/web/src-specific patterns but no generic apps/web/src/** fallback; newly-added frontend source directories may select no checks")
	}
	return warnings
}

func trackedAffectedAuditFiles(cwd string) ([]string, error) {
	ctx, stop := signalContext()
	defer stop()
	root, err := gitOutput(ctx, "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	out, err := gitOutputBytes(ctx, "-C", root, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	files := splitNULStrings(out)
	sort.Strings(files)
	return files, nil
}

func splitNULStrings(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	parts := strings.Split(string(out), "\x00")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			values = append(values, normalizeAffectedPath(part))
		}
	}
	return values
}

func sourceLikeAffectedPath(file string) bool {
	file = normalizeAffectedPath(file)
	if file == "" {
		return false
	}
	base := path.Base(file)
	switch base {
	case ".readyci.yml", ".readyci.yaml", ".readyciignore", "Makefile", "Dockerfile", "Cargo.toml", "Cargo.lock", "go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json", "tsconfig.json":
		return true
	}
	if strings.HasPrefix(file, ".github/workflows/") || strings.HasPrefix(file, ".cargo/") {
		return true
	}
	switch path.Ext(file) {
	case ".go", ".rs", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".json", ".yaml", ".yml", ".toml", ".sql", ".sh", ".css", ".scss", ".mdx":
		return true
	}
	if strings.HasSuffix(file, ".d.ts") {
		return true
	}
	return false
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := make(map[string]int, len(left))
	for _, value := range left {
		leftSet[value]++
	}
	for _, value := range right {
		leftSet[value]--
		if leftSet[value] < 0 {
			return false
		}
	}
	for _, count := range leftSet {
		if count != 0 {
			return false
		}
	}
	return true
}
