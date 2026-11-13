package syncbundle

import (
	"strings"
	"testing"
)

func TestCredentialAdmissionRequiresExactReviewedFixture(t *testing.T) {
	for _, rel := range []string{"id_rsa", ".aws/credentials"} {
		binding := SourcePolicyBinding{Digest: "sha256:" + strings.Repeat("a", 64), IncludeUntracked: true}
		if SourcePolicyAllowsPath(binding, rel, true) || SourcePolicyAllowsPath(binding, rel, false) {
			t.Errorf("credential path admitted without review: %s", rel)
		}
		manifest := Manifest{Workspace: []Entry{{Path: rel, Type: EntryTypeFile}}}
		if err := validateSourceFilterEntries(manifest); err == nil {
			t.Errorf("unbound archive bypassed confidentiality: %s", rel)
		}
		binding.ReviewedFixtures = []ReviewedFixture{{Path: rel, SHA256: strings.Repeat("b", 64)}}
		if !SourcePolicyAllowsPath(binding, rel, false) || SourcePolicyAllowsPath(binding, rel, true) {
			t.Errorf("reviewed fixture tracking not enforced: %s", rel)
		}
		binding.ExcludePatterns = []string{rel}
		if SourcePolicyAllowsPath(binding, rel, false) {
			t.Errorf("fixture bypassed explicit policy: %s", rel)
		}
	}
}

func TestCredentialNamesOnlyExcludeUntrackedConfiguration(t *testing.T) {
	binding := SourcePolicyBinding{Digest: "sha256:" + strings.Repeat("a", 64), IncludeUntracked: true, DefaultUntrackedExcludePatterns: DefaultUntrackedExcludePatterns()}
	for _, rel := range []string{".npmrc", "testdata/credentials.json", ".docker/config.json"} {
		if !SourcePolicyAllowsPath(binding, rel, false) || SourcePolicyAllowsPath(binding, rel, true) {
			t.Errorf("incorrect origin filtering: %s", rel)
		}
	}
}
