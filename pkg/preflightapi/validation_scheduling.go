package preflightapi

import (
	"fmt"
	"strings"
)

type RunnerSizeSpec struct {
	Name      string
	VCPUCount int
	MemoryMiB int
	SlotCost  int
}

var runnerSizeSpecs = []RunnerSizeSpec{
	{Name: "small", VCPUCount: 1, MemoryMiB: 2560, SlotCost: 1},
	{Name: "standard", VCPUCount: 2, MemoryMiB: 5120, SlotCost: 2},
	{Name: "large", VCPUCount: 4, MemoryMiB: 10240, SlotCost: 4},
	{Name: "xlarge", VCPUCount: 8, MemoryMiB: 20480, SlotCost: 8},
}

func RunnerSizeSpecs() []RunnerSizeSpec {
	specs := make([]RunnerSizeSpec, len(runnerSizeSpecs))
	copy(specs, runnerSizeSpecs)
	return specs
}

func RunnerSizeNames() []string {
	names := make([]string, 0, len(runnerSizeSpecs))
	for _, spec := range runnerSizeSpecs {
		names = append(names, spec.Name)
	}
	return names
}

func RunnerSizeSpecFor(size string) (RunnerSizeSpec, bool) {
	size = strings.TrimSpace(size)
	if size == "" {
		size = DefaultRunnerSize
	}
	for _, spec := range runnerSizeSpecs {
		if spec.Name == size {
			return spec, true
		}
	}
	return RunnerSizeSpec{}, false
}

func RunnerSizeSlotCost(size string) int {
	spec, ok := RunnerSizeSpecFor(size)
	if !ok {
		return 0
	}
	return spec.SlotCost
}

func NormalizeRegion(region string) string {
	region = strings.ToLower(strings.TrimSpace(region))
	if region == "" {
		return DefaultRegion
	}
	return region
}

func ValidateRegion(region string) error {
	region = NormalizeRegion(region)
	if len(region) > 64 {
		return fmt.Errorf("region %q is too long", region)
	}
	if !isRegionLetterOrNumber(rune(region[0])) || !isRegionLetterOrNumber(rune(region[len(region)-1])) {
		return fmt.Errorf("region %q must start and end with a letter or number", region)
	}
	for _, r := range region {
		if isRegionLetterOrNumber(r) {
			continue
		}
		switch r {
		case '-', '_', '.':
			continue
		default:
			return fmt.Errorf("region %q must contain only letters, numbers, dots, underscores, or dashes", region)
		}
	}
	return nil
}

func NormalizeConcurrency(group string, limit int) (string, int, error) {
	group = strings.TrimSpace(group)
	if group != "" {
		if len(group) > MaxConcurrencyGroupLength {
			return "", 0, fmt.Errorf("concurrency_group %q is too long", group)
		}
		for _, r := range group {
			if r < 0x20 || r == 0x7f {
				return "", 0, fmt.Errorf("concurrency_group must not contain control characters")
			}
		}
		if limit == 0 {
			limit = 1
		}
	}
	if limit < 0 {
		return "", 0, fmt.Errorf("concurrency_limit must be non-negative")
	}
	if limit > 0 && group == "" {
		return "", 0, fmt.Errorf("concurrency_limit requires concurrency_group")
	}
	return group, limit, nil
}

func isRegionLetterOrNumber(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
