package main

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var setupAliasSeriesPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
var setupAliasDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Resolve lazily, only when a workflow actually uses a moving alias. A setup
// invocation shares the result across detection passes. No bearer or source
// files are sent, and an unavailable service never falls back to stale policy.
func setupAPIRuntimeAliasResolver(apiURL string) func(preflightapi.RuntimeAliasRequest, time.Time) (preflightapi.RuntimeResolution, error) {
	type outcome struct {
		result preflightapi.RuntimeResolution
		err    error
	}
	cache := map[preflightapi.RuntimeAliasRequest]outcome{}
	return func(request preflightapi.RuntimeAliasRequest, _ time.Time) (preflightapi.RuntimeResolution, error) {
		if prior, ok := cache[request]; ok {
			return prior.result, prior.err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result, err := api.NewClient(apiURL).ResolveRuntimeAlias(ctx, request)
		if err == nil && (result.Selector != request.Selector || result.Policy != preflightapi.RuntimeResolutionPolicySetupNodeLTSImport || !setupAliasSeriesPattern.MatchString(result.Requirement) || !setupAliasDigestPattern.MatchString(result.CatalogDigest)) {
			err = fmt.Errorf("service returned an invalid runtime alias resolution")
		}
		cache[request] = outcome{result, err}
		return result, err
	}
}
