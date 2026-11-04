package verificationplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

// Preparation and consumption may declare different views of one canonical
// snapshot. Change selection still belongs to the session, not to each view.
func ValidateDependencySourceScope(warm, target CheckPlan) error {
	warmMode, err := syncbundle.NormalizeChangeMode(warm.Run.ChangeMode)
	if err != nil {
		return err
	}
	targetMode, err := syncbundle.NormalizeChangeMode(target.Run.ChangeMode)
	if err != nil {
		return err
	}
	if warmMode != targetMode {
		return fmt.Errorf("dependency check %q and consumer %q require different change modes; one session must use one exact workspace", warm.Name, target.Name)
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
