package preflightapi

import (
	"reflect"
	"testing"
)

func TestCloneRunCommandSourceSharesNothing(t *testing.T) {
	if CloneRunCommandSource(nil) != nil {
		t.Fatal("clone of nil is not nil")
	}
	source := &RunCommandSource{
		Command: "go test ./...", Steps: []RunStepSpec{{Name: "test", Command: "go test", Env: map[string]string{"A": "1"}}},
		Env: map[string]string{"B": "2"}, Prepare: []string{"p"}, Post: []string{"q"}, Always: []string{"r"},
		CachePaths: []string{"~/.cache"}, CacheKeyFiles: []string{"go.sum"}, CacheRuntimeRequirements: map[string]string{"go": "1.25"},
		CacheDependencyCheck: "deps", KeepFailed: true,
	}
	clone := CloneRunCommandSource(source)
	if !reflect.DeepEqual(clone, source) {
		t.Fatalf("clone = %+v, want %+v", clone, source)
	}
	clone.Steps[0].Env["A"], clone.Env["B"], clone.Prepare[0], clone.Post[0], clone.Always[0] = "x", "x", "x", "x", "x"
	clone.CachePaths[0], clone.CacheKeyFiles[0], clone.CacheRuntimeRequirements["go"] = "x", "x", "x"
	if source.Steps[0].Env["A"] != "1" || source.Env["B"] != "2" || source.Prepare[0] != "p" || source.Post[0] != "q" || source.Always[0] != "r" ||
		source.CachePaths[0] != "~/.cache" || source.CacheKeyFiles[0] != "go.sum" || source.CacheRuntimeRequirements["go"] != "1.25" {
		t.Fatalf("mutating the clone changed the source: %+v", source)
	}
}
