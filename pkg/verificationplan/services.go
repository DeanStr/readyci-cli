package verificationplan

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func validateServiceHealthDuration(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return errors.New("must be a positive duration")
	}
	return nil
}

// SortedServiceNames lists configured service names in order.
func SortedServiceNames(services map[string]config.ServiceConfig) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validServiceName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for index, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if index > 0 && (r == '-' || r == '_') {
			continue
		}
		return false
	}
	return true
}

// ValidServiceEnvKey reports whether a service env key is well formed.
func ValidServiceEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' {
			continue
		}
		if index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// SecretBearingServiceEnv reports whether a service env pair looks like an
// inline secret, which configuration must not carry.
func SecretBearingServiceEnv(key, value string) bool {
	return preflightapi.SecretBearingRunServiceEnv(key, value)
}

// Small generic helpers are kept private to the shared planning package.
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ResolvedServiceType resolves a service's effective type.
func ResolvedServiceType(service config.ServiceConfig) string {
	serviceType := strings.TrimSpace(service.Type)
	if serviceType == "" && strings.TrimSpace(service.Image) != "" {
		return "container"
	}
	return serviceType
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

// ValidateServiceConfigs validates declared project services.
func ValidateServiceConfigs(services map[string]config.ServiceConfig) error {
	for _, name := range SortedServiceNames(services) {
		service := services[name]
		if !validServiceName(name) {
			return fmt.Errorf("services.%s has invalid service name (use letters, numbers, underscore, or hyphen)", name)
		}
		serviceType := strings.TrimSpace(service.Type)
		if service.Type != serviceType {
			return fmt.Errorf("services.%s.type must not contain surrounding whitespace", name)
		}
		if serviceType == "" && strings.TrimSpace(service.Image) != "" {
			serviceType = "container"
		}
		switch serviceType {
		case "postgres":
			if strings.TrimSpace(service.Image) != "" {
				return fmt.Errorf("services.%s.image is only supported for container services", name)
			}
			if len(service.Ports) > 0 {
				return fmt.Errorf("services.%s.ports are only supported for container services", name)
			}
			switch service.Version {
			case "", "15", "16", "17":
			default:
				return fmt.Errorf("services.%s.version must be one of 15, 16, 17", name)
			}
		case "container":
			if strings.TrimSpace(service.Version) != "" {
				return fmt.Errorf("services.%s.version is only supported for postgres services", name)
			}
			if !preflightapi.ValidRunServiceImage(service.Image) {
				return fmt.Errorf("services.%s.image is invalid", name)
			}
			if len(service.Ports) > preflightapi.MaxRunServicePorts {
				return fmt.Errorf("service %q ports must contain at most %d entries", name, preflightapi.MaxRunServicePorts)
			}
			seenPorts := make(map[string]struct{}, len(service.Ports))
			for _, port := range service.Ports {
				if err := preflightapi.ValidateRunServicePort(port); err != nil {
					return fmt.Errorf("services.%s.ports contains invalid port %q: %w", name, port, err)
				}
				if _, ok := seenPorts[port]; ok {
					return fmt.Errorf("services.%s.ports contains duplicate port %q", name, port)
				}
				seenPorts[port] = struct{}{}
			}
		default:
			return fmt.Errorf("services.%s.type must be postgres or container", name)
		}
		if len(service.Env) > preflightapi.MaxRunServiceEnvVars {
			return fmt.Errorf("service %q env must contain at most %d entries", name, preflightapi.MaxRunServiceEnvVars)
		}
		for key, value := range service.Env {
			if !ValidServiceEnvKey(key) {
				return fmt.Errorf("services.%s.env.%s has invalid env name", name, key)
			}
			if containsControlCharacter(value) {
				return fmt.Errorf("services.%s.env.%s must not contain control characters", name, key)
			}
			if SecretBearingServiceEnv(key, value) {
				return fmt.Errorf("services.%s.env.%s must be a literal non-secret value; configure check secrets outside services", name, key)
			}
		}
		if strings.TrimSpace(service.Health.Command) == "" {
			return fmt.Errorf("services.%s.health.command is required", name)
		}
		if strings.Contains(service.Health.Command, "\x00") {
			return fmt.Errorf("services.%s.health.command contains a NUL byte", name)
		}
		if err := validateServiceHealthDuration(service.Health.Timeout); err != nil {
			return fmt.Errorf("services.%s.health.timeout: %w", name, err)
		}
		if err := validateServiceHealthDuration(service.Health.Interval); err != nil {
			return fmt.Errorf("services.%s.health.interval: %w", name, err)
		}
		if len(service.Logs) > preflightapi.MaxRunServiceLogs {
			return fmt.Errorf("service %q logs must contain at most %d entries", name, preflightapi.MaxRunServiceLogs)
		}
		for index, logPath := range service.Logs {
			if err := preflightapi.ValidateRunServiceLogPath(logPath); err != nil {
				return fmt.Errorf("services.%s.logs[%d] %w", name, index, err)
			}
		}
	}
	return nil
}

// ValidateCheckServiceRefs validates a check's service references.
func ValidateCheckServiceRefs(checkName string, refs []string, services map[string]config.ServiceConfig, docker bool) error {
	if len(refs) > preflightapi.MaxRunServices {
		return fmt.Errorf("check %q services must contain at most %d entries", checkName, preflightapi.MaxRunServices)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != ref || ref == "" {
			return fmt.Errorf("check %q services contains an empty or whitespace-padded service reference", checkName)
		}
		if _, ok := services[ref]; !ok {
			return fmt.Errorf("check %q references unknown service %q", checkName, ref)
		}
		if _, ok := seen[ref]; ok {
			return fmt.Errorf("check %q services contains duplicate service %q", checkName, ref)
		}
		seen[ref] = struct{}{}
	}
	if len(refs) > 0 && !docker {
		return fmt.Errorf("check %q uses services and must set docker: true or inherit resources.default.docker: true", checkName)
	}
	return nil
}

// ResolvedServiceSpecs resolves a check's service references to specs.
func ResolvedServiceSpecs(services map[string]config.ServiceConfig, refs []string) []preflightapi.RunServiceSpec {
	resolved := make([]preflightapi.RunServiceSpec, 0, len(refs))
	for _, ref := range refs {
		service, ok := services[ref]
		if !ok {
			continue
		}
		resolved = append(resolved, preflightapi.RunServiceSpec{
			Name:    ref,
			Type:    ResolvedServiceType(service),
			Version: service.Version,
			Image:   service.Image,
			Ports:   append([]string(nil), service.Ports...),
			Env:     copyStringMap(service.Env),
			Health: preflightapi.RunServiceHealthSpec{
				Command:  service.Health.Command,
				Timeout:  service.Health.Timeout,
				Interval: service.Health.Interval,
			},
			Logs: append([]string(nil), service.Logs...),
		})
	}
	return resolved
}
