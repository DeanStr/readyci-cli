package verificationplan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// A component installer cannot silently broaden or narrow its consumer's
// uploaded source. Independent components may use different sealed sessions.
func ValidateDependencySourceScope(warm, target CheckPlan) error {
	key := func(run RunSpec) (string, error) {
		changes, err := syncbundle.NormalizeChangeMode(run.ChangeMode)
		if err != nil {
			return "", err
		}
		exclude := append([]string{}, run.SyncExclude...)
		sort.Strings(exclude)
		var canonical []string
		for _, pattern := range exclude {
			if len(canonical) == 0 || canonical[len(canonical)-1] != pattern {
				canonical = append(canonical, pattern)
			}
		}
		include := true
		if run.IncludeUntracked != nil {
			include = *run.IncludeUntracked
		}
		if changes == syncbundle.ChangeModeStaged {
			include = false
		}
		fixtures, err := syncbundle.NormalizeReviewedFixtures(run.SyncReviewedFixtures)
		if err != nil {
			return "", err
		}
		encoded, _ := json.Marshal(struct {
			Changes  string
			Exclude  []string
			Include  bool
			Fixtures []syncbundle.ReviewedFixture
		}{changes, canonical, include, fixtures})
		return string(encoded), nil
	}
	warmKey, err := key(warm.Run)
	if err != nil {
		return err
	}
	targetKey, err := key(target.Run)
	if err != nil {
		return err
	}
	if warmKey != targetKey {
		return fmt.Errorf("dependency check %q and consumer %q require different sync.exclude, reviewed_fixtures, or include_untracked settings (or change mode); one sealed verification session must use one exact workspace", warm.Name, target.Name)
	}
	return nil
}

// DependencyWarmPlansForChecks resolves each consumer's installer from trusted
// configuration. Callers cannot substitute a warm command, source scope or SDK.
func DependencyWarmPlansForChecks(cfg config.ProjectConfig, targets []CheckPlan, options ResolveOptions) ([]CheckPlan, error) {
	groups := map[string][]CheckPlan{}
	for _, target := range targets {
		name, err := config.DependencyCheckFor(cfg, target.Name)
		if err != nil {
			return nil, err
		}
		if name == "" || target.Run.CacheMode == "ephemeral" {
			continue
		}
		groups[name] = append(groups[name], target)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	mode := strings.TrimSpace(cfg.Network.WarmMode)
	if mode == "" {
		return nil, fmt.Errorf("dependency warming requires an explicit network.warm_mode")
	}
	options.NetworkMode = mode
	options.NetworkAllowlist = strings.Join(cfg.Network.WarmAllowlist, ",")
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var plans []CheckPlan
	for _, name := range names {
		warm, err := ResolveCheck(name, cfg, options)
		if err != nil {
			return nil, fmt.Errorf("dependency_check %q: %w", name, err)
		}
		secrets := false
		warm.Run.SecretsRequested = &secrets
		resolved, err := DependencyWarmPlansForTargets(warm, groups[name])
		if err != nil {
			return nil, err
		}
		plans = append(plans, resolved...)
	}
	return plans, nil
}
