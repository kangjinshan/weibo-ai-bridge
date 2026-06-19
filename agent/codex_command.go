package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type codexCommandSpec struct {
	command    string
	argsPrefix []string
}

type lookPathFunc func(string) (string, error)

func resolveCodexCommandSpec() (codexCommandSpec, error) {
	return resolveCodexCommandSpecFor(runtime.GOOS, exec.LookPath)
}

func resolveCodexCommandSpecFor(goos string, lookPath lookPathFunc) (codexCommandSpec, error) {
	path, err := lookPath("codex")
	if err != nil {
		return codexCommandSpec{}, err
	}

	spec := codexCommandSpec{command: "codex"}
	if goos != "windows" {
		return spec, nil
	}
	if isWindowsBatchCommand(path) {
		return codexCommandSpec{
			command:    "cmd.exe",
			argsPrefix: []string{"/d", "/s", "/c", path},
		}, nil
	}
	if !isPackagedWindowsAppsCodexPath(path) {
		return spec, nil
	}

	for _, candidate := range []string{"codex.cmd", "codex.bat", "codex.exe"} {
		candidatePath, err := lookPath(candidate)
		if err == nil && !isPackagedWindowsAppsCodexPath(candidatePath) {
			if isWindowsBatchCommand(candidatePath) {
				return codexCommandSpec{
					command:    "cmd.exe",
					argsPrefix: []string{"/d", "/s", "/c", candidatePath},
				}, nil
			}
			return codexCommandSpec{command: candidatePath}, nil
		}
	}

	if desktopCLI := findWindowsDesktopCodexCLI(); desktopCLI != "" {
		return codexCommandSpec{command: desktopCLI}, nil
	}

	return codexCommandSpec{
		command:    "cmd.exe",
		argsPrefix: []string{"/d", "/s", "/c", "codex"},
	}, nil
}

func findWindowsDesktopCodexCLI() string {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return ""
	}

	matches, err := filepath.Glob(filepath.Join(localAppData, "OpenAI", "Codex", "bin", "*", "codex.exe"))
	if err != nil || len(matches) == 0 {
		return ""
	}

	best := ""
	var bestModTime int64
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		modTime := info.ModTime().UnixNano()
		if best == "" || modTime > bestModTime {
			best = match
			bestModTime = modTime
		}
	}
	return best
}

func isPackagedWindowsAppsCodexPath(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	return strings.Contains(normalized, `\program files\windowsapps\`) &&
		strings.Contains(normalized, `\openai.codex_`) &&
		strings.HasSuffix(normalized, `\codex.exe`)
}

func isWindowsBatchCommand(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

func newCodexCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	spec, err := resolveCodexCommandSpec()
	if err != nil {
		spec = codexCommandSpec{command: "codex"}
	}

	cmdArgs := append([]string{}, spec.argsPrefix...)
	cmdArgs = append(cmdArgs, args...)
	return exec.CommandContext(ctx, spec.command, cmdArgs...)
}
