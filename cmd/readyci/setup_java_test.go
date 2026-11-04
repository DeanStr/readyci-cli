package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func setupJavaWorkflowJob(t *testing.T, repo, with string) repoImportCandidate {
	t.Helper()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), "on: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-java@v5\n        with:"+with+"\n      - run: mvn -B test\n")
	for _, candidate := range setupImportCandidates(repo, "github-actions") {
		if candidate.ID == "github-actions:.github/workflows/ci.yml:test" {
			return candidate
		}
	}
	t.Fatal("workflow job was not inventoried")
	return repoImportCandidate{}
}

// setup-java becomes a Java runtime requirement when it asks for an OpenJDK
// HotSpot JDK the image's OpenJDK can stand in for.
func TestWorkflowImportTranslatesSetupJava(t *testing.T) {
	for _, tc := range []struct {
		name, with, requirement, unsupported string
	}{
		{name: "temurin with Maven cache", with: "\n          distribution: temurin\n          java-version: '21'\n          cache: maven", requirement: "21"},
		{name: "integer version and x64 JDK", with: "\n          distribution: zulu\n          java-version: 21\n          java-package: jdk\n          architecture: x64", requirement: "21"},
		{name: "setup-java v1 without a distribution", with: "\n          java-version: '17'", requirement: "17"},
		{name: "GraalVM", with: "\n          distribution: graalvm\n          java-version: '21'", unsupported: "actions/setup-java distribution graalvm requires manual translation"},
		{name: "OpenJ9", with: "\n          distribution: semeru\n          java-version: '21'", unsupported: "actions/setup-java distribution semeru requires manual translation"},
		{name: "local JDK file", with: "\n          distribution: jdkfile\n          jdkFile: jdk.tar.gz\n          java-version: '21'", unsupported: "actions/setup-java input jdkFile requires manual translation"},
		{name: "JDK with jmods", with: "\n          distribution: temurin\n          java-version: '25'\n          java-package: jdk+jmods", unsupported: "actions/setup-java input java-package requires manual translation unless it is jdk"},
		{name: "ARM JDK", with: "\n          distribution: temurin\n          java-version: '21'\n          architecture: aarch64", unsupported: "actions/setup-java input architecture requires manual translation unless it is x64"},
		{name: "publishing settings", with: "\n          distribution: temurin\n          java-version: '21'\n          server-id: ossrh", unsupported: "actions/setup-java input server-id requires manual translation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			job := setupJavaWorkflowJob(t, repo, tc.with)
			unsupported := strings.Join(append(job.Unsupported, job.AutomaticUnsupported...), "; ")
			if tc.unsupported != "" {
				if !strings.Contains(unsupported, tc.unsupported) {
					t.Fatalf("unsupported = %q, want %q", unsupported, tc.unsupported)
				}
				return
			}
			if unsupported != "" || job.RuntimeRequirements["java"] != tc.requirement || !job.TestEntrypoint || job.ScopeUnknown {
				t.Fatalf("setup-java was not translated: unsupported=%q requirements=%v test=%v scopeUnknown=%v", unsupported, job.RuntimeRequirements, job.TestEntrypoint, job.ScopeUnknown)
			}
			if !strings.Contains(strings.Join(job.Requirements, "; "), "is satisfied by the image's OpenJDK build of the requested Java version") {
				t.Fatalf("distribution substitution was not disclosed: %v", job.Requirements)
			}
		})
	}
}

func TestSetupRecognizesMavenAndGradleTestInvocations(t *testing.T) {
	for command, tests := range map[string]bool{
		"mvn test":                             true,
		"mvn -B verify javadoc:jar":            true,
		"./mvnw -B clean install":              true,
		"mvn package":                          true,
		"mvn check":                            true,
		"mvn -B install -DskipTests":           false,
		"./mvnw install -U -DskipTests=true":   false,
		"mvn package -Dmaven.test.skip=true":   false,
		"mvn compile":                          false,
		"mvn dependency:go-offline":            false,
		"./gradlew build --warning-mode all":   true,
		"./gradlew check":                      true,
		"gradle test":                          true,
		"./gradlew build -x test":              false,
		"./gradlew clean publishToMavenLocal":  false,
		"./gradlew assemble":                   false,
		"./gradlew build --exclude-task check": false,
	} {
		if got := setupCommandRunsTests(command); got != tests {
			t.Errorf("setupCommandRunsTests(%q) = %v, want %v", command, got, tests)
		}
	}
}

