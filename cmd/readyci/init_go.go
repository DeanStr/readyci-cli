package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func detectGoModules(cwd string) []string {
	var modules []string
	_ = filepath.WalkDir(cwd, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != cwd && shouldSkipDetectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			modules = append(modules, relPath(cwd, filepath.Dir(path)))
		}
		return nil
	})
	sort.Slice(modules, func(i, j int) bool {
		if modules[i] == "" {
			return true
		}
		if modules[j] == "" {
			return false
		}
		return modules[i] < modules[j]
	})
	return modules
}

func detectGoPostgresSignals(cwd, module string) []string {
	root := filepath.Join(cwd, filepath.FromSlash(module))
	var signals []string
	if fileContainsAny(filepath.Join(root, "go.mod"), "github.com/jackc/pgx", "github.com/lib/pq", "gorm.io/driver/postgres") {
		signals = append(signals, displayRel(module)+" go.mod references a Postgres driver")
	}
	if anyPathExists(root, "migrations", "db/migrations", "database/migrations") {
		signals = append(signals, displayRel(module)+" contains migration directories")
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && shouldSkipDetectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		rel := relPath(cwd, path)
		switch {
		case strings.Contains(text, "READYCI_TEST_DATABASE_URL"):
			signals = append(signals, rel+" references READYCI_TEST_DATABASE_URL")
		case strings.Contains(text, "TEST_DATABASE_URL"), strings.Contains(text, "DATABASE_URL"):
			signals = append(signals, rel+" references database URL environment variables")
		case strings.Contains(text, "database/sql") && strings.Contains(strings.ToLower(text), "postgres"):
			signals = append(signals, rel+" uses database/sql with Postgres signals")
		case strings.Contains(text, "t.Skip") && (strings.Contains(text, "TEST_DATABASE_URL") || strings.Contains(text, "DATABASE_URL")):
			signals = append(signals, rel+" may skip DB-backed tests when database env is missing")
		}
		return nil
	})
	return uniqueStrings(signals)
}

func fileContainsAny(path string, needles ...string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(data)
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func generatedShellSyntaxCommand() string {
	return `bash -lc 'declare -A files=(); while IFS= read -r -d "" file; do files["$file"]=1; done < <(git ls-files -z -- "*.sh" "scripts/git-hooks/*"); while IFS= read -r -d "" file; do files["$file"]=1; done < <(git ls-files -z --others --exclude-standard -- "*.sh" "scripts/git-hooks/*"); status=0; while IFS= read -r -d "" file; do [[ -f "$file" ]] || continue; bash -n -- "$file" || status=$?; done < <(printf "%s\0" "${!files[@]}" | sort -z); exit "$status"'`
}
