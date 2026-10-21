package preflightapi

import "encoding/json"

// RunResources is diagnostic, worker-observed VM usage, not guest-process usage
// or verification evidence. Nil values are unavailable, never measured zeroes.
type RunResources struct {
	Basis            string            `json:"basis"`
	PeakMemoryBytes  *uint64           `json:"peak_memory_bytes"`
	MemoryLimitBytes *uint64           `json:"memory_limit_bytes"`
	CPUUsageUS       *uint64           `json:"cpu_usage_us"`
	CPUThrottledUS   *uint64           `json:"cpu_throttled_us"`
	OOMKills         *uint64           `json:"oom_kills"`
	Unavailable      map[string]string `json:"unavailable"`
}

func ResourcesFromEvents(events []RunEvent) *RunResources {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventVMStarting || events[i].Type == EventRunScheduled {
			return nil
		}
		if events[i].Type != EventResourceUsage {
			continue
		}
		data, err := events[i].DataBytes()
		var result RunResources
		if err == nil && json.Unmarshal(data, &result) == nil && result.Basis == "host_vm_cgroup_v2" {
			return &result
		}
	}
	return nil
}
