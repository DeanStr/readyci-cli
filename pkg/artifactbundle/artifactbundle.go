package artifactbundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
)

const (
	MediaType          = "application/vnd.readyci.artifacts+tar+gzip"
	MaxCompressedBytes = 100 << 20
	MaxExpandedBytes   = 100 << 20
	MaxEntryCount      = 10000
	// Include tar headers, PAX/GNU metadata, and padding in a finite stream
	// budget while preserving the full file-content allowance.
	MaxTarStreamBytes        = MaxExpandedBytes + (16 << 20)
	maxTarEOFZeroPaddingSize = 1 << 20
)

var (
	ErrTooLarge     = errors.New("artifact bundle too large")
	ErrTrailingData = errors.New("artifact archive contains trailing data after tar EOF")
)

type File struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type Manifest struct {
	Files              []File     `json:"files"`
	TotalExpandedBytes int64      `json:"total_expanded_bytes"`
	Retention          *Retention `json:"retention,omitempty"`
}

type ArchiveFile struct {
	Path    string
	Mode    int64
	ModTime time.Time
	Data    []byte
}

func WriteArchive(writer io.Writer, files []ArchiveFile) (Manifest, error) {
	normalizedFiles := make([]ArchiveFile, 0, len(files))
	archivePaths := make([]string, 0, len(files))
	seen := make(map[string]struct{})
	var manifest Manifest
	var payloadBytes int64
	payloadFiles := 0
	for _, file := range files {
		rel, err := ValidateArchivePath(file.Path)
		if err != nil {
			return Manifest{}, err
		}
		if _, ok := seen[rel]; ok {
			return Manifest{}, fmt.Errorf("duplicate artifact path %q", rel)
		}
		seen[rel] = struct{}{}
		archivePaths = append(archivePaths, rel)
		file.Path = rel
		if file.Mode == 0 {
			file.Mode = 0o644
		}
		if rel == RetentionManifestPath {
			if len(file.Data) > MaxRetentionManifestBytes {
				return Manifest{}, ErrTooLarge
			}
		} else {
			if int64(len(file.Data)) > MaxExpandedBytes-payloadBytes || payloadFiles >= MaxEntryCount {
				return Manifest{}, ErrTooLarge
			}
			payloadBytes += int64(len(file.Data))
			payloadFiles++
		}
		normalizedFiles = append(normalizedFiles, file)
		manifest.Files = append(manifest.Files, File{Path: rel, SizeBytes: int64(len(file.Data))})
		manifest.TotalExpandedBytes += int64(len(file.Data))
	}
	if parent, nested, ok := FindPathPrefixConflict(archivePaths); ok {
		return Manifest{}, fmt.Errorf("artifact path %q conflicts with nested artifact path %q", parent, nested)
	}

	gzipWriter := gzip.NewWriter(writer)
	gzipWriter.Name = "artifacts.tar"
	tarWriter := tar.NewWriter(NewTarStreamWriter(context.Background(), gzipWriter))
	closed := false
	defer func() {
		if !closed {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
		}
	}()

	for _, file := range normalizedFiles {
		header := &tar.Header{
			Name:    file.Path,
			Mode:    file.Mode,
			Size:    int64(len(file.Data)),
			ModTime: file.ModTime,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return Manifest{}, err
		}
		if _, err := tarWriter.Write(file.Data); err != nil {
			return Manifest{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return Manifest{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return Manifest{}, err
	}
	closed = true
	return manifest, nil
}

func Validate(reader io.Reader) (Manifest, error) {
	return ValidateContext(context.Background(), reader)
}

// ValidateContext validates an artifact archive with cancellation during parsing.
func ValidateContext(ctx context.Context, reader io.Reader) (Manifest, error) {
	gzipReader, err := gzip.NewReader(io.LimitReader(reader, MaxCompressedBytes+1))
	if err != nil {
		return Manifest{}, err
	}
	defer gzipReader.Close()

	stream := NewTarStreamReader(ctx, gzipReader)
	tarReader := tar.NewReader(stream)
	var manifest Manifest
	var payloadBytes int64
	payloadFiles := 0
	seen := make(map[string]struct{})
	var archivePaths []string
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			if err := VerifyGzipFooter(stream); err != nil {
				return Manifest{}, err
			}
			if parent, nested, ok := FindPathPrefixConflict(archivePaths); ok {
				return Manifest{}, fmt.Errorf("artifact path %q conflicts with nested artifact path %q", parent, nested)
			}
			if err := ValidateRetentionFiles(manifest.Retention, manifest.Files); err != nil {
				return Manifest{}, err
			}
			return manifest, nil
		}
		if err != nil {
			return Manifest{}, err
		}
		rel, err := validateArchivePath(header.Name)
		if err != nil {
			return Manifest{}, err
		}
		if header.Typeflag != tar.TypeReg {
			return Manifest{}, fmt.Errorf("artifact %q is not a regular file", rel)
		}
		if _, ok := seen[rel]; ok {
			return Manifest{}, fmt.Errorf("duplicate artifact path %q", rel)
		}
		seen[rel] = struct{}{}
		archivePaths = append(archivePaths, rel)
		if header.Size < 0 {
			return Manifest{}, fmt.Errorf("artifact %q has invalid size", rel)
		}
		if rel != RetentionManifestPath {
			if header.Size > MaxExpandedBytes-payloadBytes || payloadFiles >= MaxEntryCount {
				return Manifest{}, ErrTooLarge
			}
			payloadBytes += header.Size
			payloadFiles++
		}
		var n int64
		if rel == RetentionManifestPath {
			if header.Size > MaxRetentionManifestBytes {
				return Manifest{}, fmt.Errorf("artifact retention manifest exceeds its byte limit")
			}
			data, readErr := io.ReadAll(tarReader)
			if readErr != nil {
				return Manifest{}, readErr
			}
			manifest.Retention, err = DecodeRetention(data)
			n = int64(len(data))
		} else {
			n, err = io.Copy(io.Discard, tarReader)
		}
		if err != nil {
			return Manifest{}, err
		}
		if n != header.Size {
			return Manifest{}, io.ErrUnexpectedEOF
		}
		manifest.Files = append(manifest.Files, File{Path: rel, SizeBytes: header.Size})
		manifest.TotalExpandedBytes += header.Size
	}
}

// NewTarStreamReader bounds all decompressed bytes, including metadata that
// archive/tar consumes inside Next without returning a visible file header.
func NewTarStreamReader(ctx context.Context, reader io.Reader) io.Reader {
	return &tarStreamReader{ctx: ctx, reader: reader, remaining: MaxTarStreamBytes}
}

// NewTarStreamWriter applies the consumer's complete tar budget to producers,
// so a successful archive cannot exceed the limit because of metadata/padding.
func NewTarStreamWriter(ctx context.Context, writer io.Writer) io.Writer {
	return &tarStreamWriter{ctx: ctx, writer: writer, remaining: MaxTarStreamBytes}
}

type tarStreamWriter struct {
	ctx       context.Context
	writer    io.Writer
	remaining int64
}

func (w *tarStreamWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.remaining {
		return 0, ErrTooLarge
	}
	n, err := w.writer.Write(p)
	w.remaining -= int64(n)
	return n, err
}

type tarStreamReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
}

