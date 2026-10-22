package main

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/verificationplan"
)

type matchedJobFixture struct {
	Name                     string             `json:"name"`
	Repository               string             `json:"repository"`
	Commit                   string             `json:"commit"`
	Workflow                 string             `json:"workflow"`
	Job                      string             `json:"job"`
	Matrix                   map[string]any     `json:"matrix"`
	CandidateID              string             `json:"candidate_id"`
	DetectedTestEntrypoint   bool               `json:"detected_test_entrypoint"`
	Check                    string             `json:"check"`
	TestStep                 string             `json:"test_step"`
	ReviewedNetworkAllowlist []string           `json:"reviewed_network_allowlist"`
	OmittedCandidates        []string           `json:"omitted_candidates"`
	ManualConfigSHA256       string             `json:"manual_config_sha256"`
	SealedCommandSHA256      string             `json:"sealed_command_sha256"`
	SealedPolicySHA256       string             `json:"sealed_policy_sha256"`
	NativeProofSHA256        string             `json:"native_proof_sha256"`
	Sources                  []matchedJobSource `json:"sources"`
}

type matchedJobSource struct {
	Path        string `json:"path"`
	GitBlobSHA1 string `json:"git_blob_sha1"`
	SHA256      string `json:"sha256"`
	SourceURL   string `json:"source_url"`
}

