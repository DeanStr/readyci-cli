package main

import "github.com/DeanStr/readyci-cli/pkg/config"

// setupVariantAdmission asks the service whether a fresh worker admits the
// check a generated template defines. known is false without a credential,
// a resolvable check or an admission-policy answer; selection then keeps its
// offline result instead of guessing.
type setupVariantAdmission func(template, check string) (admitted, known bool)

// Each admission is one capability preflight, so the search is bounded.
const setupMaxVariantAdmissions = 8

type setupAdmissionAnswer struct{ admitted, known bool }

func newSetupVariantAdmission(apiURL, apiToken string) setupVariantAdmission {
	answers := map[string]setupAdmissionAnswer{}
	return func(template, check string) (bool, bool) {
		if cached, ok := answers[template]; ok {
			return cached.admitted, cached.known
		}
		answers[template] = setupVariantAdmissionAnswer(apiURL, apiToken, template, check)
		return answers[template].admitted, answers[template].known
	}
}

func setupVariantAdmissionAnswer(apiURL, apiToken, template, check string) (result setupAdmissionAnswer) {
	token, err := effectiveAPIToken(apiURL, apiToken)
	if err != nil {
		return result
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "matrix variant")
	if err != nil {
		return result
	}
	if _, exists := cfg.Checks[check]; !exists && len(cfg.Checks) == 1 {
		check = sortedCheckNames(cfg.Checks)[0]
	}
	plan, err := resolveCheckRunPlan(check, cfg, checkResolveOptions{ProjectID: cfg.ProjectID, RetryCount: -1})
	if err != nil {
		return result
	}
	ctx, stop := signalContext()
	defer stop()
	capability, err := publicWorkerCapability(ctx, apiURL, token, plan)
	// Admission policy applies to every variant alike; only worker capability
	// distinguishes them.
	if err != nil || capability.Policy == nil || !capability.Policy.Allowed {
		return result
	}
	result.admitted, result.known = capability.Claimable, true
	return result
}
