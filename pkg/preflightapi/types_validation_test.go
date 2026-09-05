package preflightapi

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWorkerBundleUploadSignaturesRejectTamperingAndExpiry(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute).Unix()
	signature := SignWorkerBundleUpload("run_1", "worker_1", "baseline_1", expires, "secret")

	if !VerifyWorkerBundleUpload("run_1", "worker_1", "baseline_1", expires, strings.ToUpper(signature), "secret", now) {
		t.Fatal("valid worker bundle upload signature was rejected")
	}
	if VerifyWorkerBundleUpload("run_1", "worker_2", "baseline_1", expires, signature, "secret", now) {
		t.Fatal("signature verified after worker id tampering")
	}
	if VerifyWorkerBundleUpload("run_1", "worker_1", "baseline_1", expires, signature, "secret", now.Add(2*time.Minute)) {
		t.Fatal("expired worker bundle upload signature verified")
	}
	if VerifyWorkerBundleUpload("run_1", "worker_1", "baseline_1", expires, "", "secret", now) {
		t.Fatal("empty worker bundle upload signature verified")
	}
	if VerifyWorkerBundleUpload("run_1", "worker_1", "baseline_1", expires, signature, "", now) {
		t.Fatal("signature verified without a secret")
	}
	baseline := WorkspaceBaseline{Key: "baseline_1", TeamID: "team_1", ProjectID: "project_1", CacheLaneID: "cache_lane_1"}
	scopedSignature := SignWorkerBundleUploadScoped("run_1", "worker_1", baseline, expires, "secret")
	if !VerifyWorkerBundleUploadScoped("run_1", "worker_1", baseline, expires, strings.ToUpper(scopedSignature), "secret", now) {
		t.Fatal("valid scoped worker bundle upload signature was rejected")
	}
	otherProject := baseline
	otherProject.ProjectID = "project_2"
	if VerifyWorkerBundleUploadScoped("run_1", "worker_1", otherProject, expires, scopedSignature, "secret", now) {
		t.Fatal("scoped signature verified after project tampering")
	}
	otherLane := baseline
	otherLane.CacheLaneID = "cache_lane_2"
	if VerifyWorkerBundleUploadScoped("run_1", "worker_1", otherLane, expires, scopedSignature, "secret", now) {
		t.Fatal("scoped signature verified after cache lane tampering")
	}

	proof := WorkerBundleUploadProof{
		RunID:       "run_1",
		WorkerID:    "worker_1",
		BaselineKey: "baseline_1",
		ExpiresUnix: expires,
		SizeBytes:   1024,
	}
	proof.Signature = SignWorkerBundleUploadProof(proof.RunID, proof.WorkerID, proof.BaselineKey, proof.ExpiresUnix, proof.SizeBytes, "secret")
	if !VerifyWorkerBundleUploadProof(proof, "secret", now) {
		t.Fatal("valid worker bundle upload proof was rejected")
	}
	proof.SizeBytes = 1025
	if VerifyWorkerBundleUploadProof(proof, "secret", now) {
		t.Fatal("proof verified after size tampering")
	}
	proof.SizeBytes = -1
	proof.Signature = SignWorkerBundleUploadProof(proof.RunID, proof.WorkerID, proof.BaselineKey, proof.ExpiresUnix, proof.SizeBytes, "secret")
	if VerifyWorkerBundleUploadProof(proof, "secret", now) {
		t.Fatal("proof with negative size verified")
	}
}

func TestTrustedSecretHashesAreStableAndIgnoreIncompleteSteps(t *testing.T) {
	commandHash := TrustedSecretCommandHash("echo $TOKEN")
	if commandHash == TrustedSecretCommandHash("echo $OTHER_TOKEN") {
		t.Fatal("different trusted secret commands produced the same hash")
	}
	if !strings.HasPrefix(commandHash, "sha256:") || len(commandHash) != len("sha256:")+64 {
		t.Fatalf("command hash = %q, want sha256 hex digest", commandHash)
	}

	steps := []RunStepSpec{
		{Name: "unit", Command: "go test ./...", Env: map[string]string{"B": "2", "A": "1"}, TimeoutSeconds: 60},
		{Name: "missing-command", Command: "  "},
		{Name: "always", Command: "pnpm test", Always: true},
	}
	reorderedEnv := []RunStepSpec{
		{Name: "unit", Command: "go test ./...", Env: map[string]string{"A": "1", "B": "2"}, TimeoutSeconds: 60},
		{Name: "always", Command: "pnpm test", Always: true},
	}
	if TrustedSecretStepsHash(steps) != TrustedSecretStepsHash(reorderedEnv) {
		t.Fatal("trusted secret step hash changed for reordered env keys or ignored incomplete steps")
	}
	if TrustedSecretStepsHash(reorderedEnv) == TrustedSecretStepsHash([]RunStepSpec{{Name: "unit", Command: "go test ./...", TimeoutSeconds: 60}}) {
		t.Fatal("different trusted secret steps produced the same hash")
	}
}

