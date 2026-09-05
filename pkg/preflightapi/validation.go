package preflightapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ValidateRunServices(services []RunServiceSpec, docker bool) error {
	if len(services) > MaxRunServices {
		return fmt.Errorf("services must contain at most %d entries", MaxRunServices)
	}
	seen := make(map[string]struct{}, len(services))
	seenEnvTokens := make(map[string]string, len(services))
	for _, service := range services {
		name := strings.TrimSpace(service.Name)
		if name != service.Name || !ValidRunServiceName(name) {
			return fmt.Errorf("service %q has invalid name", service.Name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("services contains duplicate service %q", name)
		}
		seen[name] = struct{}{}
		envToken := RunServiceEnvToken(name)
		if existing, ok := seenEnvTokens[envToken]; ok {
			return fmt.Errorf("services %q and %q produce the same service environment variable prefix", existing, name)
		}
		seenEnvTokens[envToken] = name
		serviceType := strings.TrimSpace(service.Type)
		if serviceType != service.Type {
			return fmt.Errorf("service %q type must not contain surrounding whitespace", name)
		}
		switch serviceType {
		case "postgres":
			if strings.TrimSpace(service.Image) != "" {
				return fmt.Errorf("service %q image is only supported for container services", name)
			}
			if len(service.Ports) > 0 {
				return fmt.Errorf("service %q ports are only supported for container services", name)
			}
			switch service.Version {
			case "", "15", "16", "17":
			default:
				return fmt.Errorf("service %q version must be one of 15, 16, 17", name)
			}
		case "container":
			if strings.TrimSpace(service.Version) != "" {
				return fmt.Errorf("service %q version is only supported for postgres services", name)
			}
			if !ValidRunServiceImage(service.Image) {
				return fmt.Errorf("service %q image is invalid", name)
			}
			if len(service.Ports) > MaxRunServicePorts {
				return fmt.Errorf("service %q ports must contain at most %d entries", name, MaxRunServicePorts)
			}
			seenPorts := make(map[string]struct{}, len(service.Ports))
			for _, port := range service.Ports {
				if err := ValidateRunServicePort(port); err != nil {
					return fmt.Errorf("service %q port %q is invalid: %w", name, port, err)
				}
				if _, ok := seenPorts[port]; ok {
					return fmt.Errorf("service %q contains duplicate port %q", name, port)
				}
				seenPorts[port] = struct{}{}
			}
		default:
			return fmt.Errorf("service %q type must be postgres or container", name)
		}
		if len(service.Env) > MaxRunServiceEnvVars {
			return fmt.Errorf("service %q env must contain at most %d entries", name, MaxRunServiceEnvVars)
		}
		for key, value := range service.Env {
			if !ValidRunServiceEnvKey(key) {
				return fmt.Errorf("service %q env key %q is invalid", name, key)
			}
			if containsControlCharacter(value) {
				return fmt.Errorf("service %q env %q must not contain control characters", name, key)
			}
			if SecretBearingRunServiceEnv(key, value) {
				return fmt.Errorf("service %q env %q must be a literal non-secret value", name, key)
			}
		}
		if strings.TrimSpace(service.Health.Command) == "" {
			return fmt.Errorf("service %q health.command is required", name)
		}
		if containsNULByte(service.Health.Command) {
			return fmt.Errorf("service %q health.command must not contain a NUL byte", name)
		}
		if err := validateOptionalPositiveDuration("health.timeout", service.Health.Timeout); err != nil {
			return fmt.Errorf("service %q %w", name, err)
		}
		if err := validateOptionalPositiveDuration("health.interval", service.Health.Interval); err != nil {
			return fmt.Errorf("service %q %w", name, err)
		}
		if len(service.Logs) > MaxRunServiceLogs {
			return fmt.Errorf("service %q logs must contain at most %d entries", name, MaxRunServiceLogs)
		}
		for index, logPath := range service.Logs {
			if err := ValidateRunServiceLogPath(logPath); err != nil {
				return fmt.Errorf("service %q logs[%d] %w", name, index, err)
			}
		}
	}
	if len(services) > 0 && !docker {
		return fmt.Errorf("services require docker")
	}
	return nil
}

func ValidateRunServiceLogPath(logPath string) error {
	if strings.TrimSpace(logPath) == "" {
		return fmt.Errorf("cannot be empty")
	}
	if containsControlCharacter(logPath) {
		return fmt.Errorf("must not contain control characters")
	}
	return nil
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func containsNULByte(value string) bool {
	return strings.Contains(value, "\x00")
}

func ValidRunServiceImage(image string) bool {
	trimmed := strings.TrimSpace(image)
	if image != trimmed {
		return false
	}
	image = trimmed
	if image == "" || len(image) > 255 || strings.HasPrefix(image, "-") || strings.Contains(image, "://") {
		return false
	}
	if image[0] == '/' || image[len(image)-1] == '/' || strings.Contains(image, "//") {
		return false
	}
	for _, r := range image {
		switch {
		case r >= 'a' && r <= 'z':
			continue
		case r >= 'A' && r <= 'Z':
			continue
		case r >= '0' && r <= '9':
			continue
		case r == '.', r == '_', r == '-', r == '/', r == ':', r == '@', r == '+':
			continue
		default:
			return false
		}
	}
	if imageReferenceNameContainsUppercase(image) {
		return false
	}
	if !validImageReferenceShape(image) {
		return false
	}
	return true
}

func validImageReferenceShape(image string) bool {
	if strings.Count(image, "@") > 1 {
		return false
	}
	name := image
	if beforeDigest, digest, ok := strings.Cut(image, "@"); ok {
		if beforeDigest == "" || digest == "" {
			return false
		}
		if algorithm, encoded, ok := strings.Cut(digest, ":"); ok && (algorithm == "" || encoded == "") {
			return false
		}
		name = beforeDigest
	}
	lastSlash := strings.LastIndex(name, "/")
	lastColon := strings.LastIndex(name, ":")
	if lastColon <= lastSlash {
		return true
	}
	if strings.Count(name[lastSlash+1:], ":") > 1 {
		return false
	}
	return lastColon < len(name)-1
}

func imageReferenceNameContainsUppercase(image string) bool {
	name := image
	if beforeDigest, _, ok := strings.Cut(name, "@"); ok {
		name = beforeDigest
	}
	lastSlash := strings.LastIndex(name, "/")
	lastColon := strings.LastIndex(name, ":")
	if lastColon > lastSlash {
		name = name[:lastColon]
	}
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func ValidateRunServicePort(port string) error {
	if strings.TrimSpace(port) != port || port == "" {
		return fmt.Errorf("must be PORT/tcp or PORT/udp")
	}
	numberText, proto, ok := strings.Cut(port, "/")
	if !ok || numberText == "" || proto == "" {
		return fmt.Errorf("must be PORT/tcp or PORT/udp")
	}
	switch proto {
	case "tcp", "udp":
	default:
		return fmt.Errorf("protocol must be tcp or udp")
	}
	if !validRunServicePortNumberText(numberText) {
		return fmt.Errorf("port number must be canonical decimal digits between 1 and 65535")
	}
	number, err := strconv.Atoi(numberText)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("port number must be between 1 and 65535")
	}
	return nil
}

func validRunServicePortNumberText(numberText string) bool {
	if numberText == "" || numberText == "0" {
		return false
	}
	if len(numberText) > 1 && numberText[0] == '0' {
		return false
	}
	for _, r := range numberText {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ValidateRunSteps(steps []RunStepSpec) error {
	if len(steps) > MaxRunSteps {
		return fmt.Errorf("steps must contain at most %d entries", MaxRunSteps)
	}
	seen := make(map[string]struct{}, len(steps))
	for index, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return fmt.Errorf("steps[%d].name is required", index)
		}
		if name != step.Name || !ValidRunStepName(name) {
			return fmt.Errorf("steps[%d].name %q is invalid", index, step.Name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("steps contains duplicate step %q", name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(step.Command) == "" {
			return fmt.Errorf("steps[%d].command is required", index)
		}
		if containsNULByte(step.Command) {
			return fmt.Errorf("steps[%d].command must not contain a NUL byte", index)
		}
		if step.TimeoutSeconds < 0 {
			return fmt.Errorf("steps[%d].timeout_seconds must be non-negative", index)
		}
		if len(step.Env) > MaxRunStepEnvVars {
			return fmt.Errorf("steps[%d].env must contain at most %d entries", index, MaxRunStepEnvVars)
		}
		for key, value := range step.Env {
			if !ValidRunServiceEnvKey(key) {
				return fmt.Errorf("steps[%d].env key %q is invalid", index, key)
			}
			if containsControlCharacter(value) {
				return fmt.Errorf("steps[%d].env %q must not contain control characters", index, key)
			}
			if SecretBearingRunServiceEnv(key, value) {
				return fmt.Errorf("steps[%d].env %q must be a literal non-secret value", index, key)
			}
		}
	}
	return nil
}

func ValidRunStepName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func ValidRunServiceName(name string) bool {
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

func RunServiceEnvToken(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "SERVICE"
	}
	return b.String()
}

func validateOptionalPositiveDuration(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fmt.Errorf("%s must be a positive duration", label)
	}
	return nil
}
