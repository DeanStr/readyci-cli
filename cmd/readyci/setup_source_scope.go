package main

import (
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Different component inputs need registered verification authority for a
// combined proof. Requested checks can use independently sealed source views.
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
		return "Different component upload scopes require an active trusted verification policy for combined readyci verify; readyci check executes the requested source views without a workspace-wide verification verdict."
	}
	return ""
}
