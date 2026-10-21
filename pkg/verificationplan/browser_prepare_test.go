package verificationplan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserPreparationDoesNotInstallPrivilegedOperatingSystemPackages(t *testing.T) {
	for _, browser := range []string{"chromium", "firefox", "webkit"} {
		t.Run(browser, func(t *testing.T) {
			commands, err := browserPrepareCommands("browser-test", browser)
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			// A normal runner can install browser binaries, but cannot run apt
			// through Playwright's --with-deps helper.
			fake := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = --with-deps ]; then exit 77; fi\ndone\nprintf '%s\\n' \"$*\"\n"
			if err := os.WriteFile(filepath.Join(bin, "pnpm"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("sh", "-ec", strings.Join(commands, "\n"))
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("unprivileged browser preparation failed: %v output=%s", err, output)
			}
			if !strings.Contains(string(output), "playwright install "+browser) {
				t.Fatalf("browser binary installation missing: %s", output)
			}
		})
	}
}