func TestRegionAndConcurrencyValidation(t *testing.T) {
	if NormalizeRegion("  US-East_1 ") != "us-east_1" {
		t.Fatal("region was not trimmed and lower-cased")
	}
	for _, region := range []string{"default", "us-east.1", "a_b-9"} {
		if err := ValidateRegion(region); err != nil {
			t.Fatalf("ValidateRegion(%q) = %v, want nil", region, err)
		}
	}
	for _, region := range []string{"-bad", "bad-", "bad space", strings.Repeat("a", 65)} {
		if err := ValidateRegion(region); err == nil {
			t.Fatalf("ValidateRegion(%q) succeeded, want error", region)
		}
	}

	group, limit, err := NormalizeConcurrency("deploy/main", 0)
	if err != nil {
		t.Fatalf("NormalizeConcurrency valid group: %v", err)
	}
	if group != "deploy/main" || limit != 1 {
		t.Fatalf("NormalizeConcurrency = %q/%d, want default limit of 1", group, limit)
	}
	for _, tc := range []struct {
		group string
		limit int
	}{
		{group: "", limit: 2},
		{group: "bad\ncontrol", limit: 1},
		{group: strings.Repeat("a", MaxConcurrencyGroupLength+1), limit: 1},
		{group: "deploy", limit: -1},
	} {
		if _, _, err := NormalizeConcurrency(tc.group, tc.limit); err == nil {
			t.Fatalf("NormalizeConcurrency(%q, %d) succeeded, want error", tc.group, tc.limit)
		}
	}
}

func TestRunnerSizeMetadataIsStableAndDefensive(t *testing.T) {
	specs := RunnerSizeSpecs()
	if len(specs) != 4 {
		t.Fatalf("runner size specs = %d, want 4", len(specs))
	}
	specs[0].Name = "mutated"
	names := RunnerSizeNames()
	if strings.Join(names, ",") != "small,standard,large,xlarge" {
		t.Fatalf("runner size names = %v", names)
	}
	spec, ok := RunnerSizeSpecFor(" ")
	if !ok || spec.Name != DefaultRunnerSize || spec.SlotCost != 2 {
		t.Fatalf("default runner size spec = %+v/%t, want standard", spec, ok)
	}
	spec, ok = RunnerSizeSpecFor("large")
	if !ok || spec.VCPUCount != 4 || spec.MemoryMiB != 10240 || spec.SlotCost != 4 {
		t.Fatalf("large runner size spec = %+v/%t", spec, ok)
	}
	if _, ok := RunnerSizeSpecFor("unknown"); ok {
		t.Fatal("unknown runner size returned a spec")
	}
	if got := RunnerSizeSlotCost("xlarge"); got != 8 {
		t.Fatalf("xlarge slot cost = %d, want 8", got)
	}
	if got := RunnerSizeSlotCost("unknown"); got != 0 {
		t.Fatalf("unknown slot cost = %d, want 0", got)
	}
}

func TestRunEventDataBytesPrefersBase64Payload(t *testing.T) {
	event := RunEvent{Data: "plain", DataBase64: base64.StdEncoding.EncodeToString([]byte("encoded"))}
	data, err := event.DataBytes()
	if err != nil {
		t.Fatalf("DataBytes base64: %v", err)
	}
	if string(data) != "encoded" {
		t.Fatalf("DataBytes = %q, want encoded payload", data)
	}
	event = RunEvent{Data: "plain"}
	data, err = event.DataBytes()
	if err != nil {
		t.Fatalf("DataBytes plain: %v", err)
	}
	if string(data) != "plain" {
		t.Fatalf("DataBytes = %q, want plain payload", data)
	}
	event = RunEvent{DataBase64: "not base64"}
	if _, err := event.DataBytes(); err == nil {
		t.Fatal("invalid base64 payload decoded without error")
	}
}

