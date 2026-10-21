package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var setupNumericRuntimePin = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,2}$`)

func parseSetupRuntimePins(values []string) (map[string]string, error) {
	var pins map[string]string
	for _, value := range values {
		runtime, version, ok := strings.Cut(value, "=")
		if !ok || !setupNumericRuntimePin.MatchString(version) {
			return nil, cliConfigError("--runtime requires NAME=VERSION with a numeric version or series, such as node=24 or python=3.12", nil)
		}
		if pins[runtime] != "" {
			return nil, cliConfigError("--runtime cannot select "+runtime+" more than once", nil)
		}
		// Match the runtime actions whose declarations setup can inspect.
		normalized, err := preflightapi.NormalizeSetupRuntimeRequirement(runtime, version)
		if err != nil {
			return nil, cliConfigError("--runtime: "+err.Error(), err)
		}
		if pins == nil {
			pins = map[string]string{}
		}
		pins[runtime] = normalized
	}
	return pins, nil
}

func setupUserRuntimePinNotices(pins map[string]string) []string {
	var notices []string
	for _, runtime := range sortedStringMapKeys(pins) {
		notices = append(notices, fmt.Sprintf("User-selected %s %s via --runtime; source compatibility constraints still apply. The saved check does not track moving runtime selectors.", runtime, pins[runtime]))
	}
	return notices
}

func setupRuntimePinNextFlags(pins map[string]string) string {
	var flags string
	for _, runtime := range sortedStringMapKeys(pins) {
		flags += " --runtime " + shellQuoteArg(runtime+"="+pins[runtime])
	}
	return flags
}