// These fixtures are promoted only after independently authored manual and
// generated configurations run the complete selected upstream job successfully.
// The offline regression checks the real setup command, the resolved job and
// the compiled, sealed command; it does not run upstream code during detection.
// Alias fixtures retain their historical native policy. Current imports
// deliberately replace that policy with an explicit numeric pin; the native
// proof establishes the historical job, not a native run of the new policy.
// Likewise, historical sealed commands do not attest to today's cache wrapper;
// current manual and automatic commands are compiled and compared separately.
func TestSetupMatchedWholeJobAcceptance(t *testing.T) {
	// New imports use the current reviewed catalog; native proof retains its
	// original catalog. Refreshes within the same series do not rewrite proof.
	withReviewedAliasClock(t, preflightapi.NodeRuntimeAliasCatalogMetadata().ReviewedAt.Add(time.Minute))
	root, err := filepath.Abs("testdata/setup-matched-jobs")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []matchedJobFixture
	matchedJobReadJSON(t, filepath.Join(root, "provenance.json"), &fixtures)
	if len(fixtures) == 0 {
		t.Fatal("no matched whole-job fixtures")
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			fixtureRoot := filepath.Join(root, fixture.Name)
			repo := chdirCLITestRepo(t, fixture.Name)
			for _, source := range fixture.Sources {
				data := matchedJobRead(t, filepath.Join(fixtureRoot, "input", source.Path))
				blob := append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...)
				if source.SourceURL != "https://raw.githubusercontent.com/"+fixture.Repository+"/"+fixture.Commit+"/"+source.Path ||
					fmt.Sprintf("%x", sha1.Sum(blob)) != source.GitBlobSHA1 || matchedJobSHA256(data) != source.SHA256 {
					t.Fatalf("pinned public source changed: %s", source.Path)
				}
				writeFileForRepoMapTest(t, filepath.Join(repo, source.Path), string(data))
			}

			// Assert the concrete upstream matrix cell independently of generated
			// comments. An explicit selection must not expand or replace siblings.
			var selected *repoImportCandidate
			for _, candidate := range setupImportCandidates(repo, "github-actions") {
				if candidate.ID == fixture.CandidateID {
					copy := candidate
					selected = &copy
				}
			}
			var matrix map[string]string
			if len(fixture.Matrix) > 0 {
				matrix = map[string]string{}
				for key, value := range fixture.Matrix {
					matrix[key] = fmt.Sprint(value)
				}
			}
			parentID := "github-actions:" + fixture.Workflow + ":" + fixture.Job
			if selected == nil || selected.TestEntrypoint != fixture.DetectedTestEntrypoint || len(selected.Unsupported) > 0 || !reflect.DeepEqual(selected.Matrix, matrix) ||
				(selected.ParentJobID != "" && selected.ParentJobID != parentID) || (selected.ParentJobID == "" && selected.ID != parentID) {
				t.Fatalf("selected upstream cell changed: %+v", selected)
			}

			printed, err := captureStdoutForCLITest(t, func() error {
				return setupCommand([]string{"--print", "--project", "matched-fixture", "--candidate", fixture.CandidateID,
					"--image", "ubuntu-24.04-ci", "--network-allowlist", strings.Join(fixture.ReviewedNetworkAllowlist, ",")})
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(".readyci.yml"); !os.IsNotExist(err) {
				t.Fatal("setup preview wrote a configuration")
			}
			var omitted []string
			for _, line := range strings.Split(printed, "\n") {
				if strings.HasPrefix(line, "# Not imported: ") {
					var id string
					if _, err := fmt.Sscanf(strings.TrimPrefix(line, "# Not imported: "), "%q", &id); err != nil {
						t.Fatal(err)
					}
					omitted = append(omitted, id)
				}
			}
			if !reflect.DeepEqual(omitted, fixture.OmittedCandidates) ||
				!strings.Contains(printed, "# Selected: "+fmt.Sprintf("%q", fixture.CandidateID)) {
				t.Fatalf("selected/omitted scope changed: %v", omitted)
			}

			manualData := matchedJobRead(t, filepath.Join(fixtureRoot, "manual.readyci.yml"))
			if matchedJobSHA256(manualData) != fixture.ManualConfigSHA256 {
				t.Fatal("independent manual oracle changed")
			}
			manualNumeric, manualResolutions := matchedJobNumericOracle(t, manualData, fixture.Check)
			manual := matchedJobResolve(t, manualNumeric, fixture.Check)
			automatic := matchedJobResolve(t, []byte(printed), fixture.Check)
			// Only API-defined defaults and unordered network hosts are
			// canonicalized. Every other resolved field must match, including
			// all commands, step env, preparation, sync scope and cache paths.
			manualEffective, automaticEffective := manual, automatic
			matchedJobEffectiveDefaults(&manualEffective.Run)
			matchedJobEffectiveDefaults(&automaticEffective.Run)
			if len(manualResolutions) > 0 {
				if len(manualResolutions) != len(selected.RuntimeResolutions) || !reflect.DeepEqual(manual.Run.RuntimeRequirements, automatic.Run.RuntimeRequirements) {
					t.Fatal("alias import must change only the historical alias policy to a disclosed numeric pin")
				}
				for name, historical := range manualResolutions {
					current := selected.RuntimeResolutions[name]
					if current.Selector != historical.Selector || current.Policy != preflightapi.RuntimeResolutionPolicySetupNodeLTSImport || historical.Policy != "setup_node_lts_cache_first_v1" || current.Requirement != historical.Requirement {
						t.Fatal("alias series changed; review the new execution policy independently of historical native proof")
					}
				}
			}
			if !reflect.DeepEqual(automaticEffective, manualEffective) {
				want, _ := json.MarshalIndent(manualEffective, "", "  ")
				got, _ := json.MarshalIndent(automaticEffective, "", "  ")
				t.Fatalf("generated whole job differs from independent manual oracle\nwant %s\ngot %s", want, got)
			}
			if automatic.Retries != 0 || len(automatic.Variants) != 1 || automatic.Run.Command != "" || len(automatic.Run.Steps) == 0 {
				t.Fatal("whole selected job was narrowed, retried or expanded")
			}
			if automatic.Run.IncludeUntracked == nil {
				t.Fatal("selected source inclusion policy disappeared")
			}
			wantHosts := append([]string(nil), fixture.ReviewedNetworkAllowlist...)
			sort.Strings(wantHosts)
			if automaticEffective.Run.NetworkMode != "allowlist" || !reflect.DeepEqual(automaticEffective.Run.NetworkAllowlist, wantHosts) {
				t.Fatal("reviewed network override was widened or replaced")
			}

			sealed := matchedJobSeal(t, automatic, fixture)
			manualSealed := matchedJobSeal(t, manual, fixture)
			if len(sealed.Plan.Nodes) != 1 || len(sealed.Plan.MatrixGroups) != 0 || sealed.Plan.Nodes[0].Retry.MaxAttempts != 1 ||
				sealed.Plan.Nodes[0].ID != fixture.Check || sealed.Plan.Nodes[0].Name != fixture.Check || sealed.Plan.Nodes[0].Kind != "check" ||
				sealed.Plan.Nodes[0].Phase != "verify" || sealed.Plan.Nodes[0].Condition != "always" || !sealed.Plan.Nodes[0].Required ||
				len(sealed.Plan.Nodes[0].DependsOn) != 0 || len(sealed.Plan.Nodes[0].SuccessDependsOn) != 0 {
				t.Fatal("sealing changed the single full selected job")
			}
			historicalCommand := matchedJobRead(t, filepath.Join(fixtureRoot, "sealed-command.sh"))
			if matchedJobSHA256(historicalCommand) != fixture.SealedCommandSHA256 {
				t.Fatal("historical native sealed command changed")
			}
			if len(manualSealed.Plan.Nodes) != 1 || len(manualSealed.Plan.MatrixGroups) != 0 ||
				sealed.Plan.Nodes[0].Run.Command == "" || sealed.Plan.Nodes[0].Run.Command != manualSealed.Plan.Nodes[0].Run.Command ||
				len(sealed.Plan.Nodes[0].Run.Steps) != 0 || len(manualSealed.Plan.Nodes[0].Run.Steps) != 0 {
				t.Fatal("current automatic sealed command differs from the independently compiled manual whole job")
			}
			// The native manual job also supplies the independent effective
			// policy oracle after sealing. Transport/source identities vary per
			// execution; retain every execution policy field and reject extras.
			policyData := matchedJobRead(t, filepath.Join(fixtureRoot, "sealed-policy.json"))
			if matchedJobSHA256(policyData) != fixture.SealedPolicySHA256 {
				t.Fatal("native execution policy oracle changed")
			}
			var wantPolicy map[string]any
			if err := json.Unmarshal(policyData, &wantPolicy); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(sealed.Plan.Nodes[0].Run)
			if err != nil {
				t.Fatal(err)
			}
			var gotPolicy map[string]any
			if err := json.Unmarshal(encoded, &gotPolicy); err != nil {
				t.Fatal(err)
			}
			for _, identity := range []string{"project_id", "client", "git", "bundle_baseline_key", "command"} {
				delete(gotPolicy, identity)
			}
			if _, exists := gotPolicy["secrets_requested"]; !exists {
				gotPolicy["secrets_requested"] = false
			}
			if region, exists := gotPolicy["region"]; !exists || region == "" {
				gotPolicy["region"] = preflightapi.DefaultRegion
			}
			if _, exists := gotPolicy["cache_lane_size_bytes"]; !exists && gotPolicy["cache_mode"] == "cache-only" {
				gotPolicy["cache_lane_size_bytes"] = float64(preflightapi.DefaultCacheLaneSizeBytes)
			}
			if len(manualResolutions) > 0 {
				// Keep the recorded proof and its hash immutable. Assert its
				// alias policy before applying the intentional numeric-pin change.
				recorded, _ := json.Marshal(wantPolicy["runtime_resolutions"])
				var historical map[string]preflightapi.RuntimeResolution
				if err := json.Unmarshal(recorded, &historical); err != nil || !reflect.DeepEqual(historical, manualResolutions) {
					t.Fatal("historical native alias policy changed")
				}
				delete(wantPolicy, "runtime_resolutions")
			}
			if !reflect.DeepEqual(gotPolicy, wantPolicy) {
				t.Fatalf("sealed execution policy differs from successful native manual job\nwant %v\ngot %v", wantPolicy, gotPolicy)
			}
			matchedJobAssertNativeProof(t, fixtureRoot, fixture)
		})
	}
}