func TestValidateRunServicesCatchesUnsafeOrInvalidServiceSpecs(t *testing.T) {
	valid := RunServiceSpec{
		Name:    "postgres_main",
		Type:    "postgres",
		Version: "16",
		Env:     map[string]string{"POSTGRES_DB": "readyci", "POSTGRES_PASSWORD": "postgres"},
		Health:  RunServiceHealthSpec{Command: "pg_isready -U postgres", Timeout: "30s", Interval: "2s"},
		Logs:    []string{"/var/log/postgresql/*.log"},
	}
	if err := ValidateRunServices([]RunServiceSpec{valid}, true); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}
	validContainer := RunServiceSpec{
		Name:   "redis",
		Type:   "container",
		Image:  "redis:7-alpine",
		Ports:  []string{"6379/tcp"},
		Env:    map[string]string{"REDIS_PASSWORD": "dev"},
		Health: RunServiceHealthSpec{Command: "redis-cli ping", Timeout: "30s", Interval: "2s"},
		Logs:   []string{"/data/*.log"},
	}
	if err := ValidateRunServices([]RunServiceSpec{validContainer}, true); err != nil {
		t.Fatalf("valid container service rejected: %v", err)
	}
	if !ValidRunServiceImage("redis:RC1") {
		t.Fatal("valid image tag with uppercase characters was rejected")
	}

	cases := []struct {
		name    string
		service RunServiceSpec
		docker  bool
	}{
		{name: "requires docker", service: valid, docker: false},
		{name: "bad name", service: withServiceName(valid, " postgres"), docker: true},
		{name: "bad type", service: withServiceType(valid, "redis"), docker: true},
		{name: "padded type", service: withServiceType(valid, " postgres"), docker: true},
		{name: "bad version", service: withServiceVersion(valid, "14"), docker: true},
		{name: "postgres with image", service: withServiceImage(valid, "postgres:16"), docker: true},
		{name: "container with version", service: withServiceVersion(validContainer, "7"), docker: true},
		{name: "container missing image", service: withServiceImage(validContainer, ""), docker: true},
		{name: "container bad image", service: withServiceImage(validContainer, "https://example.invalid/redis"), docker: true},
		{name: "container uppercase repository image", service: withServiceImage(validContainer, "Redis:7-alpine"), docker: true},
		{name: "container empty tag image", service: withServiceImage(validContainer, "redis:"), docker: true},
		{name: "container empty digest image", service: withServiceImage(validContainer, "redis@"), docker: true},
		{name: "container empty digest payload image", service: withServiceImage(validContainer, "redis@sha256:"), docker: true},
		{name: "container multiple tag separator image", service: withServiceImage(validContainer, "redis:7:extra"), docker: true},
		{name: "container padded image", service: withServiceImage(validContainer, " redis:7-alpine"), docker: true},
		{name: "container bad port", service: withServicePorts(validContainer, []string{"6379:6379"}), docker: true},
		{name: "container signed port", service: withServicePorts(validContainer, []string{"+6379/tcp"}), docker: true},
		{name: "container leading zero port", service: withServicePorts(validContainer, []string{"06379/tcp"}), docker: true},
		{name: "missing health", service: withServiceHealth(valid, RunServiceHealthSpec{}), docker: true},
		{name: "health command NUL byte", service: withServiceHealth(valid, RunServiceHealthSpec{Command: "pg_isready\x00echo ok"}), docker: true},
		{name: "bad env key", service: withServiceEnv(valid, map[string]string{"postgres_password": "postgres"}), docker: true},
		{name: "control character env value", service: withServiceEnv(valid, map[string]string{"POSTGRES_PASSWORD": "post\ngres"}), docker: true},
		{name: "secret env value", service: withServiceEnv(valid, map[string]string{"POSTGRES_PASSWORD": "${{ secrets.DB_PASSWORD }}"}), docker: true},
		{name: "secret env value without spaces", service: withServiceEnv(valid, map[string]string{"POSTGRES_PASSWORD": "${{secrets.DB_PASSWORD}}"}), docker: true},
		{name: "secret env bracket expression", service: withServiceEnv(valid, map[string]string{"POSTGRES_PASSWORD": "${{ secrets['DB_PASSWORD'] }}"}), docker: true},
		{name: "secret env double quote bracket expression", service: withServiceEnv(valid, map[string]string{"POSTGRES_PASSWORD": `${{ secrets["DB_PASSWORD"] }}`}), docker: true},
		{name: "bad timeout", service: withServiceHealth(valid, RunServiceHealthSpec{Command: "pg_isready", Timeout: "0s"}), docker: true},
		{name: "empty log path", service: withServiceLogs(valid, []string{""}), docker: true},
		{name: "blank log path", service: withServiceLogs(valid, []string{" \t "}), docker: true},
		{name: "control character log path", service: withServiceLogs(valid, []string{"/var/log/postgres\nsecret.log"}), docker: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRunServices([]RunServiceSpec{tc.service}, tc.docker); err == nil {
				t.Fatal("ValidateRunServices succeeded, want error")
			}
		})
	}
	if err := ValidateRunServices([]RunServiceSpec{valid, valid}, true); err == nil {
		t.Fatal("duplicate service names were accepted")
	}
	if err := ValidateRunServices([]RunServiceSpec{
		withServiceName(validContainer, "redis-cache"),
		withServiceName(validContainer, "redis_cache"),
	}, true); err == nil {
		t.Fatal("colliding service environment variable prefixes were accepted")
	}
}

