package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Guided setup uses the same configuration and server-owned execution path as
// explicit setup/check commands. Consent precedes writes and source upload.
func guidedSetup(options setupOptions, input io.Reader, output io.Writer, yes bool) error {
	reader := bufio.NewReader(input)
	state, err := readSetupConfigState()
	if err != nil {
		return err
	}
	report, err := buildSetupReport(options)
	if err != nil {
		return err
	}
	if report.configError != nil && !options.Force {
		return report.configError
	}
	if report.authError != nil {
		return report.authError
	}
	if err := state.verify(); err != nil {
		return err
	}
	if report.AuthStatus == "missing" {
		if yes {
			return cliAuthMissingError("sign in with readyci login or set READYCI_API_TOKEN before using setup --yes", nil)
		}
		approved, err := confirmSetup(reader, output, "Sign in using your browser?")
		if err != nil || !approved {
			return err
		}
		if err := loginCommand([]string{"--api-url", options.APIURL}); err != nil {
			return err
		}
		report, err = buildSetupReport(options)
		if err != nil {
			return err
		}
		if report.authError != nil {
			return report.authError
		}
		if report.AuthStatus != "ok" {
			return cliAuthMissingError("sign-in did not provide a Ready CI credential", nil)
		}
		if err := state.verify(); err != nil {
			return err
		}
	}
	if report.ConfigStatus == "missing" || options.Force {
		var imported *repoImportCandidate
		if options.Detect {
			imported, err = selectSetupImport(options, report.ImportCandidates, reader, output, yes)
			if err != nil {
				return err
			}
		}
		template := remoteCIProjectTemplateWithProfile(report.ProjectID, options.Image, options.Profile)
		if imported != nil {
			template = setupImportedTemplateWithScope(".", options, report.ProjectID, *imported, report.scopeCandidates)
			options.ImportScope = selectedSetupImportScope(*imported, report.scopeCandidates)
		} else if options.Detect {
			template, err = setupDetectedTemplate(".", report.ProjectID, options, report.ImportCandidates)
			if err != nil {
				return err
			}
		}
		template, err = applySetupNetworkPolicy(template, options)
		if err != nil {
			return err
		}
		cfg, err := config.ParseProjectConfig([]byte(template), "starter configuration")
		if err != nil {
			return err
		}
		if err := validateProjectConfig(cfg); err != nil {
			return err
		}
		if err := validateGuidedSetupCheck(options.CheckName, sortedCheckNames(cfg.Checks)); err != nil {
			return err
		}
		fmt.Fprintf(output, "\nConfiguration for project %s:\n%s\n", report.ProjectID, template)
		question := "Save this configuration?"
		if options.Force && report.ConfigStatus != "missing" {
			question = "Replace the existing configuration with this configuration?"
		}
		if !yes {
			approved, err := confirmSetup(reader, output, question)
			if err != nil || !approved {
				return err
			}
		}
		if err := state.verify(); err != nil {
			return err
		}
		options.ProjectID = report.ProjectID
		options.ConfigTemplate = &template
		options.WriteConfig = true
		report, err = buildSetupReport(options)
		if err != nil {
			return err
		}
		if report.authError != nil {
			return report.authError
		}
	} else if err := validateGuidedSetupCheck(options.CheckName, report.Checks); err != nil {
		return err
	}
	state, err = readSetupConfigState()
	if err != nil {
		return err
	}
	printSetupReport(report)
	if err := setupCheck(options, report, true); err != nil {
		return err
	}
	if !yes {
		approved, err := confirmSetup(reader, output, "Run this check remotely?")
		if err != nil || !approved {
			return err
		}
	}
	if err := state.verify(); err != nil {
		return err
	}
	if err := setupCheck(options, report, false); err != nil {
		return err
	}
	fmt.Fprintln(output, "Next time, run readyci check.")
	return nil
}

func confirmSetup(reader *bufio.Reader, output io.Writer, question string) (bool, error) {
	for {
		fmt.Fprintf(output, "%s [Y/n] ", question)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return false, cliConfigError("setup input ended before confirmation; use --yes for automation", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			fmt.Fprintln(output, "Setup stopped. Run readyci setup when you're ready to continue.")
			return false, nil
		default:
			fmt.Fprintln(output, "Enter yes or no.")
		}
	}
}

func setupCheck(options setupOptions, report setupReport, dryRun bool) error {
	if err := validateSetupEnvironment(report.Environment); err != nil {
		return err
	}
	if len(report.Checks) == 0 {
		return cliConfigError("configuration required: add the repository's documented test command to .readyci.yml before running setup", nil)
	}
	args := []string{"--api-url", options.APIURL, "--project", report.ProjectID}
	if options.APIToken != "" {
		args = append(args, "--token", options.APIToken)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if report.Check != "" {
		args = append(args, report.Check)
	}
	return checkCommand(args)
}

func validateGuidedSetupCheck(name string, checks []string) error {
	if len(checks) == 0 {
		return cliConfigError("configuration required: no runnable test check was selected; configure the repository's documented tests before saving or running setup", nil)
	}
	name = strings.TrimSpace(name)
	if name != "" && !containsString(checks, name) {
		return cliConfigError(fmt.Sprintf("unknown check %q (available: %s)", name, strings.Join(checks, ", ")), nil)
	}
	return nil
}
