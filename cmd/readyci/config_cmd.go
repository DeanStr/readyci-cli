package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func configCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci config <validate|explain>", nil)
	}
	switch args[0] {
	case "validate":
		return configValidateCommand(args[1:])
	case "explain":
		return configExplainCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown config command %q", args[0]), nil)
	}
}

func configValidateCommand(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci config validate", nil)
	}
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if path == "" {
		return cliConfigError("no .readyci.yml found", nil)
	}
	if err := validateProjectConfigForConfigPath(cfg, path); err != nil {
		return err
	}
	fmt.Printf("config: %s\n", path)
	fmt.Printf("project_id: %s\n", defaultString(cfg.ProjectID, "proj_dev"))
	fmt.Printf("region: %s\n", preflightapi.NormalizeRegion(cfg.Region))
	fmt.Printf("checks: %d\n", len(cfg.Checks))
	fmt.Println("status: ok")
	return nil
}

func configExplainCommand(args []string) error {
	fs := flag.NewFlagSet("config explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	fields := fs.Bool("fields", false, "include config field reference")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci config explain [--json] [--fields]", nil)
	}
	cfg, path, err := config.LoadProjectConfig(".")
	if err != nil {
		return err
	}
	if path == "" {
		return cliConfigError("no .readyci.yml found", nil)
	}
	report := buildConfigExplainReport(cfg, path)
	if *fields {
		report.FieldHelp = config.ConfigFieldMetadata()
	}
	if *jsonOutput {
		if err := writeJSONOutput(report); err != nil {
			return err
		}
		if report.Status != "ok" {
			return exitCodeError{code: 1}
		}
		return nil
	}
	printConfigExplainReport(report)
	if report.Status != "ok" {
		return exitCodeError{code: 1}
	}
	return nil
}
