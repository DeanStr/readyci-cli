package main

import (
	"flag"
	"os"
)

func doctorCommand(args []string) error {
	if len(args) > 0 && args[0] == "coverage" {
		return doctorCoverageCommand(args[1:])
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	adminToken := adminOnlyTokenFlag(fs, "optional admin token for worker checks")
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to validate against worker capabilities")
	repoChecks := fs.Bool("repo", false, "run repository cross-file verification policy checks without contacting the API")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci doctor [--repo] [--api-url URL] [--token TOKEN] [--admin-token TOKEN] [--project PROJECT] [--check NAME] [--json]", nil)
	}

	ctx, stop := signalContext()
	defer stop()

	report := buildDoctorReport(ctx, doctorOptions{
		APIURL:     *apiURL,
		APIToken:   *apiToken,
		AdminToken: *adminToken,
		ProjectID:  *projectID,
		CheckName:  *checkName,
		Repo:       *repoChecks,
	})
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
	} else {
		printDoctorReport(report)
	}
	if report.Summary.Failures > 0 {
		return exitCodeError{code: 1}
	}
	return nil
}
