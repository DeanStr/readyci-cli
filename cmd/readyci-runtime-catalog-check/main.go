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
	if minimumValidity < 0 {
		return fmt.Errorf("minimum validity must be non-negative")
	}
	if _, err := preflightapi.ResolveRuntimeAlias("node", "lts/*", now); err != nil {
		return err
	}
	catalog := preflightapi.NodeRuntimeAliasCatalogMetadata()
	if !now.Add(minimumValidity).Before(catalog.ValidBefore) {
		return fmt.Errorf("Node alias import catalog expires at %s, within %s; review a fresh official snapshot and publish an updated CLI for new alias imports", catalog.ValidBefore.Format(time.RFC3339), minimumValidity)
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
