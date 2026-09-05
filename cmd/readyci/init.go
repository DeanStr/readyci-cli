package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

const remoteCIToolchainImage = "ubuntu-24.04-ci-docker"

func initCommand(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "remote-ci", "project template profile")
	image := fs.String("image", "", "runner image to pin in the generated config")
	detect := fs.Bool("detect", false, "keep local generation conservative; use plan --recommend --suggest-config for hosted smart detection")
	printConfig := fs.Bool("print", false, "print the generated config instead of writing it")
	force := fs.Bool("force", false, "overwrite an existing Ready CI config")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci init [--profile remote-ci|strict] [--image IMAGE] [--detect] [--print] [--force]", nil)
	}
	if *profile != "remote-ci" && *profile != "strict" {
		return cliConfigError(fmt.Sprintf("unknown init profile %q", *profile), nil)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	template := remoteCIProjectTemplateWithProfile(inferProjectID(cwd), *image, *profile)
	if *detect {
		fmt.Fprintln(os.Stderr, "readyci: local init --detect now emits the conservative starter; run readyci plan --recommend --suggest-config for hosted smart config suggestions")
		template = detectedProjectTemplateWithProfile(cwd, inferProjectID(cwd), *image, *profile)
	}
	if *printConfig {
		fmt.Fprint(os.Stdout, template)
		return nil
	}
	path, err := initConfigPath(*force)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "wrote %s\n", path)
	fmt.Fprintln(os.Stdout, "next: readyci config validate")
	if cfg, _, err := config.LoadProjectConfig("."); err == nil {
		if check := preferredSetupCheck(sortedCheckNames(cfg.Checks)); check != "" {
			fmt.Fprintf(os.Stdout, "next: readyci doctor --check %s\n", shellQuoteArg(check))
			fmt.Fprintf(os.Stdout, "next: readyci check --dry-run %s\n", shellQuoteArg(check))
		}
	}
	if strings.TrimSpace(*image) == "" {
		fmt.Fprintf(os.Stdout, "hint: Makefile-oriented checks expect project toolchains; uncomment the image line or rerun with --force --image %s once your worker advertises it\n", remoteCIToolchainImage)
	}
	return nil
}

func initConfigPath(force bool) (string, error) {
	for _, existingPath := range []string{".readyci.yml", ".readyci.yaml"} {
		if _, err := os.Stat(existingPath); err == nil {
			if force {
				return existingPath, nil
			}
			return "", fmt.Errorf("%s already exists; use --force to overwrite", existingPath)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return ".readyci.yml", nil
}

func inferProjectID(cwd string) string {
	base := strings.ToLower(filepath.Base(cwd))
	var out strings.Builder
	lastDash := false
	for _, r := range base {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.'
		if allowed {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(out.String(), "-.")
	if value == "" {
		return "proj_dev"
	}
	return value
}
