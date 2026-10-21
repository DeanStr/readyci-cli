package main

import "github.com/DeanStr/readyci-cli/pkg/preflightapi"

type repoMapReport struct {
	Status          string               `json:"status"`
	ConfigPath      string               `json:"config_path,omitempty"`
	Inferred        bool                 `json:"inferred,omitempty"`
	ProjectID       string               `json:"project_id,omitempty"`
	Checks          []string             `json:"checks,omitempty"`
	Signals         []repoMapSignal      `json:"signals,omitempty"`
	CheckReferences []repoCheckReference `json:"check_references,omitempty"`
	MakeReferences  []repoMakeReference  `json:"make_references,omitempty"`
	UndefinedChecks []repoUndefinedCheck `json:"undefined_checks,omitempty"`
	UndefinedMake   []repoUndefinedMake  `json:"undefined_make_targets,omitempty"`
	Unreferenced    []string             `json:"unreferenced_checks,omitempty"`
	Make            repoMakeMap          `json:"make,omitempty"`
	GitHubActions   repoGitHubActionsMap `json:"github_actions,omitempty"`
	GoModules       []repoGoModuleMap    `json:"go_modules,omitempty"`
	Cargo           *repoCargoMap        `json:"cargo,omitempty"`
	NodePackages    []repoNodePackageMap `json:"node_packages,omitempty"`
	OpenAPI         []repoOpenAPIMap     `json:"openapi,omitempty"`
	OpsPaths        []string             `json:"ops_paths,omitempty"`
	ShellPaths      []string             `json:"shell_paths,omitempty"`
	Warnings        []string             `json:"warnings,omitempty"`
}

type repoMapSignal struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path,omitempty"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
}

type repoCheckReference struct {
	Check   string `json:"check"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Context string `json:"context,omitempty"`
	Archive bool   `json:"archive,omitempty"`
}

type repoMakeReference struct {
	Target  string `json:"target"`
	Dir     string `json:"dir,omitempty"`
	Source  string `json:"source"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Context string `json:"context,omitempty"`
	Archive bool   `json:"archive,omitempty"`
}

type repoUndefinedCheck struct {
	Check      string               `json:"check"`
	References []repoCheckReference `json:"references"`
}

type repoUndefinedMake struct {
	Target     string              `json:"target"`
	References []repoMakeReference `json:"references"`
}

type repoMakeMap struct {
	Files   []string         `json:"files,omitempty"`
	Targets []repoMakeTarget `json:"targets,omitempty"`
}

type repoMakeTarget struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Conditional bool   `json:"conditional,omitempty"`
}

type repoGitHubActionsMap struct {
	Workflows []repoWorkflowMap `json:"workflows,omitempty"`
}

type repoGoModuleMap struct {
	Path string `json:"path"`
}

type repoWorkflowMap struct {
	Path     string            `json:"path"`
	Jobs     []repoWorkflowJob `json:"jobs,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Problems []string          `json:"problems,omitempty"`
}

type repoWorkflowJob struct {
	ID                   string                                    `json:"id,omitempty"`
	ScopeUnknown         bool                                      `json:"scope_unknown,omitempty"`
	ParentJobID          string                                    `json:"parent_job_id,omitempty"`
	Matrix               map[string]string                         `json:"matrix,omitempty"`
	Timeout              string                                    `json:"timeout,omitempty"`
	RuntimeRequirements  map[string]string                         `json:"runtime_requirements,omitempty"`
	RuntimePins          map[string]string                         `json:"runtime_pins,omitempty"`
	RuntimeResolutions   map[string]preflightapi.RuntimeResolution `json:"runtime_resolutions,omitempty"`
	Name                 string                                    `json:"name"`
	RunsOn               []string                                  `json:"runs_on,omitempty"`
	Needs                []string                                  `json:"needs,omitempty"`
	Commands             []string                                  `json:"commands,omitempty"`
	CommandLines         []int                                     `json:"command_lines,omitempty"`
	RunSteps             []repoWorkflowRunStep                     `json:"run_steps,omitempty"`
	Runtimes             []string                                  `json:"runtimes,omitempty"`
	Conditions           []repoWorkflowCondition                   `json:"conditions,omitempty"`
	Requirements         []string                                  `json:"requirements,omitempty"`
	Unsupported          []string                                  `json:"unsupported,omitempty"`
	AutomaticUnsupported []string                                  `json:"automatic_unsupported,omitempty"`
}

type repoWorkflowRunStep struct {
	Command          string            `json:"command"`
	Line             int               `json:"line"`
	Shell            string            `json:"shell,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Timeout          string            `json:"timeout,omitempty"`
}

