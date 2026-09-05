package syncbundlebuild

import "testing"

func TestDefaultIgnoresAllowEnvExamplesButKeepBuildOutputExcluded(t *testing.T) {
	matcher := newIgnoreMatcher(nil)
	cases := []struct {
		path string
		want bool
	}{
		{path: ".env", want: true},
		{path: ".env.local", want: true},
		{path: "web/.env.local", want: true},
		{path: ".env.example", want: false},
		{path: "nested/app.env.example", want: false},
		{path: "dist/.env.example", want: true},
		{path: "node_modules/pkg/.env.example", want: true},
		{path: "target/app.env.example", want: true},
		{path: "apps/web/.next/.env.example", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := matcher.ignored(tc.path); got != tc.want {
				t.Fatalf("ignored(%q) = %t, want %t", tc.path, got, tc.want)
			}
		})
	}
}

func TestExtraIgnoresStillExcludeEnvExamples(t *testing.T) {
	matcher := newIgnoreMatcher([]string{".env.example", "nested/*.env.example"})
	for _, path := range []string{".env.example", "nested/app.env.example"} {
		t.Run(path, func(t *testing.T) {
			if !matcher.ignored(path) {
				t.Fatalf("ignored(%q) = false, want true", path)
			}
		})
	}
}
