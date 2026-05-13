package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceScriptMacOSRestartInstallsBeforeStart(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS launchd script behavior")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "service.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	writeExecutable(t, filepath.Join(fakeBin, "launchctl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$LAUNCHCTL_CALLS"
if [[ "${1:-}" == "print" ]]; then
  exit 1
fi
exit 0
`)

	targetBin := filepath.Join(tmp, "weibo-ai-bridge")
	if err := os.WriteFile(targetBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	launchctlCalls := filepath.Join(tmp, "launchctl-calls.txt")
	cmd := exec.Command("bash", scriptPath, "restart")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LAUNCHCTL_CALLS="+launchctlCalls,
		"WEIBO_AI_BRIDGE_BIN="+targetBin,
		"WEIBO_AI_BRIDGE_CONFIG_PATH="+filepath.Join(repoRoot, "config", "config.toml"),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service restart failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "launchd plist 已安装") {
		t.Fatalf("restart should install plist before start, got:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Library", "LaunchAgents", "com.weibo-ai-bridge.plist")); err != nil {
		t.Fatalf("plist was not installed: %v\n%s", err, output)
	}
}

func TestServiceScriptMacOSStartDoesNotKickstartAfterBootstrap(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS launchd script behavior")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "service.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	writeExecutable(t, filepath.Join(fakeBin, "launchctl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$LAUNCHCTL_CALLS"
if [[ "${1:-}" == "print" ]]; then
  exit 1
fi
exit 0
`)

	targetBin := filepath.Join(tmp, "weibo-ai-bridge")
	if err := os.WriteFile(targetBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	launchctlCalls := filepath.Join(tmp, "launchctl-calls.txt")
	cmd := exec.Command("bash", "-c", scriptPath+" install && "+scriptPath+" start")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LAUNCHCTL_CALLS="+launchctlCalls,
		"WEIBO_AI_BRIDGE_BIN="+targetBin,
		"WEIBO_AI_BRIDGE_CONFIG_PATH="+filepath.Join(repoRoot, "config", "config.toml"),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service install/start failed: %v\n%s", err, output)
	}

	calls, err := os.ReadFile(launchctlCalls)
	if err != nil {
		t.Fatalf("read launchctl calls: %v", err)
	}
	if !strings.Contains(string(calls), "bootstrap ") {
		t.Fatalf("start should bootstrap unloaded service, calls:\n%s", calls)
	}
	if strings.Contains(string(calls), "kickstart ") {
		t.Fatalf("start should not immediately kickstart a freshly bootstrapped service, calls:\n%s", calls)
	}
}

func TestServiceScriptMacOSStartKickstartsLoadedService(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS launchd script behavior")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "service.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	writeExecutable(t, filepath.Join(fakeBin, "launchctl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$LAUNCHCTL_CALLS"
exit 0
`)

	plistDir := filepath.Join(tmp, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		t.Fatalf("mkdir plist dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plistDir, "com.weibo-ai-bridge.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	launchctlCalls := filepath.Join(tmp, "launchctl-calls.txt")
	cmd := exec.Command("bash", scriptPath, "start")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LAUNCHCTL_CALLS="+launchctlCalls,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service start failed: %v\n%s", err, output)
	}

	calls, err := os.ReadFile(launchctlCalls)
	if err != nil {
		t.Fatalf("read launchctl calls: %v", err)
	}
	if !strings.Contains(string(calls), "kickstart -k ") {
		t.Fatalf("start should kickstart an already loaded service, calls:\n%s", calls)
	}
	if strings.Contains(string(calls), "bootstrap ") {
		t.Fatalf("start should not bootstrap an already loaded service, calls:\n%s", calls)
	}
}