type repoWorkflowCondition struct {
	Scope       string `json:"scope"`
	Expression  string `json:"expression"`
	Line        int    `json:"line,omitempty"`
	Supported   bool   `json:"supported"`
	Translation string `json:"translation,omitempty"`
}

type repoCargoMap struct {
	WorkspaceRoot string                `json:"workspace_root"`
	Packages      []repoCargoPackageMap `json:"packages"`
	Rules         []repoCargoRuleMap    `json:"suggested_rules,omitempty"`
}

type repoCargoPackageMap struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Check      string   `json:"check"`
	Dependents []string `json:"dependents,omitempty"`
}

type repoCargoRuleMap struct {
	Paths  []string `json:"paths"`
	Checks []string `json:"checks"`
}

type repoNodePackageMap struct {
	Path            string   `json:"path"`
	PackageManager  string   `json:"package_manager,omitempty"`
	Lockfile        string   `json:"lockfile,omitempty"`
	PreferredScript string   `json:"preferred_script,omitempty"`
	Scripts         []string `json:"scripts,omitempty"`
	ContractScripts []string `json:"contract_scripts,omitempty"`
	HasVite         bool     `json:"has_vite,omitempty"`
	HasReact        bool     `json:"has_react,omitempty"`
	HasPlaywright   bool     `json:"has_playwright,omitempty"`
}

type repoOpenAPIMap struct {
	Source             string   `json:"source"`
	GeneratedConsumers []string `json:"generated_consumers,omitempty"`
	CheckCandidates    []string `json:"check_candidates,omitempty"`
}

type repoImportCandidate struct {
	ScopeUnknown         bool                                      `json:"scope_unknown,omitempty"`
	ParentJobID          string                                    `json:"parent_job_id,omitempty"`
	Matrix               map[string]string                         `json:"matrix,omitempty"`
	RunSteps             []repoWorkflowRunStep                     `json:"run_steps,omitempty"`
	Timeout              string                                    `json:"timeout,omitempty"`
	RuntimeRequirements  map[string]string                         `json:"runtime_requirements,omitempty"`
	RuntimePins          map[string]string                         `json:"runtime_pins,omitempty"`
	RuntimeResolutions   map[string]preflightapi.RuntimeResolution `json:"runtime_resolutions,omitempty"`
	TestEntrypoint       bool                                      `json:"test_entrypoint,omitempty"`
	AutomaticUnsupported []string                                  `json:"automatic_unsupported,omitempty"`
	Runtimes             []string                                  `json:"runtimes,omitempty"`
	WorkingDirectories   []string                                  `json:"working_directories,omitempty"`
	ID                   string                                    `json:"id,omitempty"`
	Source               string                                    `json:"source"`
	Name                 string                                    `json:"name"`
	Command              string                                    `json:"command,omitempty"`
	Check                string                                    `json:"check,omitempty"`
	Reason               string                                    `json:"reason,omitempty"`
	Confidence           float64                                   `json:"confidence"`
	Evidence             []string                                  `json:"evidence,omitempty"`
	Requirements         []string                                  `json:"requirements,omitempty"`
	Unsupported          []string                                  `json:"unsupported,omitempty"`
}

type repoImportReport struct {
	Source     string                `json:"source"`
	Mode       string                `json:"mode"`
	Candidates []repoImportCandidate `json:"candidates"`
	References []repoCheckReference  `json:"references,omitempty"`
	Warnings   []string              `json:"warnings,omitempty"`
}
