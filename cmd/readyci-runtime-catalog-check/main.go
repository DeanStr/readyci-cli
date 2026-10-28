// readyci-runtime-catalog-check is a maintainer gate for new alias imports.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func checkCatalog(now time.Time, minimumValidity time.Duration) error {
	catalog := preflightapi.NodeRuntimeAliasCatalogMetadata()
	if err := checkCatalogWindow(now, minimumValidity, catalog.ReviewedAt, catalog.ValidBefore); err != nil {
		return err
	}
	if _, err := preflightapi.ResolveRuntimeAlias("node", "lts/*", now); err != nil {
		return err
	}
	return nil
}

func checkCatalogWindow(now time.Time, minimumValidity time.Duration, reviewedAt, validBefore time.Time) error {
	if minimumValidity < 0 {
		return fmt.Errorf("minimum validity must be non-negative")
	}
	if !reviewedAt.Before(validBefore) || now.Before(reviewedAt) || !now.Before(validBefore) {
		return fmt.Errorf("reviewed Node alias catalog is not valid at %s (valid from %s before %s); update the service import catalog or choose an explicit numeric runtime", now.UTC().Format(time.RFC3339), reviewedAt.Format(time.RFC3339), validBefore.Format(time.RFC3339))
	}
	if !now.Add(minimumValidity).Before(validBefore) {
		return fmt.Errorf("Node alias import catalog expires at %s, within %s; review a fresh official snapshot and deploy the reviewed service catalog for new alias imports", validBefore.Format(time.RFC3339), minimumValidity)
	}
	return nil
}

func main() {
	minimumValidity := flag.Duration("min-validity", 48*time.Hour, "required remaining validity for new alias imports")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: readyci-runtime-catalog-check [--min-validity 48h]")
		os.Exit(2)
	}
	if err := checkCatalog(time.Now(), *minimumValidity); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("node_alias_catalog=%s valid_before=%s\n", preflightapi.NodeRuntimeAliasCatalogDigest(), preflightapi.NodeRuntimeAliasCatalogMetadata().ValidBefore.Format(time.RFC3339))
}
