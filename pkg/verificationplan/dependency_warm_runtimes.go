package verificationplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type dependencyWarmRuntimeGroup struct {
	requirements map[string]string
	targets      []string
}

// Group identical effective declarations so neither version selection nor cache
// identity depends on which other checks happen to be selected. Standalone warm
// sessions and later offline consumers must address the same dependency files.
func dependencyWarmRuntimeGroups(warm map[string]string, targets []CheckPlan) ([]dependencyWarmRuntimeGroup, error) {
	var pending []dependencyWarmRuntimeGroup
	for _, target := range targets {
		requirements, err := mergeDependencyWarmRequirements(warm, target.Run.RuntimeRequirements)
		if err != nil {
			return nil, fmt.Errorf("dependency warm runtime requirements for %s: %w", target.Name, err)
		}
		pending = append(pending, dependencyWarmRuntimeGroup{requirements: requirements, targets: []string{target.Name}})
	}
	sort.Slice(pending, func(i, j int) bool {
		if a, b := pending[i].key(), pending[j].key(); a != b {
			return a < b
		}
		return pending[i].targets[0] < pending[j].targets[0]
	})
	var groups []dependencyWarmRuntimeGroup
	for _, target := range pending {
		last := len(groups) - 1
		if last >= 0 && groups[last].key() == target.key() {
			groups[last].targets = append(groups[last].targets, target.targets...)
		} else {
			groups = append(groups, target)
		}
	}
	return groups, nil
}

func mergeDependencyWarmRequirements(left, right map[string]string) (map[string]string, error) {
	left, err := preflightapi.NormalizeRuntimeRequirements(left)
	if err != nil {
		return nil, err
	}
	right, err = preflightapi.NormalizeRuntimeRequirements(right)
	if err != nil {
		return nil, err
	}
	// Repeated consumers must not exhaust the bounded comparator grammar.
	for name, value := range right {
		if previous := left[name]; previous != "" {
			if preflightapi.RuntimeRequirementImplies(previous, value) {
				delete(right, name)
			} else if preflightapi.RuntimeRequirementImplies(value, previous) {
				delete(left, name)
			}
		}
	}
	return preflightapi.MergeRuntimeRequirements(left, right)
}

func (group dependencyWarmRuntimeGroup) key() string {
	encoded, _ := json.Marshal(group.requirements)
	return string(encoded)
}

func (group dependencyWarmRuntimeGroup) digest() string {
	digest := sha256.Sum256([]byte(group.key()))
	return hex.EncodeToString(digest[:12])
}

func (group dependencyWarmRuntimeGroup) description() string {
	var names []string
	for name := range group.requirements {
		names = append(names, name)
	}
	sort.Strings(names)
	var descriptions []string
	for _, name := range names {
		descriptions = append(descriptions, name+"="+group.requirements[name])
	}
	description := strings.Join(descriptions, "; ")
	if len(description) > 64 {
		description = description[:61] + "..."
	}
	return description
}
