package main

import (
	"slices"
	"strings"
	"testing"
)

func TestAgentPolicyReportsOnlyExplicitSecretRequests(t *testing.T) {
	plans := []checkRunPlan{{Name: "integration", Options: remoteRunOptions{NetworkMode: "none"}}}
	for _, tc := range []struct {
		intent    string
		requested bool
		want      bool
	}{
		{agentIntentEditLoop, false, false},
		{agentIntentEditLoop, true, true},
		{agentIntentSecurity, true, false},
	} {
		policy := agentPolicyForPlansForIntent("working", tc.intent, tc.requested, plans)
		scoped := slices.Contains(policy.TokenScopesRequired, "runs:create_with_secrets")
		if policy.SecretsRequested != tc.want || scoped != tc.want || policy.SecretsAuthorized {
			t.Fatalf("%s requested=%t: policy = %+v", tc.intent, tc.requested, policy)
		}
	}
	security := agentPolicyForPlansForIntent("working", agentIntentSecurity, true, plans)
	if !strings.Contains(strings.Join(security.PolicyReasons, "\n"), "does not request project secrets") {
		t.Fatalf("security policy reasons = %v", security.PolicyReasons)
	}
}
