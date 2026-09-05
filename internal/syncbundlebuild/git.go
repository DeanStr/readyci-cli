package syncbundlebuild

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

var ErrNotGitRepository = errors.New("not a git repository")

func gitOutput(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func gitOutputAllowFailure(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	return cmd.Output()
}

func gitObjects(ctx context.Context, root string, objectIDs []string) ([][]byte, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	uniqueIDs := make([]string, 0, len(objectIDs))
	seen := make(map[string]struct{}, len(objectIDs))
	for _, objectID := range objectIDs {
		if _, ok := seen[objectID]; ok {
			continue
		}
		seen[objectID] = struct{}{}
		uniqueIDs = append(uniqueIDs, objectID)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(uniqueIDs, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	abort := func(cause error) ([][]byte, error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, cause
	}

	reader := bufio.NewReader(stdout)
	objectsByID := make(map[string][]byte, len(uniqueIDs))
	for _, requestedID := range uniqueIDs {
		header, err := reader.ReadString('\n')
		if err != nil {
			return abort(fmt.Errorf("read git object %s header: %w", requestedID, err))
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != requestedID || fields[1] != "blob" {
			return abort(fmt.Errorf("git object %s returned invalid batch header %q", requestedID, strings.TrimSpace(header)))
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 || size > int64(int(^uint(0)>>1)) {
			return abort(fmt.Errorf("git object %s returned invalid size %q", requestedID, fields[2]))
		}
		data := make([]byte, int(size))
		if _, err := io.ReadFull(reader, data); err != nil {
			return abort(fmt.Errorf("read git object %s body: %w", requestedID, err))
		}
		separator, err := reader.ReadByte()
		if err != nil {
			return abort(fmt.Errorf("read git object %s separator: %w", requestedID, err))
		}
		if separator != '\n' {
			return abort(fmt.Errorf("git object %s returned invalid separator", requestedID))
		}
		objectsByID[requestedID] = data
	}
	if err := cmd.Wait(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, fmt.Errorf("git cat-file --batch failed: %w", err)
		}
		return nil, fmt.Errorf("git cat-file --batch failed: %w: %s", err, message)
	}
	objects := make([][]byte, len(objectIDs))
	for index, objectID := range objectIDs {
		objects[index] = objectsByID[objectID]
	}
	return objects, nil
}

func repositoryRoot(ctx context.Context, cwd string) (string, error) {
	out, err := gitOutputAllowFailure(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotGitRepository
	}
	return strings.TrimSpace(string(out)), nil
}

func gitHead(ctx context.Context, root string) string {
	out, err := gitOutputAllowFailure(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBranch(ctx context.Context, root string) string {
	out, err := gitOutputAllowFailure(ctx, root, "branch", "--show-current")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitStatusDirty(ctx context.Context, root string) bool {
	out, err := gitOutputAllowFailure(ctx, root, "status", "--porcelain")
	if err != nil {
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

func splitNUL(out []byte) []string {
	raw := bytes.Split(out, []byte{0})
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if len(item) == 0 {
			continue
		}
		values = append(values, string(item))
	}
	return values
}
