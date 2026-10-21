package main

import (
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Separate named checks may deliberately use different inputs. Warn before a
// combined session is attempted without rejecting or rewriting those filters.
func sharedSourceScopeWarning(cfg config.ProjectConfig) string {
	var plans []affectedCheckPlan
	for _, name := range sortedCheckNames(cfg.Checks) {
		plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{})
		if err != nil {
			// Ordinary check validation owns failures unrelated to source scope.
			return ""
		}
		plans = append(plans, affectedCheckPlan{Plan: plan})
	}
	if _, err := compatibleAffectedSessionBuildOptions(plans, syncbundlebuild.BuildOptions{ChangeMode: "working"}); err != nil {
		return "Combined checks need a shared upload scope: " + err.Error()
	}
	return ""
}
