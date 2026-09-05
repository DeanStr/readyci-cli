package syncbundle

import (
	"reflect"
	"testing"
)

func TestNormalizeSourcePolicyBindingCanonicalizesExcludes(t *testing.T) {
	binding, err := NormalizeSourcePolicyBinding(SourcePolicyBinding{
		Digest:                 " sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ",
		ExcludePatterns:        []string{" z/** ", "", "a/**", "z/**"},
		DefaultExcludePatterns: []string{" target ", ".env*", "target"},
		IncludeUntracked:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if binding.Digest != "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		!reflect.DeepEqual(binding.ExcludePatterns, []string{"a/**", "z/**"}) ||
		!reflect.DeepEqual(binding.DefaultExcludePatterns, []string{".env*", "target"}) {
		t.Fatalf("normalized binding = %+v", binding)
	}
	if err := validateSourcePolicyBinding(binding); err != nil {
		t.Fatalf("validate canonical binding: %v", err)
	}
	binding.ExcludePatterns = []string{"z/**", "a/**"}
	if err := validateSourcePolicyBinding(binding); err == nil {
		t.Fatal("non-canonical binding validated")
	}
	binding.ExcludePatterns = []string{"a/**", "z/**"}
	binding.DefaultExcludePatterns = []string{"target", ".env*"}
	if err := validateSourcePolicyBinding(binding); err == nil {
		t.Fatal("non-canonical default binding validated")
	}
}