func matchedJobSeal(t *testing.T, plan verificationplan.CheckPlan, fixture matchedJobFixture) preflightapi.CreateVerificationSessionRequest {
	t.Helper()
	if plan.Run.IncludeUntracked == nil {
		t.Fatal("selected source inclusion policy disappeared")
	}
	nodes, groups, err := verificationplan.NodesForPlans(nil, []verificationplan.CheckPlan{plan}, verificationplan.BundleIdentity{}, verificationplan.ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := preflightapi.NormalizeCreateVerificationSessionRequest(preflightapi.CreateVerificationSessionRequest{
		ProjectID: "matched-fixture", IdempotencyKey: "matched-whole-job",
		Recipe: preflightapi.VerificationRecipe{SchemaVersion: preflightapi.VerificationRecipeSchemaVersion,
			Purpose: preflightapi.ExecutionPurpose, Check: fixture.Check, ChangeMode: "working",
			Source: &preflightapi.VerificationSourceRecipe{IncludeUntracked: *plan.Run.IncludeUntracked, ExcludePatterns: plan.Run.SyncExclude}},
		Snapshot: preflightapi.VerificationSnapshot{Digest: "sha256:" + strings.Repeat("a", 64), GitHead: fixture.Commit, ChangeMode: "working"},
		Plan:     preflightapi.VerificationPlan{Nodes: nodes, MatrixGroups: groups},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func matchedJobResolve(t *testing.T, data []byte, check string) verificationplan.CheckPlan {
	t.Helper()
	cfg, err := config.ParseProjectConfig(data, "matched whole-job fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 1 {
		t.Fatal("selected job must contain exactly one complete check")
	}
	plan, err := verificationplan.ResolveCheck(check, cfg, verificationplan.ResolveOptions{ProjectID: "matched-fixture", Changes: "working"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func matchedJobEffectiveDefaults(run *verificationplan.RunSpec) {
	if run.Region == "" {
		run.Region = preflightapi.DefaultRegion
	}
	if run.CacheMode == "cache-only" && run.CacheLaneSizeBytes == 0 {
		run.CacheLaneSizeBytes = preflightapi.DefaultCacheLaneSizeBytes
	}
	run.NetworkAllowlist = append([]string(nil), run.NetworkAllowlist...)
	sort.Strings(run.NetworkAllowlist)
}

func matchedJobAssertNativeProof(t *testing.T, root string, fixture matchedJobFixture) {
	t.Helper()
	data := matchedJobRead(t, filepath.Join(root, "native-success.json"))
	if matchedJobSHA256(data) != fixture.NativeProofSHA256 {
		t.Fatal("native matched evidence projection changed")
	}
	var proof struct {
		Repository, Commit  string
		SealedCommandSHA256 string `json:"sealed_command_sha256"`
		Lanes               []struct {
			Lane, Status, Classification string
			ExitCode                     *int     `json:"exit_code"`
			EvidenceAttested             bool     `json:"evidence_attested"`
			EvidenceDigest               string   `json:"evidence_digest"`
			SealedCommandSHA256          string   `json:"sealed_command_sha256"`
			OverallCommandExitCode       *int     `json:"overall_command_exit_code"`
			LogsTruncated                bool     `json:"logs_truncated"`
			ObservedCommandOutputSteps   []string `json:"observed_command_output_steps"`
		} `json:"lanes"`
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatal(err)
	}
	if proof.Repository != fixture.Repository || proof.Commit != fixture.Commit || proof.SealedCommandSHA256 != fixture.SealedCommandSHA256 || len(proof.Lanes) != 2 {
		t.Fatal("native matched proof no longer binds this fixture")
	}
	for index, lane := range proof.Lanes {
		if lane.Lane != []string{"manual", "automatic"}[index] || lane.Status != "succeeded" || lane.Classification != "success" ||
			lane.ExitCode == nil || *lane.ExitCode != 0 || lane.OverallCommandExitCode == nil || *lane.OverallCommandExitCode != 0 ||
			!lane.EvidenceAttested || !strings.HasPrefix(lane.EvidenceDigest, "sha256:") || lane.LogsTruncated ||
			lane.SealedCommandSHA256 != fixture.SealedCommandSHA256 || fixture.TestStep == "" ||
			!containsString(lane.ObservedCommandOutputSteps, fixture.TestStep) {
			t.Fatalf("missing complete positive native evidence for %s", lane.Lane)
		}
	}
}

func matchedJobReadJSON(t *testing.T, path string, target any) {
	t.Helper()
	if err := json.Unmarshal(matchedJobRead(t, path), target); err != nil {
		t.Fatal(err)
	}
}

func matchedJobRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func matchedJobSHA256(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Preserve the recorded manual/native proof bytes and hashes. The abandoned
// execution alias field is asserted as historical evidence, then removed only
// in this test adapter to compare the current numeric execution contract.
func matchedJobNumericOracle(t *testing.T, data []byte, check string) ([]byte, map[string]preflightapi.RuntimeResolution) {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	fields := document["checks"].(map[string]any)[check].(map[string]any)
	raw, exists := fields["runtime_resolutions"]
	if !exists {
		return data, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var records map[string]preflightapi.RuntimeResolution
	if err := json.Unmarshal(encoded, &records); err != nil {
		t.Fatal(err)
	}
	delete(fields, "runtime_resolutions")
	numeric, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return numeric, records
}
