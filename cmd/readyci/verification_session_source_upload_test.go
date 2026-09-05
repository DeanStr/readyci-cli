package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestUploadVerificationSessionSourceUsesDirectObjectStoreContract(t *testing.T) {
	const sessionID = "session_direct_upload"
	const token = "pfl_direct_upload"
	source := []byte("sealed verification source")
	digest := sha256.Sum256(source)
	md5Digest := md5.Sum(source)
	wantSHA256 := hex.EncodeToString(digest[:])
	wantMD5 := base64.StdEncoding.EncodeToString(md5Digest[:])
	var objectUploaded bool
	var prepared, completed bool
	var apiServer *httptest.Server
	apiServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sealed-source":
			if r.Method != http.MethodPut {
				t.Errorf("object method = %s", r.Method)
			}
			if r.Header.Get("Authorization") != "" {
				t.Errorf("same-origin object upload received API authorization")
			}
			if r.Header.Get("X-ReadyCI-Upload") != "sealed-source" {
				t.Errorf("object upload header = %q", r.Header.Get("X-ReadyCI-Upload"))
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read object upload: %v", err)
			}
			if objectUploaded {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			bodyMD5 := md5.Sum(body)
			if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(bodyMD5[:]) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if string(body) != string(source) {
				t.Errorf("object upload body = %q", body)
			}
			objectUploaded = true
			w.WriteHeader(http.StatusOK)
		case "/v1/verification-sessions/" + sessionID + "/source-upload":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("prepare API authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			var request preflightapi.PrepareVerificationSessionSourceUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode prepare request: %v", err)
			}
			if request.SizeBytes != int64(len(source)) || request.ContentSHA256 != wantSHA256 || request.ContentMD5 != wantMD5 {
				t.Errorf("prepare descriptor = %+v", request)
			}
			prepared = true
			_ = json.NewEncoder(w).Encode(preflightapi.PrepareVerificationSessionSourceUploadResponse{
				UploadURL:     apiServer.URL + "/sealed-source",
				UploadMethod:  http.MethodPut,
				UploadHeaders: map[string]string{"X-ReadyCI-Upload": "sealed-source", "Content-MD5": wantMD5},
				CompleteURL:   "/v1/verification-sessions/" + sessionID + "/source-complete",
			})
		case "/v1/verification-sessions/" + sessionID + "/source-complete":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("completion API authorization = %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			var request preflightapi.CompleteVerificationSessionSourceUploadRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode complete request: %v", err)
			}
			if request.SizeBytes != int64(len(source)) {
				t.Errorf("complete descriptor = %+v", request)
			}
			if request.ContentSHA256 != wantSHA256 {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "source_upload_mismatch", "error": "descriptor mismatch"})
				return
			}
			if !objectUploaded {
				t.Error("completion happened before object upload")
			}
			completed = true
			_ = json.NewEncoder(w).Encode(preflightapi.VerificationSession{SessionID: sessionID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	bundlePath := filepath.Join(t.TempDir(), "source.bundle.tar.gz")
	if err := os.WriteFile(bundlePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	client := api.NewClient(apiServer.URL, api.WithToken(token))
	session, err := uploadVerificationSessionSource(
		context.Background(),
		client,
		preflightapi.CreateVerificationSessionResponse{
			Session:                preflightapi.VerificationSession{SessionID: sessionID, State: preflightapi.VerificationStateSealed},
			SourceUploadMode:       preflightapi.VerificationSourceUploadModeDirect,
			SourceUploadPrepareURL: "/v1/verification-sessions/" + sessionID + "/source-upload",
		},
		bundlePath,
		syncbundlebuild.BuildResult{Size: int64(len(source))},
		nil,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionID != sessionID || !prepared || !objectUploaded || !completed {
		t.Fatalf("session=%+v prepared=%t uploaded=%t completed=%t", session, prepared, objectUploaded, completed)
	}
}

func TestUploadVerificationSessionSourceRequiresDirectContract(t *testing.T) {
	client := api.NewClient("https://api.readyci.test")
	_, err := uploadVerificationSessionSource(
		context.Background(),
		client,
		preflightapi.CreateVerificationSessionResponse{
			Session:          preflightapi.VerificationSession{SessionID: "session_missing_contract"},
			SourceUploadMode: preflightapi.VerificationSourceUploadModeDirect,
		},
		"not-opened-when-contract-is-rejected",
		syncbundlebuild.BuildResult{Size: 1},
		nil,
		false,
	)
	var cliErr cliError
	if !errors.As(err, &cliErr) ||
		cliErr.Code != "platform_internal" ||
		cliErr.ExitCode != cliExitInternal ||
		cliErr.Classification != preflightapi.ClassificationPlatformInternalError ||
		cliErr.Retryable ||
		!strings.Contains(cliErr.Message, "incomplete direct object-store") {
		t.Fatalf("direct-contract requirement error = %#v", err)
	}
}

func TestValidateVerificationSourceUploadRequirementsAcceptsServerCopyReceipt(t *testing.T) {
	created := preflightapi.CreateVerificationSessionResponse{
		Session:          preflightapi.VerificationSession{SessionID: "session_server_copy", State: preflightapi.VerificationStateSealed},
		SourceUploadMode: preflightapi.VerificationSourceUploadModeServerCopy,
	}
	required, err := validateVerificationSourceUploadRequirements(created, false)
	if err != nil || required {
		t.Fatalf("server-copy upload requirement = %t, %v; want false, nil", required, err)
	}
}

func TestValidateVerificationSourceUploadRequirementsRejectsUnlabeledReceipt(t *testing.T) {
	created := preflightapi.CreateVerificationSessionResponse{
		Session:                preflightapi.VerificationSession{SessionID: "session_unlabeled", State: preflightapi.VerificationStateSealed},
		SourceUploadPrepareURL: "/v1/verification-sessions/session_unlabeled/source-upload",
	}
	required, err := validateVerificationSourceUploadRequirements(created, false)
	var cliErr cliError
	if required || !errors.As(err, &cliErr) ||
		cliErr.Code != "platform_internal" ||
		cliErr.ExitCode != cliExitInternal ||
		cliErr.Classification != preflightapi.ClassificationPlatformInternalError ||
		cliErr.Retryable ||
		!strings.Contains(cliErr.Message, "unsupported source upload mode") {
		t.Fatalf("unlabeled upload requirement = %t, %#v", required, err)
	}
}

func TestValidateVerificationSourceUploadRequirementsRejectsTerminalProbeReplay(t *testing.T) {
	created := preflightapi.CreateVerificationSessionResponse{
		Session:                preflightapi.VerificationSession{SessionID: "session_terminal_direct", State: preflightapi.VerificationStateTerminal},
		SourceUploadMode:       preflightapi.VerificationSourceUploadModeDirect,
		SourceUploadPrepareURL: "/v1/verification-sessions/session_terminal_direct/source-upload",
	}
	if required, err := validateVerificationSourceUploadRequirements(created, false); err != nil || !required {
		t.Fatalf("require direct contract on terminal direct replay: %v", err)
	}
	_, err := validateVerificationSourceUploadRequirements(created, true)
	var cliErr cliError
	if !errors.As(err, &cliErr) ||
		cliErr.Code != "verification_source_direct_upload_probe_not_executed" ||
		cliErr.ExitCode != cliExitUsageOrConfig ||
		cliErr.Classification != preflightapi.ClassificationStateConflict ||
		cliErr.Retryable {
		t.Fatalf("terminal direct probe replay error = %#v", err)
	}
}
