package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func shouldSkipDetectDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "target", "dist", "coverage", "test-results", "output", "tmp", "vendor", ".next":
		return true
	default:
		return false
	}
}

func nodeCheckName(rel string) string {
	if rel == "" {
		return "web"
	}
	base := sanitizeCheckName(filepath.Base(rel))
	if base == "" || base == "." {
		return "node-" + sanitizeCheckName(rel)
	}
	return base
}

func uniqueCheckName(name string, used map[string]bool) string {
	name = sanitizeCheckName(name)
	if name == "" {
		name = "check"
	}
	if !used[name] {
		used[name] = true
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func sanitizeCheckName(value string) string {
	value = filepath.ToSlash(value)
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func appendExistingRelFiles(values []string, cwd, rel string, names ...string) []string {
	for _, name := range names {
		joined := relJoin(rel, name)
		if fileExists(filepath.Join(cwd, filepath.FromSlash(joined))) {
			values = append(values, joined)
		}
	}
	return values
}

func relJoin(rel, child string) string {
	child = filepath.ToSlash(child)
	if rel == "" {
		return child
	}
	return filepath.ToSlash(filepath.Join(rel, child))
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func displayRel(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

func sameFilepath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasAnyFile(cwd string, names ...string) bool {
	for _, name := range names {
		if fileExists(filepath.Join(cwd, name)) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsCheckName(checks []detectedCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func commandListForExistingChecks(checks []detectedCheck, candidates ...string) config.CommandList {
	var out config.CommandList
	for _, candidate := range candidates {
		if containsCheckName(checks, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func filterDetectedAffectedProfiles(profiles []string, checks []detectedCheck) []string {
	if len(profiles) == 0 || len(checks) == 0 {
		return nil
	}
	checkMap := make(map[string]config.CheckConfig, len(checks))
	for _, check := range checks {
		checkMap[check.Name] = config.CheckConfig{Command: check.Command}
	}
	var filtered []string
	for _, profile := range uniqueStrings(profiles) {
		rules, ok := affectedProfileRules(profile)
		if !ok {
			continue
		}
		for _, rule := range rules {
			if len(existingAffectedProfileChecks(checkMap, rule)) > 0 {
				filtered = append(filtered, profile)
				break
			}
		}
	}
	return filtered
}

func isLikelyVitePackage(pkg detectedNodePackage) bool {
	return hasAnyFile(pkg.Dir, "vite.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.cjs", "vite.config.mts", "vite.config.cts") ||
		pathExists(pkg.Dir, "src") ||
		strings.Contains(pkg.Rel, "web")
}
