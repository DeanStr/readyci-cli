package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestConfigFieldMetadataMatchesYAMLStructs(t *testing.T) {
	scopes := map[FieldScope]any{
		ScopeProject:       ProjectConfig{},
		ScopeSync:          SyncConfig{},
		ScopeCache:         CacheConfig{},
		ScopeArtifacts:     ArtifactConfig{},
		ScopeAffected:      AffectedConfig{},
		ScopeAffectedRule:  AffectedRule{},
		ScopeAffectedTest:  AffectedTest{},
		ScopeNetwork:       NetworkConfig{},
		ScopeResources:     ResourceConfig{},
		ScopeResource:      ResourceDefaults{},
		ScopeService:       ServiceConfig{},
		ScopeServiceHealth: ServiceHealthConfig{},
		ScopeCheck:         CheckConfig{},
		ScopeStep:          StepConfig{},
		ScopeMatrix:        MatrixConfig{},
		ScopeWhen:          WhenConfig{},
		ScopeWhenChanges:   WhenChangesConfig{},
	}
	fields := metadataNamesByScope(t, ConfigFieldMetadata())
	for scope, value := range scopes {
		want := yamlFieldNames(value)
		got := fields[scope]
		if diff := compareStringSets(got, want); diff != "" {
			t.Fatalf("%s metadata mismatch: %s", scope, diff)
		}
	}
}

func TestConfigFieldMetadataMatchesJSONSchemaSections(t *testing.T) {
	schema := readConfigSchema(t)
	fields := metadataNamesByScope(t, ConfigFieldMetadata())
	rootProps := schemaObjectProperties(t, schema)
	defs := objectValue(t, schema, "$defs")

	assertPropertiesContainScope(t, rootProps, fields[ScopeProject], ScopeProject)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "sync")), fields[ScopeSync], ScopeSync)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "cache")), fields[ScopeCache], ScopeCache)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "artifacts")), fields[ScopeArtifacts], ScopeArtifacts)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "affected")), fields[ScopeAffected], ScopeAffected)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "network")), fields[ScopeNetwork], ScopeNetwork)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, rootProps, "resources")), fields[ScopeResources], ScopeResources)

	serviceProps := schemaObjectProperties(t, objectValue(t, defs, "service"))
	assertPropertiesContainScope(t, serviceProps, fields[ScopeService], ScopeService)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, serviceProps, "health")), fields[ScopeServiceHealth], ScopeServiceHealth)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, defs, "resource")), fields[ScopeResource], ScopeResource)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, defs, "affectedRule")), fields[ScopeAffectedRule], ScopeAffectedRule)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, defs, "affectedTest")), fields[ScopeAffectedTest], ScopeAffectedTest)

	checkProps := checkObjectProperties(t, objectValue(t, defs, "check"))
	assertPropertiesContainScope(t, checkProps, fields[ScopeCheck], ScopeCheck)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, checkProps, "sync")), fields[ScopeSync], ScopeSync)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, checkProps, "cache")), fields[ScopeCache], ScopeCache)
	stepItems := objectValue(t, objectValue(t, checkProps, "steps"), "items")
	assertPropertiesContainScope(t, schemaObjectProperties(t, stepItems), fields[ScopeStep], ScopeStep)
	assertPropertiesContainScope(t, structuredMatrixProperties(t, objectValue(t, checkProps, "matrix")), fields[ScopeMatrix], ScopeMatrix)
	whenProps := schemaObjectProperties(t, objectValue(t, checkProps, "when"))
	assertPropertiesContainScope(t, whenProps, fields[ScopeWhen], ScopeWhen)
	assertPropertiesContainScope(t, schemaObjectProperties(t, objectValue(t, whenProps, "changes")), fields[ScopeWhenChanges], ScopeWhenChanges)
}

func TestConfigFieldMetadataReportsRuntimeNetworkDefault(t *testing.T) {
	fields := ConfigFieldMetadata()
	found := map[string]bool{
		"network":      false,
		"network.mode": false,
	}
	for _, field := range fields {
		switch field.Path {
		case "network":
			found[field.Path] = true
			if field.Default != "" {
				t.Fatalf("network object default = %q, want empty", field.Default)
			}
		case "network.mode":
			found[field.Path] = true
			if field.Default != preflightapi.DefaultNetworkMode {
				t.Fatalf("network.mode default = %q, want %q", field.Default, preflightapi.DefaultNetworkMode)
			}
		}
	}
	for path, wasFound := range found {
		if !wasFound {
			t.Errorf("metadata missing %q", path)
		}
	}
}

func metadataNamesByScope(t *testing.T, fields []ConfigField) map[FieldScope][]string {
	t.Helper()
	seen := make(map[string]bool, len(fields))
	byScope := make(map[FieldScope][]string)
	for _, field := range fields {
		if strings.TrimSpace(field.Description) == "" {
			t.Fatalf("metadata field %s has empty description", field.Path)
		}
		key := string(field.Scope) + "\x00" + field.Name
		if seen[key] {
			t.Fatalf("duplicate metadata field %s.%s", field.Scope, field.Name)
		}
		seen[key] = true
		byScope[field.Scope] = append(byScope[field.Scope], field.Name)
	}
	for scope := range byScope {
		sort.Strings(byScope[scope])
	}
	return byScope
}

func yamlFieldNames(value any) []string {
	typ := reflect.TypeOf(value)
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		names = append(names, tag)
	}
	sort.Strings(names)
	return names
}

func compareStringSets(got, want []string) string {
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if reflect.DeepEqual(got, want) {
		return ""
	}
	return "got [" + strings.Join(got, ", ") + "], want [" + strings.Join(want, ", ") + "]"
}

func readConfigSchema(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "schema", "readyci.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return schema
}

func assertPropertiesContainScope(t *testing.T, props map[string]any, names []string, scope FieldScope) {
	t.Helper()
	for _, name := range names {
		if _, ok := props[name]; !ok {
			t.Fatalf("schema section for %s missing property %q", scope, name)
		}
	}
}

func schemaObjectProperties(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	return objectValue(t, object, "properties")
}

func checkObjectProperties(t *testing.T, checkDef map[string]any) map[string]any {
	t.Helper()
	oneOf, ok := checkDef["oneOf"].([]any)
	if !ok || len(oneOf) < 2 {
		t.Fatalf("check schema missing oneOf object alternative")
	}
	checkObject, ok := oneOf[1].(map[string]any)
	if !ok {
		t.Fatalf("check schema object alternative has type %T", oneOf[1])
	}
	return schemaObjectProperties(t, checkObject)
}

func structuredMatrixProperties(t *testing.T, matrixDef map[string]any) map[string]any {
	t.Helper()
	anyOf, ok := matrixDef["anyOf"].([]any)
	if !ok || len(anyOf) < 2 {
		t.Fatalf("matrix schema missing structured anyOf alternative")
	}
	matrixObject, ok := anyOf[1].(map[string]any)
	if !ok {
		t.Fatalf("matrix schema structured alternative has type %T", anyOf[1])
	}
	return schemaObjectProperties(t, matrixObject)
}

func objectValue(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("schema key %q has type %T", key, object[key])
	}
	return value
}
