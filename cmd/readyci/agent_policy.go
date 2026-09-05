package main

import (
	"strings"
)

var agentVerifyOnlyScopes = []string{"runs:create", "runs:read", "cache:read", "projects:read", "account:read"}

func agentPolicyForPlans(changes string, plans []checkRunPlan) *agentPolicyReport {
	return agentPolicyForPlansForIntent(changes, "", false, plans)
}

func agentPolicyForPlansForIntent(changes, intent string, requestSecrets bool, plans []checkRunPlan) *agentPolicyReport {
	policy := &agentPolicyReport{
		NetworkMode:          "none",
		NetworkAllowlist:     []string{},
		SecretsRequested:     requestSecrets && intent != agentIntentSecurity,
		SecretsAuthorized:    false,
		TokenScopesRequired:  append([]string(nil), agentVerifyOnlyScopes...),
		TokenScopesMissing:   []string{},
		UploadPreviewCommand: agentSyncPreviewCommand(changes, commonPlanCheckName(plans)),
	}
	if policy.SecretsRequested {
		policy.TokenScopesRequired = append(policy.TokenScopesRequired, "runs:create_with_secrets")
		policy.PolicyReasons = append(policy.PolicyReasons, "project-secret release is requested explicitly; server-side token and project policy decide authorization")
	}
	if len(plans) == 0 {
		policy.TokenScopesRequired = []string{}
		policy.SecretsRequested = false
		policy.SecretsAuthorized = false
		policy.PolicyReasons = nil
		policy.UploadPreviewCommand = agentSyncPreviewCommand(changes, "")
		applyAgentIntentPolicy(intent, policy)
		return policy
	}
	policy.NetworkMode = plans[0].Options.NetworkMode
	policy.NetworkAllowlist = append([]string{}, plans[0].Options.NetworkAllowlist...)
	for _, plan := range plans[1:] {
		if plan.Options.NetworkMode != policy.NetworkMode {
			policy.PolicyReasons = append(policy.PolicyReasons, "selected checks use multiple network modes")
			policy.NetworkMode = "mixed"
		}
		if strings.Join(plan.Options.NetworkAllowlist, "\x00") != strings.Join(policy.NetworkAllowlist, "\x00") {
			policy.PolicyReasons = append(policy.PolicyReasons, "selected checks use different network allowlists")
			policy.NetworkAllowlist = nil
		}
	}
	applyAgentIntentPolicy(intent, policy)
	return policy
}

func agentPolicyForPlan(changes string, plan checkRunPlan) *agentPolicyReport {
	return agentPolicyForPlansForIntent(changes, "", false, []checkRunPlan{plan})
}

func agentPolicyForPlanForIntent(changes, intent string, requestSecrets bool, plan checkRunPlan) *agentPolicyReport {
	return agentPolicyForPlansForIntent(changes, intent, requestSecrets, []checkRunPlan{plan})
}

func applyAgentIntentPolicy(intent string, policy *agentPolicyReport) {
	if policy == nil || intent != agentIntentSecurity {
		return
	}
	if policy.NetworkMode != "none" {
		policy.PolicyReasons = append(policy.PolicyReasons, "security-sensitive intent should run with network_mode none unless server policy rejects the plan")
	}
	policy.SecretsRequested = false
	policy.SecretsAuthorized = false
	policy.PolicyReasons = append(policy.PolicyReasons, "security-sensitive intent does not request project secrets")
}

func commonPlanCheckName(plans []checkRunPlan) string {
	if len(plans) != 1 {
		return ""
	}
	return plans[0].Name
}

func agentSyncPreviewCommand(changes, check string) string {
	command := "readyci sync --dry-run --json"
	if strings.TrimSpace(changes) != "" {
		command += " --changes " + shellQuoteArg(changes)
	}
	if strings.TrimSpace(check) != "" {
		command += " --check " + shellQuoteArg(check)
	}
	return command
}
