package preflightapi

import (
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func TestComponentSourceRequiresServiceAuthorityAndChangesSealedIdentity(t *testing.T) {
	req := validVerificationRequest()
	source := VerificationNodeSource{SnapshotDigest: "sha256:" + strings.Repeat("b", 64), ChangedFiles: []string{"a.go"}, Policy: syncbundle.SourcePolicyBinding{Digest: "sha256:" + strings.Repeat("c", 64), ExcludePatterns: []string{"other-component"}}, ManifestDigest: "sha256:" + strings.Repeat("f", 64)}
	req.Plan.Nodes[0].Source = &source
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
		t.Fatal("client-proposed source view granted authority")
	}
	req.Plan.Authority = VerificationPlanAuthorityServer
	for _, invalid := range []string{"", strings.Repeat("f", 64), "sha256:" + strings.Repeat("F", 64)} {
		source.ManifestDigest = invalid
		if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
			t.Fatalf("source manifest digest %q was accepted", invalid)
		}
	}
	source.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
	normalized, err := NormalizeCreateVerificationSessionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*VerificationNodeSource){
		func(s *VerificationNodeSource) { s.SnapshotDigest = "sha256:" + strings.Repeat("d", 64) },
		func(s *VerificationNodeSource) { s.ChangedFiles = []string{"another.go"} },
		func(s *VerificationNodeSource) { s.Policy.ExcludePatterns = []string{"different-component"} },
		func(s *VerificationNodeSource) { s.BaselineKey = strings.Repeat("e", 64) },
		func(s *VerificationNodeSource) { s.ManifestDigest = "sha256:" + strings.Repeat("0", 64) },
	} {
		copy := *normalized.Plan.Nodes[0].Source
		plan := normalized.Plan
		plan.Nodes = append([]VerificationNodePlan(nil), plan.Nodes...)
		plan.Nodes[0].Source = &copy
		mutate(&copy)
		changed, err := VerificationPlanDigest(plan)
		if err != nil || changed == normalized.Plan.Digest {
			t.Fatalf("source mutation did not change sealed identity: %v", err)
		}
	}
}