// A JsonPath-shaped Gradle matrix imports its JDK 21 variant with the
// wrapper's distribution hosts, Maven Central and the Gradle caches. Publishing
// to the local Maven repository is an ordinary upstream step, not a release.
func TestSetupImportsAGradleWrapperJavaMatrixVariant(t *testing.T) {
	const workflow = "on:\n  push:\n  pull_request:\njobs:\n  test:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        java: [17, 21]\n      fail-fast: false\n    name: JDK ${{ matrix.java }}\n    steps:\n      - uses: actions/checkout@v4\n      - name: Set up JDK\n        uses: actions/setup-java@v3\n        with:\n          java-version: ${{ matrix.java }}\n          distribution: temurin\n          cache: 'gradle'\n      - name: Grant execute permission for gradlew\n        run: chmod +x gradlew\n      - name: Build with Gradle\n        run: ./gradlew build --warning-mode all\n      - name: Check\n        run: ./gradlew check\n      - name: Publish to local\n        run: ./gradlew clean publishToMavenLocal\n"
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/ci.yml"), workflow)
	writeFileForRepoMapTest(t, filepath.Join(repo, "gradlew"), "#!/bin/sh\nexit 0\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, "build.gradle"), "")
	candidates := setupImportCandidates(repo, "")
	var selected *repoImportCandidate
	for i, candidate := range candidates {
		if candidate.ParentJobID == "github-actions:.github/workflows/ci.yml:test" && candidate.Matrix["java"] == "21" {
			selected = &candidates[i]
		}
	}
	if selected == nil {
		t.Fatalf("JDK 21 variant was not inventoried: %+v", candidates)
	}
	if problems := strings.Join(append(selected.Unsupported, selected.AutomaticUnsupported...), "; "); problems != "" || selected.ScopeUnknown || !selected.TestEntrypoint {
		t.Fatalf("JDK 21 variant is not importable: problems=%q scopeUnknown=%v test=%v", problems, selected.ScopeUnknown, selected.TestEntrypoint)
	}
	// Zero-config setup offers the variants to the runner in matrix order and
	// imports the first one admitted, here the image's JDK 21.
	var asked []string
	options := setupOptions{Profile: "remote-ci", variantAdmission: func(template, check string) (bool, bool) {
		cfg, err := config.ParseProjectConfig([]byte(template), "variant")
		if err != nil {
			t.Fatal(err)
		}
		asked = append(asked, cfg.Checks[check].RuntimeRequirements["java"])
		return cfg.Checks[check].RuntimeRequirements["java"] == "21", true
	}}
	template, err := setupDetectedTemplate(repo, "fixture", options, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []string{"17", "21"}) || !reflect.DeepEqual(setupImportedCandidates(t, template), []string{selected.ID}) {
		t.Fatalf("admission asked %q and imported %v, want the JDK 21 variant:\n%s", asked, setupImportedCandidates(t, template), template)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "java variant")
	if err != nil {
		t.Fatalf("%v\n%s", err, template)
	}
	check := cfg.Checks["test"]
	var commands []string
	for _, step := range check.Steps {
		commands = append(commands, step.Command)
	}
	if want := []string{"bash -e -c 'chmod +x gradlew'", "bash -e -c './gradlew build --warning-mode all'", "bash -e -c './gradlew check'", "bash -e -c './gradlew clean publishToMavenLocal'"}; !reflect.DeepEqual(commands, want) {
		t.Fatalf("steps = %q, want %q\n%s", commands, want, template)
	}
	if check.RuntimeRequirements["java"] != "21" {
		t.Fatalf("java requirement = %q:\n%s", check.RuntimeRequirements["java"], template)
	}
	for _, host := range []string{"repo.maven.apache.org", "repo1.maven.org", "plugins.gradle.org", "plugins-artifacts.gradle.org", "services.gradle.org", "github.com", "release-assets.githubusercontent.com"} {
		if !containsString(cfg.Network.Allowlist, host) {
			t.Fatalf("network allowlist %v lacks %s:\n%s", cfg.Network.Allowlist, host, template)
		}
	}
	for _, path := range []string{"~/.m2/repository", "~/.gradle/caches", "~/.gradle/wrapper"} {
		if !containsString(cfg.Cache.Paths, path) {
			t.Fatalf("cache paths %v lack %s:\n%s", cfg.Cache.Paths, path, template)
		}
	}
}

// Maven's verify phase runs the tests, so a gson-like build job is a test job
// and is imported on its own; packaging without tests does not compete with it.
func TestSetupImportsAMavenVerifyJob(t *testing.T) {
	repo := t.TempDir()
	writeFileForRepoMapTest(t, filepath.Join(repo, "pom.xml"), "<project/>\n")
	writeFileForRepoMapTest(t, filepath.Join(repo, ".github/workflows/build.yml"), "on: [push, pull_request]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-java@v5\n        with:\n          distribution: temurin\n          java-version: '21'\n          cache: maven\n      - run: mvn verify javadoc:jar\n  package:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-java@v5\n        with:\n          distribution: temurin\n          java-version: '21'\n      - run: mvn -B package -DskipTests\n")
	template, err := setupDetectedTemplate(repo, "fixture", setupOptions{Profile: "remote-ci"}, setupImportCandidates(repo, ""))
	if err != nil {
		t.Fatal(err)
	}
	if imported := setupImportedCandidates(t, template); !reflect.DeepEqual(imported, []string{"github-actions:.github/workflows/build.yml:build"}) {
		t.Fatalf("imported %v:\n%s", imported, template)
	}
	cfg, err := config.ParseProjectConfig([]byte(template), "maven")
	if err != nil {
		t.Fatal(err)
	}
	check := cfg.Checks["build"]
	if check.RuntimeRequirements["java"] != "21" || containsString(cfg.Network.Allowlist, "github.com") || !containsString(cfg.Network.Allowlist, "repo.maven.apache.org") {
		t.Fatalf("Maven job requirements or hosts are wrong: %v %v\n%s", check.RuntimeRequirements, cfg.Network.Allowlist, template)
	}
}

func TestWorkflowImportExcludesRemoteJavaPublication(t *testing.T) {
	for command, operational := range map[string]bool{
		"mvn -B deploy":                       true,
		"./mvnw deploy -DskipTests":           true,
		"mvn deploy:deploy-file -Dfile=a.jar": true,
		"./gradlew publish":                   true,
		"./gradlew :lib:publishToSonatype":    true,
		"gradle publishPlugins":               true,
		"./gradlew clean publishToMavenLocal": false,
		"./gradlew build":                     false,
		"mvn -B verify":                       false,
	} {
		job := repoWorkflowJob{Name: "build", Commands: []string{command}}
		if got := isOperationalWorkflowJob(repoWorkflowMap{Path: ".github/workflows/ci.yml"}, job); got != operational {
			t.Errorf("isOperationalWorkflowJob(%q) = %v, want %v", command, got, operational)
		}
	}
}