func TestValidateRunServicePortRequiresCanonicalUnsignedPortNumber(t *testing.T) {
	validPorts := []string{"1/tcp", "6379/tcp", "65535/udp"}
	for _, port := range validPorts {
		if err := ValidateRunServicePort(port); err != nil {
			t.Fatalf("ValidateRunServicePort(%q) rejected valid port: %v", port, err)
		}
	}

	invalidPorts := []string{
		"+6379/tcp",
		"-1/tcp",
		"06379/tcp",
		"0/tcp",
		"65536/tcp",
		"6379.0/tcp",
		"6379/TCP",
	}
	for _, port := range invalidPorts {
		if err := ValidateRunServicePort(port); err == nil {
			t.Fatalf("ValidateRunServicePort(%q) succeeded, want error", port)
		}
	}
}

func TestValidateRunStepsAndWorkerLabels(t *testing.T) {
	steps := []RunStepSpec{{
		Name:           "unit-tests",
		Command:        "go test ./...",
		Env:            map[string]string{"READYCI_MODE": "test"},
		TimeoutSeconds: 120,
	}}
	if err := ValidateRunSteps(steps); err != nil {
		t.Fatalf("valid steps rejected: %v", err)
	}
	for _, step := range []RunStepSpec{
		{Name: "", Command: "go test ./..."},
		{Name: " bad", Command: "go test ./..."},
		{Name: "unit", Command: "  "},
		{Name: "unit", Command: "go test ./...\x00echo ok"},
		{Name: "unit", Command: "go test ./...", TimeoutSeconds: -1},
		{Name: "unit", Command: "go test ./...", Env: map[string]string{"bad-key": "value"}},
		{Name: "unit", Command: "go test ./...", Env: map[string]string{"READYCI_MODE": "test\nshadow"}},
		{Name: "unit", Command: "go test ./...", Env: map[string]string{"READYCI_TOKEN": "${{secrets.READYCI_TOKEN}}"}},
	} {
		if err := ValidateRunSteps([]RunStepSpec{step}); err == nil {
			t.Fatalf("ValidateRunSteps(%+v) succeeded, want error", step)
		}
	}
	if err := ValidateRunSteps([]RunStepSpec{steps[0], steps[0]}); err == nil {
		t.Fatal("duplicate step names were accepted")
	}

	labels, err := NormalizeWorkerLabels(map[string]string{" role ": " linux/x64 ", "env.prod": "true"})
	if err != nil {
		t.Fatalf("NormalizeWorkerLabels valid map: %v", err)
	}
	if labels["role"] != "linux/x64" || labels["env.prod"] != "true" {
		t.Fatalf("normalized labels = %+v, want trimmed key/value", labels)
	}
	for _, labels := range []map[string]string{
		{"": "value"},
		{ReservedWorkerLabelPrefix + "system": "value"},
		{ReservedWorkerSystemLabelPrefix + "custom": "value"},
		{WorkerIDSystemLabel: "worker-02"},
		{RootFSReleaseSystemLabel: strings.Repeat("a", 64)},
		{"role": ""},
		{"bad key": "value"},
		{"role": "bad value"},
		{"role": "linux", " role ": "arm64"},
	} {
		if err := ValidateWorkerLabels(labels); err == nil {
			t.Fatalf("ValidateWorkerLabels(%+v) succeeded, want error", labels)
		}
	}
	if err := ValidateWorkerSelector(map[string]string{"role": "linux"}); err != nil {
		t.Fatalf("valid worker selector rejected: %v", err)
	}
	systemSelector, err := NormalizeWorkerSelector(map[string]string{
		WorkerIDSystemLabel:         "worker-02",
		RootFSReleaseSystemLabel:    strings.Repeat("a", 64),
		DockerCapabilitySystemLabel: "true",
	})
	if err != nil || len(systemSelector) != 3 {
		t.Fatalf("system worker selector = %+v/%v, want three validated labels", systemSelector, err)
	}
	for _, selector := range []map[string]string{
		{ReservedWorkerSystemLabelPrefix + "custom": "value"},
		{RootFSReleaseSystemLabel: "not-a-digest"},
		{RuntimeManifestSystemLabel: "not-a-digest"},
		{ApprovedRuntimeSystemLabel: "not-a-digest"},
		{DockerCapabilitySystemLabel: "false"},
	} {
		if err := ValidateWorkerSelector(selector); err == nil {
			t.Fatalf("ValidateWorkerSelector(%+v) succeeded, want error", selector)
		}
	}
	if err := ValidateWorkerSelector(map[string]string{"role": "bad value"}); err == nil || !strings.Contains(err.Error(), "worker_selector") {
		t.Fatalf("worker selector error = %v, want wrapped validation error", err)
	}
	claimLabels := make(map[string]string, MaxWorkerLabels+4)
	for index := 0; index < MaxWorkerLabels; index++ {
		claimLabels[fmt.Sprintf("label-%02d", index)] = "value"
	}
	claimLabels[WorkerIDSystemLabel] = "worker-02"
	claimLabels[RootFSReleaseSystemLabel] = strings.Repeat("a", 64)
	claimLabels[RuntimeManifestSystemLabel] = strings.Repeat("b", 64)
	claimLabels[ApprovedRuntimeSystemLabel] = strings.Repeat("c", 64)
	if normalized, err := NormalizeWorkerClaimLabels(claimLabels); err != nil || len(normalized) != MaxWorkerLabels+4 {
		t.Fatalf("worker claim labels = %d/%v, want %d labels", len(normalized), err, MaxWorkerLabels+4)
	}
	claimLabels["overflow"] = "value"
	if _, err := NormalizeWorkerClaimLabels(claimLabels); err == nil {
		t.Fatal("worker claim accepted more than 32 user labels")
	}
	delete(claimLabels, "overflow")
	claimLabels[RootFSReleaseSystemLabel] = "not-a-digest"
	if _, err := NormalizeWorkerClaimLabels(claimLabels); err == nil {
		t.Fatal("worker claim accepted an invalid rootfs release label")
	}
	claimLabels[RootFSReleaseSystemLabel] = strings.Repeat("a", 64)
	claimLabels[RuntimeManifestSystemLabel] = "not-a-digest"
	if _, err := NormalizeWorkerClaimLabels(claimLabels); err == nil {
		t.Fatal("worker claim accepted an invalid runtime manifest label")
	}
	claimLabels[RuntimeManifestSystemLabel] = strings.Repeat("b", 64)
	claimLabels[ApprovedRuntimeSystemLabel] = "not-a-digest"
	if _, err := NormalizeWorkerClaimLabels(claimLabels); err == nil {
		t.Fatal("worker claim accepted an invalid approved runtime label")
	}
}

func withServiceName(service RunServiceSpec, name string) RunServiceSpec {
	service.Name = name
	return service
}

func withServiceType(service RunServiceSpec, serviceType string) RunServiceSpec {
	service.Type = serviceType
	return service
}

func withServiceVersion(service RunServiceSpec, version string) RunServiceSpec {
	service.Version = version
	return service
}

func withServiceImage(service RunServiceSpec, image string) RunServiceSpec {
	service.Image = image
	return service
}

func withServicePorts(service RunServiceSpec, ports []string) RunServiceSpec {
	service.Ports = ports
	return service
}

func withServiceEnv(service RunServiceSpec, env map[string]string) RunServiceSpec {
	service.Env = env
	return service
}

func withServiceHealth(service RunServiceSpec, health RunServiceHealthSpec) RunServiceSpec {
	service.Health = health
	return service
}

func withServiceLogs(service RunServiceSpec, logs []string) RunServiceSpec {
	service.Logs = logs
	return service
}
