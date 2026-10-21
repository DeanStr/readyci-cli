package main

import (
	"bytes"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/netpolicy"
	"gopkg.in/yaml.v3"
)

type setupNetworkReview struct {
	PolicySource                string   `json:"policy_source"`
	InferredDependencyAllowlist []string `json:"inferred_dependency_allowlist,omitempty"`
	ExplicitAllowlist           []string `json:"explicit_allowlist,omitempty"`
	Scope                       string   `json:"scope"`
	TestEndpointStatus          string   `json:"test_endpoint_status"`
}

func normalizeSetupNetworkAllowlist(value, profile string) ([]string, error) {
	if profile == "strict" {
		return nil, cliConfigError("--network-allowlist conflicts with --profile strict; choose the remote-ci profile to review explicit network access", nil)
	}
	hosts, err := netpolicy.NormalizeAllowlistEntries(splitCSV(value))
	if err != nil {
		return nil, cliConfigError("--network-allowlist: "+err.Error(), err)
	}
	return hosts, nil
}

// This edits generated configuration only. The override is the complete policy
// for a check, including its preparation and structured steps; it does not add
// access to the inferred dependency hosts or create a second execution path.
func applySetupNetworkPolicy(template string, options setupOptions) (string, error) {
	if len(options.NetworkAllowlist) == 0 {
		return template, nil
	}
	if options.Profile == "strict" {
		return "", cliConfigError("--network-allowlist conflicts with --profile strict", nil)
	}
	hosts, err := netpolicy.NormalizeAllowlistEntries(options.NetworkAllowlist)
	if err != nil {
		return "", cliConfigError("generated network allowlist: "+err.Error(), err)
	}
	// Omitted-scope comments are an exact retained suffix, independent of how
	// the YAML encoder attaches comments to a replaced network mapping.
	suffix := ""
	if at := strings.LastIndex(template, "\n# Ready CI import scope:"); at >= 0 {
		suffix, template = template[at:], template[:at]
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(template), &document); err != nil {
		return "", cliConfigError("cannot apply network policy to generated configuration", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", cliConfigError("generated configuration must be a mapping", nil)
	}
	var network yaml.Node
	if err := network.Encode(struct {
		Mode      string   `yaml:"mode"`
		Allowlist []string `yaml:"allowlist"`
	}{Mode: "allowlist", Allowlist: hosts}); err != nil {
		return "", err
	}
	root := document.Content[0]
	found := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "network" {
			root.Content[i+1] = &network
			found = true
		}
		if root.Content[i].Value == "checks" {
			checks := root.Content[i+1]
			for j := 1; j < len(checks.Content); j += 2 {
				check := checks.Content[j]
				var fields []*yaml.Node
				for k := 0; k+1 < len(check.Content); k += 2 {
					if key := check.Content[k].Value; key != "network_mode" && key != "network_allowlist" {
						fields = append(fields, check.Content[k:k+2]...)
					}
				}
				check.Content = fields
			}
		}
	}
	if !found {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "network"}, &network)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return out.String() + suffix, nil
}

func setupNetworkNextFlags(environment *setupEnvironmentReport) string {
	if environment == nil || environment.NetworkReview == nil || len(environment.NetworkReview.ExplicitAllowlist) == 0 {
		return ""
	}
	return " --network-allowlist " + shellQuoteArg(strings.Join(environment.NetworkReview.ExplicitAllowlist, ","))
}

func describeSetupNetwork(options setupOptions, cfg config.ProjectConfig, report *setupReport, commands []string) *setupNetworkReview {
	policySource := "configuration"
	if report.ProposedConfig != "" || report.configGenerated {
		policySource = "inferred_dependency_hosts"
	}
	review := &setupNetworkReview{PolicySource: policySource, Scope: "entire_check_including_preparation_and_steps", TestEndpointStatus: "not_verified"}
	var runtimes []string
	for _, command := range commands {
		for _, runtime := range setupCommandRuntimes(".", command) {
			runtimes = appendUniqueString(runtimes, runtime)
		}
	}
	if check, ok := cfg.Checks[report.Check]; ok {
		for runtime := range check.RuntimeRequirements {
			runtimes = appendUniqueString(runtimes, runtime)
		}
	}
	review.InferredDependencyAllowlist = detectedDependencyHosts([]detectedCheck{{Runtimes: runtimes}})
	if len(options.NetworkAllowlist) > 0 {
		review.PolicySource = "explicit_setup_override"
		review.ExplicitAllowlist = append([]string(nil), options.NetworkAllowlist...)
	}
	return review
}

func setupNetworkWarning() string {
	return "Inferred dependency destinations do not establish the network access required by tests. Review external test endpoints and use setup --network-allowlist HOSTS to replace the complete generated policy; access applies to preparation and all steps, and remains subject to server policy."
}
