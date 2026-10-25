package artifactbundle

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRetentionManifestValidationBindsWholeRetainedArchive(t *testing.T) {
	base := Retention{SchemaVersion: RetentionSchemaVersion, MatchedFileCount: 2, RetainedFileCount: 1,
		OmittedFileCount: 1, OmittedReportCount: 1, OmittedFiles: []OmittedFile{{Path: "reports/TEST-lost.xml", SizeBytes: 120, Reason: "bundle_limit"}}}
	for _, tc := range []struct {
		name   string
		mutate func(*Retention)
		files  []ArchiveFile
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "unmatched file and directory", valid: true, mutate: func(r *Retention) { r.UnmatchedPaths = []string{"reports/missing.xml", "empty"} }},
		{name: "unmatched retained file", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"reports/TEST-kept.xml"} }},
		{name: "unmatched omitted directory", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"reports"} }},
		{name: "unmatched omitted file", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"reports/TEST-lost.xml"} }},
		{name: "unmatched optional workspace", mutate: func(r *Retention) { r.UnmatchedPaths = []string{".readyci/failed-workspace.tar.gz"} }},
		{name: "unmatched traversal", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"../outside"} }},
		{name: "unmatched glob", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"missing/**"} }},
		{name: "unmatched duplicate", mutate: func(r *Retention) { r.UnmatchedPaths = []string{"missing.xml", "missing.xml"} }},
		{name: "invalid retained count", mutate: func(r *Retention) { r.RetainedFileCount = 0; r.MatchedFileCount = 1 }},
		{name: "omitted retained file", mutate: func(r *Retention) { r.OmittedFiles[0].Path = "reports/TEST-kept.xml" }},
		{name: "forged optional omission", mutate: func(r *Retention) { r.FailedWorkspaceOmitted = true }},
		{name: "out of workspace", mutate: func(r *Retention) { r.OmittedFiles[0].Path = "../outside.xml" }},
		{name: "wrong report count", mutate: func(r *Retention) { r.OmittedReportCount = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.OmittedFiles = append([]OmittedFile(nil), base.OmittedFiles...)
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if _, err := WriteArchive(&archive, []ArchiveFile{{Path: RetentionManifestPath, Data: data}, {Path: "reports/TEST-kept.xml", Data: []byte("kept")}}); err != nil {
				t.Fatal(err)
			}
			manifest, err := Validate(bytes.NewReader(archive.Bytes()))
			if tc.valid {
				if err != nil || manifest.Retention == nil || !manifest.Retention.RequiredOmissions() {
					t.Fatalf("manifest=%+v err=%v", manifest, err)
				}
				if !reflect.DeepEqual(manifest.Retention.UnmatchedPaths, r.UnmatchedPaths) {
					t.Fatalf("unmatched paths lost in archive validation: %v", manifest.Retention.UnmatchedPaths)
				}
			} else if err == nil {
				t.Fatal("invalid retention metadata accepted")
			}
		})
	}
}

func TestUnmatchedLiteralPathsUsesCompleteDirectorySelection(t *testing.T) {
	files := []File{{Path: "reports-old"}, {Path: "reports/result.xml"}}
	paths := []string{"./reports", "reports-old", "reports/missing.xml", "reports/**/*.xml", "empty", "./reports/missing.xml", ".readyci/failed-workspace.tar.gz"}
	unmatched, err := UnmatchedLiteralPaths(paths, files)
	if err != nil || !reflect.DeepEqual(unmatched, []string{"reports/missing.xml", "empty"}) {
		t.Fatalf("unmatched=%v err=%v", unmatched, err)
	}
	if _, err := UnmatchedLiteralPaths([]string{"../outside"}, files); err == nil {
		t.Fatal("invalid configured path was accepted")
	}
}

func TestRetentionManifestDecoderIsBoundedAndStrict(t *testing.T) {
	for _, data := range [][]byte{bytes.Repeat([]byte(" "), MaxRetentionManifestBytes+1), []byte(`{"schema_version":"unknown"}`), []byte(`{} {}`)} {
		if _, err := DecodeRetention(data); err == nil {
			t.Fatal("unbounded or invalid retention metadata accepted")
		}
	}
	if (*Retention)(nil).RequiredOmissions() {
		t.Fatal("nil collection metadata requires omission")
	}
	r := Retention{SchemaVersion: RetentionSchemaVersion, MatchedFileCount: 1, OmittedFileCount: 1, FailedWorkspaceOmitted: true,
		OmittedFiles: []OmittedFile{{Path: ".readyci/failed-workspace.tar.gz", SizeBytes: 1, Reason: "bundle_limit"}}}
	data, _ := json.Marshal(r)
	decoded, err := DecodeRetention(data)
	if err != nil || decoded.RequiredOmissions() {
		t.Fatalf("optional omission changed: %+v %v", decoded, err)
	}
}