func (r *tarStreamReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func VerifyGzipFooter(reader io.Reader) error {
	var buf [32 << 10]byte
	var paddingBytes int64
	for {
		n, err := reader.Read(buf[:])
		if n > 0 {
			paddingBytes += int64(n)
			if paddingBytes > maxTarEOFZeroPaddingSize {
				return ErrTrailingData
			}
			for _, b := range buf[:n] {
				if b != 0 {
					return ErrTrailingData
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrTrailingData
		}
	}
}

func IsFailedWorkspaceArtifactPattern(pattern string) bool {
	normalized, err := NormalizePattern(pattern)
	return err == nil && normalized == guestcontract.FailedWorkspaceArtifactPath
}

func PatternWalkRoots(patterns []string) []string {
	seen := make(map[string]struct{})
	roots := make([]string, 0, len(patterns))
	add := func(root string) {
		root = strings.Trim(root, "/")
		if root == "." {
			root = ""
		}
		if root == "" {
			seen = map[string]struct{}{"": {}}
			roots = []string{""}
			return
		}
		if _, fullWalk := seen[""]; fullWalk {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	for _, pattern := range patterns {
		add(artifactWalkRoot(pattern))
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i] == "" {
			return true
		}
		if roots[j] == "" {
			return false
		}
		return roots[i] < roots[j]
	})
	return roots
}

func artifactWalkRoots(patterns []string) []string {
	return PatternWalkRoots(patterns)
}

func artifactWalkRoot(pattern string) string {
	parts := strings.Split(pattern, "/")
	literal := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "**" || hasPatternMeta(part) {
			break
		}
		literal = append(literal, part)
	}
	if len(literal) == 0 {
		return ""
	}
	return strings.Join(literal, "/")
}

func NormalizePatterns(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	seen := make(map[string]struct{})
	for _, pattern := range patterns {
		normalized, err := NormalizePattern(pattern)
		if err != nil {
			return nil, err
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizePatterns(patterns []string) ([]string, error) {
	return NormalizePatterns(patterns)
}

func NormalizePattern(pattern string) (string, error) {
	pattern = strings.TrimSpace(slashPath(pattern))
	pattern = strings.TrimPrefix(pattern, "./")
	if pattern == "" {
		return "", nil
	}
	if hasControlCharacter(pattern) {
		return "", fmt.Errorf("artifact pattern %q must not contain control characters", pattern)
	}
	if strings.HasPrefix(pattern, "../") || pattern == ".." || path.IsAbs(pattern) || hasWindowsVolumePrefix(pattern) {
		return "", fmt.Errorf("artifact pattern %q must be workspace-relative", pattern)
	}
	pattern = strings.Trim(pattern, "/")
	if normalized, ok := normalizeFailedWorkspaceArtifactPattern(pattern); ok {
		return normalized, nil
	}
	if hasParentPathSegment(pattern) {
		return "", fmt.Errorf("artifact pattern %q is not allowed", pattern)
	}
	cleaned := path.Clean(pattern)
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || IsGitControlPath(cleaned) {
		return "", fmt.Errorf("artifact pattern %q is not allowed", pattern)
	}
	if err := validatePatternSyntax(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func normalizePattern(pattern string) (string, error) {
	return NormalizePattern(pattern)
}

func validatePatternSyntax(pattern string) error {
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("artifact pattern %q is invalid: %w", pattern, err)
		}
	}
	return nil
}

func ValidateArchivePath(value string) (string, error) {
	// Manifest paths must survive a JSON round trip without replacement or
	// collisions. Unix filenames and GNU tar headers can contain arbitrary bytes.
	if !utf8.ValidString(value) || strings.Contains(value, "\\") {
		return "", fmt.Errorf("invalid artifact path %q", value)
	}
	rel := slashPath(value)
	if hasControlCharacter(rel) {
		return "", fmt.Errorf("invalid artifact path %q", value)
	}
	if rel == "" || path.IsAbs(rel) || hasWindowsVolumePrefix(rel) {
		return "", fmt.Errorf("invalid artifact path %q", value)
	}
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, "../") || IsGitControlPath(cleaned) {
		return "", fmt.Errorf("invalid artifact path %q", value)
	}
	if cleaned != rel {
		return "", fmt.Errorf("artifact path %q is not normalized", value)
	}
	return cleaned, nil
}

func validateArchivePath(value string) (string, error) {
	return ValidateArchivePath(value)
}

func hasControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func hasParentPathSegment(value string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func normalizeFailedWorkspaceArtifactPattern(value string) (string, bool) {
	reservedPath := guestcontract.FailedWorkspaceArtifactPath
	base, file, ok := strings.Cut(reservedPath, "/")
	if !ok {
		return "", false
	}
	pattern := value
	for strings.HasPrefix(pattern, base+"/../") {
		pattern = strings.TrimPrefix(pattern, base+"/../")
	}
	if pattern == base+"/"+file {
		return reservedPath, true
	}
	return "", false
}

func PatternMatches(pattern, rel string) bool {
	if !hasPatternMeta(pattern) {
		return rel == pattern || strings.HasPrefix(rel, pattern+"/")
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

// UnmatchedLiteralPaths identifies configured files or directories that matched
// no selected files. Wildcards and the optional generated workspace archive do
// not produce missing-path warnings. Pass the complete selection before any
// bounded retention removes files.
func UnmatchedLiteralPaths(patterns []string, files []File) ([]string, error) {
	normalized, err := NormalizePatterns(patterns)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	selectedPaths := make([]string, len(files))
	for index, file := range files {
		selectedPaths[index] = file.Path
	}
	sort.Strings(selectedPaths)
	var unmatched []string
	for _, pattern := range normalized {
		if hasPatternMeta(pattern) || IsFailedWorkspaceArtifactPattern(pattern) {
			continue
		}
		index := sort.SearchStrings(selectedPaths, pattern)
		if index < len(selectedPaths) && selectedPaths[index] == pattern {
			continue
		}
		// A sibling such as "report-old" sorts before "report/result.xml".
		// Search the directory prefix separately from the exact file name.
		prefix := pattern + "/"
		index = sort.SearchStrings(selectedPaths, prefix)
		if index == len(selectedPaths) || !strings.HasPrefix(selectedPaths[index], prefix) {
			unmatched = append(unmatched, pattern)
		}
	}
	return unmatched, nil
}

func artifactPatternMatches(pattern, rel string) bool {
	return PatternMatches(pattern, rel)
}

func matchSegments(patterns, parts []string) bool {
	if len(patterns) == 0 {
		return len(parts) == 0
	}
	if patterns[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if matchSegments(patterns[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	ok, err := path.Match(patterns[0], parts[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(patterns[1:], parts[1:])
}

func hasPatternMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func FindPathPrefixConflict(paths []string) (string, string, bool) {
	if len(paths) < 2 {
		return "", "", false
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		previous := sorted[i-1]
		current := sorted[i]
		if strings.HasPrefix(current, previous+"/") {
			return previous, current, true
		}
	}
	return "", "", false
}

func IsGitControlPath(rel string) bool {
	for _, part := range strings.Split(slashPath(rel), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

func isGitControlPath(rel string) bool {
	return IsGitControlPath(rel)
}

func slashPath(value string) string {
	return strings.ReplaceAll(filepath.ToSlash(value), "\\", "/")
}

func hasWindowsVolumePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	c := value[0]
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
