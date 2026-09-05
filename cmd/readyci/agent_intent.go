package main

import (
	"fmt"
	"strings"
)

var agentIntentValues = []string{
	agentIntentEditLoop,
	agentIntentPreCommit,
	agentIntentPrePR,
	agentIntentFixtureUpdate,
	agentIntentSecurity,
	agentIntentFinalHandoff,
}

func normalizeAgentIntent(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return agentIntentEditLoop, nil
	}
	for _, allowed := range agentIntentValues {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid intent %q (available: %s)", value, strings.Join(agentIntentValues, ", "))
}

func agentIntentRequestsBroadVerification(intent, goal string) bool {
	return intent == agentIntentFinalHandoff || intent == agentIntentFixtureUpdate || agentGoalRequestsBroadVerification(goal)
}

func agentIntentDefaultChanges(intent, changes string) string {
	if strings.TrimSpace(changes) != "" {
		return changes
	}
	if intent == agentIntentPreCommit {
		return "staged"
	}
	return changes
}

func agentIntentDisplayGoal(intent, goal string) string {
	goal = strings.TrimSpace(goal)
	if goal != "" {
		return goal
	}
	return intent
}

func agentResolveOptionsForIntent(intent string, options checkResolveOptions) checkResolveOptions {
	if intent == agentIntentSecurity {
		options.NetworkMode = "none"
		options.NetworkAllowlist = ""
	}
	return options
}

func validateAgentSecretRequestForIntent(intent string, requestSecrets bool) error {
	if requestSecrets && intent == agentIntentSecurity {
		return fmt.Errorf("--request-secrets cannot be combined with intent %s", agentIntentSecurity)
	}
	return nil
}

func applyAgentRemoteRunOptionsForIntent(intent string, requestSecrets bool, options *remoteRunOptions) {
	if options == nil {
		return
	}
	// Make the run request explicit so secret-capable tokens cannot trigger
	// legacy nil-as-requested behavior unless the agent opted in.
	options.SecretsRequested = boolPtr(requestSecrets && intent != agentIntentSecurity)
}

func applyAgentAffectedPlanOptionsForIntent(intent string, requestSecrets bool, plans []affectedCheckPlan) []affectedCheckPlan {
	for i := range plans {
		applyAgentRemoteRunOptionsForIntent(intent, requestSecrets, &plans[i].Plan.Options)
	}
	return plans
}

func agentRunGroupNameForIntent(intent, label string) string {
	intent = defaultString(strings.TrimSpace(intent), agentIntentEditLoop)
	label = strings.TrimSpace(label)
	if label == "" {
		return "agent " + intent + " verification"
	}
	return "agent " + intent + " " + label
}
