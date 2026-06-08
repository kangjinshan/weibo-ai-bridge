package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceScriptTemplatesPersistLinuxScope(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	templateBytes, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "weibo-ai-bridge.service.tmpl"))
	if err != nil {
		t.Fatalf("read systemd template: %v", err)
	}
	if !strings.Contains(string(templateBytes), "Environment=WEIBO_AI_BRIDGE_SCOPE=__SCOPE__") {
		t.Fatalf("systemd template must persist the resolved Linux service scope:\n%s", templateBytes)
	}

	scriptBytes, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "service.sh"))
	if err != nil {
		t.Fatalf("read service script: %v", err)
	}
	if !strings.Contains(string(scriptBytes), "__SCOPE__") {
		t.Fatalf("service script must render the Linux service scope into the unit")
	}
}

func TestWindowsServiceSupportFiles(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	makefileBytes, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(makefileBytes)
	if !strings.Contains(makefile, "build-windows:") || !strings.Contains(makefile, "GOOS=windows GOARCH=amd64") {
		t.Fatalf("Makefile must expose a Windows build target:\n%s", makefile)
	}
	if !strings.Contains(makefile, "BINARY_WINDOWS=$(BINARY_NAME).exe") || !strings.Contains(makefile, "$(BUILD_DIR)/$(BINARY_WINDOWS)") {
		t.Fatalf("Windows build target should produce the .exe binary:\n%s", makefile)
	}

	serviceScriptBytes, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "service.ps1"))
	if err != nil {
		t.Fatalf("read Windows service script: %v", err)
	}
	serviceScript := string(serviceScriptBytes)
	for _, want := range []string{"New-Service", "HKLM:\\SYSTEM\\CurrentControlSet\\Services\\$Name", "CONFIG_PATH=", "LOG_OUTPUT=", "Get-Content -LiteralPath $logPath -Tail 100 -Wait"} {
		if !strings.Contains(serviceScript, want) {
			t.Fatalf("Windows service script missing %q:\n%s", want, serviceScript)
		}
	}

	windowsServiceBytes, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "server", "windows_service.go"))
	if err != nil {
		t.Fatalf("read Windows service host: %v", err)
	}
	windowsService := string(windowsServiceBytes)
	for _, want := range []string{"//go:build windows", "svc.IsWindowsService()", "svc.Run", "svc.AcceptStop | svc.AcceptShutdown"} {
		if !strings.Contains(windowsService, want) {
			t.Fatalf("Windows service host missing %q:\n%s", want, windowsService)
		}
	}
}

func TestServiceScriptLinuxUserInstallPersistsUserScope(t *testing.T) {
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

	writeExecutable(t, filepath.Join(fakeBin, "uname"), `#!/usr/bin/env bash
echo Linux
`)
	writeExecutable(t, filepath.Join(fakeBin, "systemctl"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SYSTEMCTL_CALLS"
`)

	targetBin := filepath.Join(tmp, "weibo-ai-bridge")
	if err := os.WriteFile(targetBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	systemctlCalls := filepath.Join(tmp, "systemctl-calls.txt")
	cmd := exec.Command("bash", scriptPath, "install", "--scope", "user")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SYSTEMCTL_CALLS="+systemctlCalls,
		"WEIBO_AI_BRIDGE_BIN="+targetBin,
		"WEIBO_AI_BRIDGE_CONFIG_PATH="+filepath.Join(repoRoot, "config", "config.toml"),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("service install failed: %v\n%s", err, output)
	}

	unitPath := filepath.Join(tmp, ".config", "systemd", "user", "weibo-ai-bridge.service")
	unitBytes, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read generated unit: %v\n%s", err, output)
	}
	unit := string(unitBytes)
	if !strings.Contains(unit, "Environment=WEIBO_AI_BRIDGE_SCOPE=user") {
		t.Fatalf("generated user unit should persist user scope:\n%s", unit)
	}
	if strings.Contains(unit, "__SCOPE__") {
		t.Fatalf("generated user unit still contains scope placeholder:\n%s", unit)
	}
}

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
