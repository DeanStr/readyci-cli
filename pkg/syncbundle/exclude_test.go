package syncbundle

import (
	"strings"
	"testing"
)

func TestPathMatchesExcludePatternsUsesBundleFilterSemantics(t *testing.T) {
	patterns := []string{"", "# comment", "/vendor/", "*.generated.go", "nested/cache"}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "vendor/module/file.go", want: true},
		{path: "src/vendor/module/file.go", want: true},
		{path: "src/model.generated.go", want: true},
		{path: "nested/cache/item", want: true},
		{path: "src/main.go", want: false},
	} {
		t.Run(test.path, func(t *testing.T) {
			if got := PathMatchesExcludePatterns(patterns, test.path); got != test.want {
				t.Fatalf("PathMatchesExcludePatterns(%q) = %t, want %t", test.path, got, test.want)
			}
		})
	}
}

func TestExclusionGlobsCoverNestedFilesAndDirectoryAncestors(t *testing.T) {
	for _, test := range []struct {
		pattern, path string
		want          bool
	}{
		{"secrets/**", "secrets/deep/token.txt", true},
		{"secrets/**", "secrets/token.txt", true},
		{"secrets/**", "public/secrets.txt", false},
		{"**/secrets/**", "apps/web/secrets/deep/token.txt", true},
		{"apps/**/private/*.txt", "apps/private/token.txt", true},
		{"apps/**/private/*.txt", "apps/web/deep/private/token.txt", true},
		{"*.private", "nested/config.private/token.txt", true},
		{"build-*", "nested/build-output/deep/result.txt", true},
		{"apps/*/private", "apps/web/private/deep/token.txt", true},
		{"apps/*/private", "apps/web/deep/private/token.txt", false},
		{"nested/cache", "src/nested/cache/item", false},
		{"{private/**,secrets}", "nested/private/token.txt", true},
		{"{private/**,secrets}", "nested/secrets/token.txt", true},
		{"private/**", "private", true},
		{"apps/**/private/*.txt", "apps/web/private/token.txt/contents", true},
		{"\\#private", "nested/#private", true},
		{"token\\[private\\].txt", "nested/token[private].txt", true},
	} {
		t.Run(test.pattern+":"+test.path, func(t *testing.T) {
			if got := PathMatchesExcludePatterns([]string{test.pattern}, test.path); got != test.want {
				t.Fatalf("excluded=%t, want %t", got, test.want)
			}
		})
	}
}

func TestDeepSourceExclusionsPreserveConfidentialityAndRootedRules(t *testing.T) {
	prefix := strings.Repeat("component/", 256)
	for _, test := range []struct {
		pattern, path string
		want          bool
	}{
		{"**/.aws/credentials", prefix + "src/main.go", false},
		{"**/.aws/credentials", prefix + ".aws/credentials", true},
		{"**/.aws/credentials", prefix + ".aws/credentials/backup", true},
		{".env*", prefix + ".env.local", true},
		{"id_rsa", prefix + "id_rsa/contents", true},
		{"src/private", prefix + "src/private/key", false},
		{"src/private", "src/private/" + prefix + "key", true},
	} {
		t.Run(test.pattern, func(t *testing.T) {
			if got := PathMatchesExcludePatterns([]string{test.pattern}, test.path); got != test.want {
				t.Fatalf("path %q excluded=%t, want %t", test.path, got, test.want)
			}
		})
	}
}

func TestSourceExclusionsRejectInvalidConfidentialityRules(t *testing.T) {
	for _, pattern := range []string{"[broken", "!secrets/**", "secret\x00file", "/", "./", "../private/**"} {
		if err := ValidateExcludePatterns([]string{pattern}); err == nil {
			t.Fatalf("invalid exclusion %q was silently accepted", pattern)
		}
		if _, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExcludePatterns: []string{pattern}}); err == nil {
			t.Fatalf("registered policy accepted invalid exclusion %q", pattern)
		}
	}
}
