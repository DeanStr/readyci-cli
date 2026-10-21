package preflightapi

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RuntimeInventory lists installed exact versions. Its enclosing map is keyed
// by image; an inventory is never inferred from another image or from the host.
// The first version is the image default; remaining versions are numerically ordered.
type RuntimeInventory map[string][]string

var runtimeNames = map[string]bool{"node": true, "go": true, "python": true, "rust": true, "cargo": true, "npm": true, "pnpm": true, "yarn": true}
var runtimeTermPattern = regexp.MustCompile(`^(>=|<=|!=|==|>|<|=|\^|~)?\s*(v?[0-9]+(?:\.[0-9]+){0,2}(?:\.[xX*])?|[0-9]+\.[xX*])`)
var packageManagerIntegrityPattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)\+sha512\.([0-9a-f]{128})$`)

// PackageManagerIntegrityRequirement returns an exact pnpm distribution pin.
// Its digest is SHA-512 of the complete npm package archive, as in Corepack's
// packageManager field. Numeric version matching alone cannot prove this pin.
func PackageManagerIntegrityRequirement(value string) (version, sha512 string, ok bool) {
	match := packageManagerIntegrityPattern.FindStringSubmatch(value)
	if match == nil {
		return "", "", false
	}
	parsed, err := parseRuntimeVersion(match[1])
	if err != nil || parsed.count != 3 || parsed.String() != match[1] {
		return "", "", false
	}
	return match[1], match[2], true
}

type runtimeVersion struct {
	parts [3]int
	count int
}
type runtimeTerm struct {
	op      string
	version runtimeVersion
}

func parseRuntimeVersion(value string) (runtimeVersion, error) {
	var result runtimeVersion
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) > 3 || len(parts) == 0 {
		return result, fmt.Errorf("expected a numeric runtime version")
	}
	for i, part := range parts {
		if (part == "x" || part == "X" || part == "*") && i == len(parts)-1 && i > 0 {
			break
		}
		if part == "" || len(part) > 6 || len(part) > 1 && part[0] == '0' {
			return result, fmt.Errorf("invalid numeric runtime version")
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return result, fmt.Errorf("invalid numeric runtime version")
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return result, err
		}
		result.parts[i] = value
		result.count++
	}
	return result, nil
}

func (version runtimeVersion) String() string {
	parts := make([]string, version.count)
	for i := range parts {
		parts[i] = strconv.Itoa(version.parts[i])
	}
	return strings.Join(parts, ".")
}

func parseRuntimeRequirement(value string) ([][]runtimeTerm, string, error) {
	if len(value) > 512 || strings.TrimSpace(value) == "" {
		return nil, "", fmt.Errorf("runtime requirement is empty or too long")
	}
	branches := strings.Split(value, "||")
	if len(branches) > 8 {
		return nil, "", fmt.Errorf("runtime requirement has too many alternatives")
	}
	var parsed [][]runtimeTerm
	var canonical []string
	for _, branch := range branches {
		remaining := strings.TrimSpace(branch)
		var terms []runtimeTerm
		var normalized []string
		for remaining != "" {
			match := runtimeTermPattern.FindStringSubmatch(remaining)
			if match == nil {
				return nil, "", fmt.Errorf("unsupported runtime requirement %q; use numeric versions, comparisons, ^, ~, comma/space conjunctions or || alternatives", value)
			}
			version, err := parseRuntimeVersion(match[2])
			if err != nil {
				return nil, "", err
			}
			op := match[1]
			if (op == "=" || op == "==") && strings.ContainsAny(match[2], "xX*") {
				op = ""
			} else if (op == "=" || op == "==" || op == "!=") && version.count < 3 {
				version.count = 3
			}
			if op == "==" {
				op = "="
			}
			if (op == "<" || op == ">" || op == "<=" || op == ">=" || op == "!=" || op == "^" || op == "~") && strings.ContainsAny(match[2], "xX*") {
				return nil, "", fmt.Errorf("wildcards cannot be combined with runtime comparison operators")
			}
			terms = append(terms, runtimeTerm{op: op, version: version})
			normalized = append(normalized, op+version.String())
			if len(terms) > 16 {
				return nil, "", fmt.Errorf("runtime requirement has too many comparisons")
			}
			tail := remaining[len(match[0]):]
			if tail != "" && tail[0] != ',' && tail[0] != ' ' && tail[0] != '\t' {
				return nil, "", fmt.Errorf("unsupported runtime requirement %q", value)
			}
			remaining = strings.TrimSpace(tail)
			if strings.HasPrefix(remaining, ",") {
				remaining = strings.TrimSpace(remaining[1:])
				if remaining == "" {
					return nil, "", fmt.Errorf("empty runtime comparison")
				}
			}
		}
		if len(terms) == 0 {
			return nil, "", fmt.Errorf("empty runtime alternative")
		}
		parsed = append(parsed, terms)
		canonical = append(canonical, strings.Join(normalized, ","))
	}
	return parsed, strings.Join(canonical, " || "), nil
}

// NormalizeRuntimeRequirements rejects moving aliases and unsupported syntax.
// A bare major/minor selects that series; a full version selects the exact
// advertised version. The installed keyword preserves the image default. Comparisons always use numeric three-part ordering.
func NormalizeRuntimeRequirements(requirements map[string]string) (map[string]string, error) {
	if len(requirements) > len(runtimeNames) {
		return nil, fmt.Errorf("too many runtime requirements")
	}
	result := make(map[string]string, len(requirements))
	for name, value := range requirements {
		if !runtimeNames[name] {
			return nil, fmt.Errorf("unsupported runtime %q", name)
		}
		if value == "installed" {
			result[name] = value
			continue
		}
		if _, _, pinned := PackageManagerIntegrityRequirement(value); pinned && name == "pnpm" {
			result[name] = value
			continue
		}
		branches, normalized, err := parseRuntimeRequirement(value)
		if err != nil {
			return nil, fmt.Errorf("runtime %s: %w", name, err)
		}
		if !runtimeRequirementHasVersion(branches) {
			return nil, fmt.Errorf("runtime %s has incompatible version requirements", name)
		}
		result[name] = normalized
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func compareRuntimeVersions(a, b runtimeVersion) int {
	for i := range a.parts {
		if a.parts[i] < b.parts[i] {
			return -1
		}
		if a.parts[i] > b.parts[i] {
			return 1
		}
	}
	return 0
}

func runtimeTermMatches(version runtimeVersion, term runtimeTerm) bool {
	comparison := compareRuntimeVersions(version, term.version)
	switch term.op {
	case ">=":
		return comparison >= 0
	case "<=":
		return comparison <= 0
	case ">":
		return comparison > 0
	case "<":
		return comparison < 0
	case "!=":
		return comparison != 0
	case "^", "~":
		upper := term.version
		index := 0
		if term.op == "~" && term.version.count >= 2 {
			index = 1
		} else if term.op == "^" {
			for index < term.version.count-1 && term.version.parts[index] == 0 {
				index++
			}
		}
		upper.parts[index]++
		for i := index + 1; i < 3; i++ {
			upper.parts[i] = 0
		}
		return comparison >= 0 && compareRuntimeVersions(version, upper) < 0
	default:
		for i := 0; i < term.version.count; i++ {
			if version.parts[i] != term.version.parts[i] {
				return false
			}
		}
		return true
	}
}

func RuntimeVersionSatisfies(version, requirement string) bool {
	parsed, err := parseRuntimeVersion(version)
	if err != nil || parsed.count != 3 {
		return false
	}
	if requirement == "installed" {
		return true
	}
	if exact, _, pinned := PackageManagerIntegrityRequirement(requirement); pinned {
		return version == exact
	}
	branches, _, err := parseRuntimeRequirement(requirement)
	if err != nil {
		return false
	}
	for _, branch := range branches {
		matched := true
		for _, term := range branch {
			matched = matched && runtimeTermMatches(parsed, term)
		}
		if matched {
			return true
		}
	}
	return false
}

func NormalizeRuntimeInventory(inventory RuntimeInventory) (RuntimeInventory, error) {
	if len(inventory) > len(runtimeNames) {
		return nil, fmt.Errorf("too many runtime inventory entries")
	}
	result := make(RuntimeInventory, len(inventory))
	for name, versions := range inventory {
		if !runtimeNames[name] || len(versions) == 0 || len(versions) > 32 {
			return nil, fmt.Errorf("invalid runtime inventory for %q", name)
		}
		seen := map[string]bool{}
		for _, raw := range versions {
			version, err := parseRuntimeVersion(raw)
			if err != nil || version.count != 3 || raw != version.String() {
				return nil, fmt.Errorf("runtime %s inventory must contain exact normalized versions", name)
			}
			if seen[raw] {
				return nil, fmt.Errorf("duplicate runtime inventory version")
			}
			seen[raw] = true
			result[name] = append(result[name], raw)
		}
		sort.Slice(result[name][1:], func(i, j int) bool {
			a, _ := parseRuntimeVersion(result[name][i+1])
			b, _ := parseRuntimeVersion(result[name][j+1])
			return compareRuntimeVersions(a, b) < 0
		})
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func NormalizeImageRuntimeInventory(inventories map[string]RuntimeInventory) (map[string]RuntimeInventory, error) {
	if len(inventories) > 64 {
		return nil, fmt.Errorf("too many image runtime inventories")
	}
	result := make(map[string]RuntimeInventory, len(inventories))
	for image, inventory := range inventories {
		if len(image) > 256 || image == "" || strings.TrimSpace(image) != image || strings.ContainsAny(image, "\r\n\x00") {
			return nil, fmt.Errorf("invalid runtime inventory image")
		}
		normalized, err := NormalizeRuntimeInventory(inventory)
		if err != nil {
			return nil, err
		}
		result[image] = normalized
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func SelectRuntimeVersions(requirements map[string]string, inventory RuntimeInventory) (map[string]string, error) {
	normalized, err := NormalizeRuntimeRequirements(requirements)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(normalized))
	for name, requirement := range normalized {
		if _, _, pinned := PackageManagerIntegrityRequirement(requirement); pinned {
			return nil, fmt.Errorf("runtime %s requires distribution integrity verification; installed version inventory cannot prove %s", name, requirement)
		}
		if requirement == "installed" && len(inventory[name]) > 0 {
			result[name] = inventory[name][0]
			continue
		}
		for _, candidate := range inventory[name] {
			if RuntimeVersionSatisfies(candidate, requirement) {
				current := result[name]
				a, _ := parseRuntimeVersion(candidate)
				b, _ := parseRuntimeVersion(current)
				if current == "" || compareRuntimeVersions(a, b) > 0 {
					result[name] = candidate
				}
			}
		}
		if result[name] == "" {
			return nil, fmt.Errorf("runtime %s requires %s; selected image advertises %v", name, requirement, inventory[name])
		}
	}
	return result, nil
}

func RuntimeRequirementMismatchReasons(requirements map[string]string, inventory RuntimeInventory) []string {
	normalized, err := NormalizeRuntimeRequirements(requirements)
	if err != nil {
		return []string{err.Error()}
	}
	var reasons []string
	var names []string
	for name := range normalized {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, err := SelectRuntimeVersions(map[string]string{name: normalized[name]}, inventory); err != nil {
			reasons = append(reasons, err.Error())
		}
	}
	return reasons
}

func CloneRuntimeRequirements(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
func CloneRuntimeInventory(inventory RuntimeInventory) RuntimeInventory {
	if inventory == nil {
		return nil
	}
	result := make(RuntimeInventory, len(inventory))
	for name, versions := range inventory {
		result[name] = append([]string(nil), versions...)
	}
	return result
}
func CloneImageRuntimeInventory(inventories map[string]RuntimeInventory) map[string]RuntimeInventory {
	if inventories == nil {
		return nil
	}
	result := make(map[string]RuntimeInventory, len(inventories))
	for image, inventory := range inventories {
		result[image] = CloneRuntimeInventory(inventory)
	}
	return result
}

// MergeRuntimeRequirements preserves conjunction across independent declarations,
// distributing alternatives instead of silently selecting one declaration.
func MergeRuntimeRequirements(left, right map[string]string) (map[string]string, error) {
	result := CloneRuntimeRequirements(left)
	if result == nil {
		result = map[string]string{}
	}
	for name, value := range right {
		if previous := result[name]; previous != "" && previous != "installed" && value != "installed" {
			previousVersion, previousDigest, previousPinned := PackageManagerIntegrityRequirement(previous)
			version, digest, pinned := PackageManagerIntegrityRequirement(value)
			if previousPinned || pinned {
				if previousPinned && pinned && (previousVersion != version || previousDigest != digest) {
					return nil, fmt.Errorf("runtime %s has conflicting package-manager integrity pins", name)
				}
				if previousPinned {
					if !RuntimeVersionSatisfies(previousVersion, value) {
						return nil, fmt.Errorf("runtime %s integrity pin conflicts with version requirement", name)
					}
					value = previous
				} else if !RuntimeVersionSatisfies(version, previous) {
					return nil, fmt.Errorf("runtime %s integrity pin conflicts with version requirement", name)
				}
				result[name] = value
				continue
			}
			var branches []string
			for _, a := range strings.Split(previous, "||") {
				for _, b := range strings.Split(value, "||") {
					branches = append(branches, strings.TrimSpace(a)+","+strings.TrimSpace(b))
				}
			}
			value = strings.Join(branches, " || ")
		}
		if value != "installed" || result[name] == "" {
			result[name] = value
		}
	}
	return NormalizeRuntimeRequirements(result)
}

// NormalizeNodeRuntimeRequirement translates the supported npm/node-semver
// subset into this protocol's numeric grammar. In particular, partial > and <=
// bounds advance the omitted series, while partial = selects the whole series.
func NormalizeNodeRuntimeRequirement(value string) (string, error) {
	return normalizeSemverRuntimeRequirement("node", "npm", value)
}

// NormalizeSetupRuntimeRequirement translates supported setup-action semver
// ranges without resolving moving aliases or package-manager integrity pins.
// Partial comparison bounds follow the setup actions' series semantics.
func NormalizeSetupRuntimeRequirement(runtime, value string) (string, error) {
	switch runtime {
	case "node", "go", "python", "pnpm":
		normalized, err := normalizeSemverRuntimeRequirement(runtime, runtime, value)
		if err != nil {
			return "", fmt.Errorf("runtime %s: %w", runtime, err)
		}
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported setup runtime %q", runtime)
	}
}

func normalizeSemverRuntimeRequirement(runtime, label, value string) (string, error) {
	if strings.Contains(value, ",") {
		return "", fmt.Errorf("comma-separated %s ranges are unsupported", label)
	}
	var alternatives []string
	for _, branch := range strings.Split(value, "||") {
		remaining := strings.TrimSpace(branch)
		var terms []string
		for remaining != "" {
			match := runtimeTermPattern.FindStringSubmatch(remaining)
			if match == nil {
				return "", fmt.Errorf("unsupported %s runtime requirement %q", label, value)
			}
			version, err := parseRuntimeVersion(match[2])
			if err != nil {
				return "", err
			}
			op := match[1]
			if op == "!=" || op == "==" {
				return "", fmt.Errorf("unsupported %s runtime comparator %q", label, op)
			}
			if strings.ContainsAny(match[2], "xX*") && op != "" && op != "=" {
				return "", fmt.Errorf("unsupported %s wildcard comparison", label)
			}
			if op == "=" && version.count < 3 {
				op = ""
			}
			if version.count < 3 && (op == ">" || op == "<=") {
				next := version
				next.parts[next.count-1]++
				next.count = 3
				if op == ">" {
					op = ">="
				} else {
					op = "<"
				}
				version = next
			}
			terms = append(terms, op+version.String())
			tail := remaining[len(match[0]):]
			if tail != "" && tail[0] != ' ' && tail[0] != '\t' {
				return "", fmt.Errorf("unsupported %s runtime requirement %q", label, value)
			}
			remaining = strings.TrimSpace(tail)
		}
		if len(terms) == 0 {
			return "", fmt.Errorf("empty %s runtime alternative", label)
		}
		alternatives = append(alternatives, strings.Join(terms, ","))
	}
	normalized, err := NormalizeRuntimeRequirements(map[string]string{runtime: strings.Join(alternatives, " || ")})
	if err != nil {
		return "", err
	}
	return normalized[runtime], nil
}

// Numeric conjunctions form bounded intervals and explicit exclusions. Testing
// each written boundary and its adjacent version/series detects contradictions
// before a session can wait forever for a worker that cannot exist.
func runtimeRequirementHasVersion(branches [][]runtimeTerm) bool {
	for _, branch := range branches {
		candidates := []runtimeVersion{{count: 3}}
		for _, term := range branch {
			base := term.version
			base.count = 3
			candidates = append(candidates, base)
			for index := 0; index < 3; index++ {
				next := base
				next.parts[index]++
				for trailing := index + 1; trailing < 3; trailing++ {
					next.parts[trailing] = 0
				}
				candidates = append(candidates, next)
			}
		}
		for _, candidate := range candidates {
			valid := true
			for _, part := range candidate.parts {
				valid = valid && part <= 999999
			}
			if !valid {
				continue
			}
			matched := true
			for _, term := range branch {
				matched = matched && runtimeTermMatches(candidate, term)
			}
			if matched {
				return true
			}
		}
	}
	return false
}

// RuntimeRequirementImplies reports whether the configured numeric constraint
// preserves a source declaration. It checks all interval boundaries and adjacent
// series; it does not consult a local executable or resolve a moving alias.
func RuntimeRequirementImplies(configured, required string) bool {
	configuredVersion, configuredDigest, configuredPinned := PackageManagerIntegrityRequirement(configured)
	requiredVersion, requiredDigest, requiredPinned := PackageManagerIntegrityRequirement(required)
	if requiredPinned {
		return configuredPinned && configuredVersion == requiredVersion && configuredDigest == requiredDigest
	}
	if configuredPinned {
		configured = configuredVersion
	}
	if required == "installed" {
		_, err := NormalizeRuntimeRequirements(map[string]string{"node": configured})
		return err == nil
	}
	if configured == "installed" {
		return false
	}
	left, _, err := parseRuntimeRequirement(configured)
	if err != nil {
		return false
	}
	right, _, err := parseRuntimeRequirement(required)
	if err != nil {
		return false
	}
	candidates := []runtimeVersion{{count: 3}}
	for _, branches := range [][][]runtimeTerm{left, right} {
		for _, branch := range branches {
			for _, term := range branch {
				base := term.version
				base.count = 3
				candidates = append(candidates, base)
				for index := 0; index < 3; index++ {
					next := base
					next.parts[index]++
					for trailing := index + 1; trailing < 3; trailing++ {
						next.parts[trailing] = 0
					}
					candidates = append(candidates, next)
				}
			}
		}
	}
	for _, candidate := range candidates {
		if RuntimeVersionSatisfies(candidate.String(), configured) && !RuntimeVersionSatisfies(candidate.String(), required) {
			return false
		}
	}
	return true
}
