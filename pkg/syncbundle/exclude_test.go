package syncbundle

import "testing"

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
