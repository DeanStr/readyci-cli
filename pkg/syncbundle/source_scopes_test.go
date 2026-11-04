package syncbundle

import (
	"slices"
	"strings"
	"testing"
)

func TestSourceScopeSetCanonicalizationAndConfidentiality(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	first := SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{"server"}, IncludeUntracked: true}
	second := SourcePolicyBinding{Digest: digest, ExcludePatterns: []string{"web"}, IncludeUntracked: false}
	binding, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: []SourcePolicyBinding{first, second, first}})
	if err != nil || len(binding.Scopes) != 2 {
		t.Fatalf("scope set = %+v, %v", binding, err)
	}
	reversed, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: []SourcePolicyBinding{second, first}})
	if err != nil || SourcePolicyScopeKey(binding) != SourcePolicyScopeKey(reversed) {
		t.Fatal("scope order or duplicates changed identity")
	}
	if err := validateSourcePolicyBinding(binding); err != nil {
		t.Fatal(err)
	}
	reversed.Scopes = slices.Clone(binding.Scopes)
	slices.Reverse(reversed.Scopes)
	if err := validateSourcePolicyBinding(reversed); err == nil {
		t.Fatal("non-canonical scopes were admitted")
	}
	for _, test := range []struct {
		path               string
		untracked, allowed bool
	}{
		{"web/main.ts", false, true}, {"server/main.rs", false, true},
		{"web/new.ts", true, true}, {"server/new.rs", true, false},
		{"web/.env.secret", false, false}, {".git/config", false, false},
	} {
		if actual := SourcePolicyAllowsPath(binding, test.path, test.untracked); actual != test.allowed {
			t.Errorf("%s untracked=%v allowed=%v", test.path, test.untracked, actual)
		}
	}
	first.Scopes = []SourcePolicyBinding{second}
	if _, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: []SourcePolicyBinding{first, second}}); err == nil {
		t.Fatal("nested source sets accepted")
	}
	first.Scopes = nil
	first.ReviewedFixtures = []ReviewedFixture{{Path: "test/.env.fixture", SHA256: strings.Repeat("a", 64)}}
	second.ReviewedFixtures = []ReviewedFixture{{Path: "test/.env.fixture", SHA256: strings.Repeat("b", 64)}}
	if _, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: []SourcePolicyBinding{first, second}}); err == nil {
		t.Fatal("incoherent reviewed bytes accepted")
	}
	second.ReviewedFixtures = first.ReviewedFixtures
	if shared, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: digest, Scopes: []SourcePolicyBinding{first, second}}); err != nil || len(shared.ReviewedFixtures) != 1 {
		t.Fatalf("shared fixture = %+v, %v", shared, err)
	}
}

func TestRequestedSourcePolicyDigestNamesOnlyTheCanonicalScopeSet(t *testing.T) {
	web := SourcePolicyBinding{Digest: "sha256:" + strings.Repeat("a", 64), ExcludePatterns: []string{"server", "docs"}, IncludeUntracked: true}
	server := SourcePolicyBinding{Digest: "sha256:" + strings.Repeat("b", 64), ExcludePatterns: []string{"web"}}
	digest, err := RequestedSourcePolicyDigest([]SourcePolicyBinding{web, server})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshotDigest(digest); err != nil {
		t.Fatal(err)
	}
	reordered := web
	reordered.ExcludePatterns = []string{"docs", "server"}
	same, err := RequestedSourcePolicyDigest([]SourcePolicyBinding{server, reordered, web})
	if err != nil || same != digest {
		t.Fatalf("member order, duplicates or caller digests changed the name: %q %v", same, err)
	}
	widened := web
	widened.ExcludePatterns = []string{"server"}
	if other, err := RequestedSourcePolicyDigest([]SourcePolicyBinding{widened, server}); err != nil || other == digest {
		t.Fatalf("a different filter kept the same name: %q %v", other, err)
	}
	for name, scope := range map[string]SourcePolicyBinding{
		"nested":         {Scopes: []SourcePolicyBinding{web}},
		"project ignore": {ExcludePatterns: []string{"web"}, ProjectIgnoreUsed: true},
	} {
		if _, err := RequestedSourcePolicyDigest([]SourcePolicyBinding{web, scope}); err == nil {
			t.Fatalf("%s scope was named", name)
		}
	}
}
