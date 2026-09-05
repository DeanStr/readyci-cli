package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

type setupOptions struct {
	APIURL        string
	APIToken      string
	ProjectID     string
	CheckName     string
	Profile       string
	WriteConfig   bool
	WriteMakefile bool
	WriteHelpers  bool
	Detect        bool
	Force         bool
}

func setupMakefileScaffold(apiURL, projectID string) string {
	apiURL = normalizeAPIURL(apiURL)
	projectID = strings.TrimSpace(projectID)
	if apiURL == "" {
		apiURL = defaultAPIURL()
	}
	if projectID == "" {
		projectID = "proj_dev"
	}
	return fmt.Sprintf(`READYCI ?= readyci
READYCI_API_URL ?= %s
READYCI_PROJECT_ID ?= %s
READYCI_CHECK_FLAGS ?=
READYCI_RUN_FLAGS ?=
READYCI_WARM_FLAGS ?=
export READYCI_API_URL READYCI_PROJECT_ID

define readyci-check
	@$(READYCI) check $(READYCI_CHECK_FLAGS) $(1)
endef

remote-check-touched:
	@$(READYCI) affected $(READYCI_CHECK_FLAGS)

remote-check-staged:
	@$(READYCI) affected --staged $(READYCI_CHECK_FLAGS)

remote-check-ci:
	$(call readyci-check,ci)

remote-check-all:
	$(call readyci-check,all)

remote-check-db:
	$(call readyci-check,db)

remote-warm:
	@$(READYCI) warm --profile dependencies $(READYCI_WARM_FLAGS)

remote-release-check-fast:
	@set -e; \
	$(READYCI) run $(READYCI_RUN_FLAGS) --network-mode default --runner-size large --timeout 90m "make release-check-fast"; \
	if [ "$${RUN_DB:-0}" = "1" ]; then \
		$(READYCI) check $(READYCI_CHECK_FLAGS) db; \
	fi

remote-check-affected-audit:
	@$(READYCI) affected audit --strict --duplicates
`, apiURL, projectID)
}

func resolveSetupProjectID(explicitProjectID string, fallbackConfig config.ProjectConfig) string {
	projectID := strings.TrimSpace(explicitProjectID)
	if projectID != "" {
		return projectID
	}
	cfg := fallbackConfig
	if cfg.ProjectID == "" {
		if loaded, _, err := config.LoadProjectConfig("."); err == nil {
			cfg = loaded
		}
	}
	if projectID = strings.TrimSpace(cfg.ProjectID); projectID != "" {
		return projectID
	}
	if projectID = strings.TrimSpace(os.Getenv("READYCI_PROJECT_ID")); projectID != "" {
		return projectID
	}
	if cwd, err := os.Getwd(); err == nil {
		return inferProjectID(cwd)
	}
	return ""
}

func setupHelperScript(name string) (string, string, error) {
	switch strings.TrimSpace(name) {
	case "shell-syntax":
		return "scripts/readyci-check-shell-syntax.sh", readyCIShellSyntaxHelperScript(), nil
	case "ops-config":
		return "scripts/readyci-check-ops-config.sh", readyCIOpsConfigHelperScript(), nil
	default:
		return "", "", fmt.Errorf("unknown setup helper %q (available: shell-syntax, ops-config)", name)
	}
}

func writeSetupHelperScript(path string, content string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return cliConfigError("helper path cannot be empty", nil)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("helper path %q must be relative", path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return fmt.Errorf("helper path %q must stay inside the repository", path)
	}
	root := setupHelperWriteRoot()
	cleaned = filepath.Join(root, cleaned)
	if err := validateSetupHelperParentPath(root, cleaned); err != nil {
		return err
	}
	if info, err := os.Lstat(cleaned); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to overwrite helper through a symlink", cleaned)
		}
		if !force {
			return fmt.Errorf("%s already exists; pass --force to overwrite", cleaned)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cleaned), 0o755); err != nil {
		return err
	}
	if err := validateSetupHelperParentPath(root, cleaned); err != nil {
		return err
	}
	if err := os.WriteFile(cleaned, []byte(content), 0o755); err != nil {
		return err
	}
	return os.Chmod(cleaned, 0o755)
}

func writeSetupGeneratedFile(path string, content string, mode os.FileMode, force bool) error {
	if strings.TrimSpace(path) == "" {
		return cliConfigError("generated file path cannot be empty", nil)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("generated file path %q must be relative", path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("generated file path %q must stay inside the repository", path)
	}
	root := setupHelperWriteRoot()
	target := filepath.Join(root, cleaned)
	if err := validateSetupHelperParentPath(root, target); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to overwrite generated file through a symlink", target)
		}
		if !force {
			return fmt.Errorf("%s already exists; pass --force to overwrite", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := validateSetupHelperParentPath(root, target); err != nil {
		return err
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		return err
	}
	return os.Chmod(target, mode)
}

func validateSetupHelperParentPath(root string, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("helper path %q must stay inside the repository", target)
	}
	parentRel := filepath.Dir(rel)
	if parentRel == "." {
		return nil
	}
	current := rootAbs
	for _, part := range strings.Split(parentRel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to write helper through a symlinked parent directory", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", current)
		}
	}
	return nil
}

func setupHelperWriteRoot() string {
	output, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "."
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "."
	}
	return root
}
