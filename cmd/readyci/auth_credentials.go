package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func saveStoredCredential(credential storedCredential) error {
	credential.APIURL = normalizeAPIURL(credential.APIURL)
	credential.Token = strings.TrimSpace(credential.Token)
	if credential.APIURL == "" || credential.Token == "" {
		return cliConfigError("cannot store incomplete credential", nil)
	}
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = time.Now().UTC()
	}
	file, err := loadCredentialsFile()
	if err != nil {
		return err
	}
	replaced := false
	for i := range file.Credentials {
		if normalizeAPIURL(file.Credentials[i].APIURL) == credential.APIURL {
			file.Credentials[i] = credential
			replaced = true
			break
		}
	}
	if !replaced {
		file.Credentials = append(file.Credentials, credential)
	}
	return writeCredentialsFile(file)
}

func loadStoredCredential(apiURL string) (storedCredential, bool, error) {
	file, err := loadCredentialsFile()
	if err != nil {
		return storedCredential{}, false, err
	}
	apiURL = normalizeAPIURL(apiURL)
	for _, credential := range file.Credentials {
		if normalizeAPIURL(credential.APIURL) == apiURL && strings.TrimSpace(credential.Token) != "" {
			credential.APIURL = apiURL
			return credential, true, nil
		}
	}
	return storedCredential{}, false, nil
}

func removeStoredCredential(apiURL string) (bool, error) {
	file, err := loadCredentialsFile()
	if err != nil {
		return false, err
	}
	apiURL = normalizeAPIURL(apiURL)
	out := file.Credentials[:0]
	removed := false
	for _, credential := range file.Credentials {
		if normalizeAPIURL(credential.APIURL) == apiURL {
			removed = true
			continue
		}
		out = append(out, credential)
	}
	file.Credentials = out
	if !removed {
		return false, nil
	}
	return true, writeCredentialsFile(file)
}

func loadCredentialsFile() (credentialsFile, error) {
	path, err := credentialsPath()
	if err != nil {
		return credentialsFile{}, err
	}
	file := credentialsFile{Version: credentialsFileVersion}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return credentialsFile{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return file, nil
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return credentialsFile{}, fmt.Errorf("read credentials: %w", err)
	}
	if file.Version == 0 {
		file.Version = credentialsFileVersion
	}
	return file, nil
}

func writeCredentialsFile(file credentialsFile) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file.Version = credentialsFileVersion
	tmp, err := os.CreateTemp(dir, ".credentials-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return os.Chmod(path, 0o600)
}

func credentialsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("READYCI_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "credentials.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "readyci", "credentials.json"), nil
}

func normalizeAPIURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return strings.TrimRight(parsed.String(), "/")
	}
	return strings.TrimRight(value, "/")
}

func randomAuthToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
