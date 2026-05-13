package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfUpdateScriptNoRestartExitsCleanly(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "self-update.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	writeExecutable(t, filepath.Join(fakeBin, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "ls-remote" ]]; then
  echo "def4567890abcdef	refs/heads/main"
  exit 0
fi
if [[ "${1:-}" == "clone" ]]; then
  dest="${@: -1}"
  mkdir -p "${dest}/scripts" "${dest}/skills"
  touch "${dest}/go.mod"
  printf '#!/usr/bin/env bash\n' > "${dest}/scripts/self-update.sh"
  chmod +x "${dest}/scripts/self-update.sh"
  exit 0
fi
if [[ "${1:-}" == "-C" && "${3:-}" == "describe" ]]; then
  echo "test-version"
  exit 0
fi
if [[ "${1:-}" == "-C" && "${3:-}" == "rev-parse" ]]; then
  echo "abc123"
  exit 0
fi
echo "unexpected git args: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "version" && "${2:-}" == "-m" ]]; then
  exit 1
fi
if [[ "${1:-}" == "mod" && "${2:-}" == "download" ]]; then
  exit 0
fi
if [[ "${1:-}" == "build" ]]; then
  out=""
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == "-o" ]]; then
      out="$2"
      break
    fi
    shift
  done
  [[ -n "${out}" ]] || { echo "missing -o" >&2; exit 1; }
  printf '#!/usr/bin/env bash\necho fake bridge\n' > "${out}"
  chmod +x "${out}"
  exit 0
fi
echo "unexpected go args: $*" >&2
exit 1
`)

	targetBin := filepath.Join(tmp, "install", "weibo-ai-bridge")
	cmd := exec.Command("bash", scriptPath, "--no-restart", "--repo", "fake-repo", "--ref", "main", "--target-bin", targetBin)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-update script failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "unbound variable") {
		t.Fatalf("self-update script reported unbound variable:\n%s", output)
	}
	if _, err := os.Stat(targetBin); err != nil {
		t.Fatalf("target binary not installed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(tmp, "install", "scripts", "self-update.sh")); err != nil {
		t.Fatalf("scripts not installed: %v\n%s", err, output)
	}
}

func TestSelfUpdateScriptSkipsWhenTargetMatchesRemote(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "self-update.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	const commit = "abcdef1234567890abcdef1234567890abcdef12"
	writeExecutable(t, filepath.Join(fakeBin, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "ls-remote" ]]; then
  echo "`+commit+`	refs/heads/main"
  exit 0
fi
if [[ "${1:-}" == "clone" ]]; then
  echo "clone should not run when local version is current" >&2
  exit 1
fi
echo "unexpected git args: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "version" && "${2:-}" == "-m" ]]; then
  cat <<EOF
$3: go1.25.0
	build	vcs.revision=`+commit+`
EOF
  exit 0
fi
if [[ "${1:-}" == "build" || "${1:-}" == "mod" ]]; then
  echo "go build/mod should not run when local version is current" >&2
  exit 1
fi
echo "unexpected go args: $*" >&2
exit 1
`)

	targetBin := filepath.Join(tmp, "install", "weibo-ai-bridge")
	if err := os.MkdirAll(filepath.Dir(targetBin), 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(targetBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	cmd := exec.Command("bash", scriptPath, "--no-restart", "--repo", "fake-repo", "--ref", "main", "--target-bin", targetBin)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-update script failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "本地版本已是最新") {
		t.Fatalf("expected up-to-date message, got:\n%s", output)
	}
	if !strings.Contains(string(output), "WEIBO_AI_BRIDGE_ALREADY_UP_TO_DATE=1") {
		t.Fatalf("expected up-to-date marker, got:\n%s", output)
	}
}

func TestSelfUpdateScriptSchedulesMacOSInstallThenStart(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	scriptPath, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "self-update.sh"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	writeExecutable(t, filepath.Join(fakeBin, "git"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "ls-remote" ]]; then
  echo "2222222222222222222222222222222222222222	refs/heads/main"
  exit 0
fi
if [[ "${1:-}" == "clone" ]]; then
  dest="${@: -1}"
  mkdir -p "${dest}/scripts" "${dest}/skills"
  touch "${dest}/go.mod"
  printf '#!/usr/bin/env bash\n' > "${dest}/scripts/service.sh"
  chmod +x "${dest}/scripts/service.sh"
  exit 0
fi
if [[ "${1:-}" == "-C" && "${3:-}" == "describe" ]]; then
  echo "test-version"
  exit 0
fi
if [[ "${1:-}" == "-C" && "${3:-}" == "rev-parse" ]]; then
  echo "2222222"
  exit 0
fi
echo "unexpected git args: $*" >&2
exit 1
`)
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "version" && "${2:-}" == "-m" ]]; then
  cat <<EOF
$3: go1.25.0
	build	vcs.revision=1111111111111111111111111111111111111111
EOF
  exit 0
fi
if [[ "${1:-}" == "mod" && "${2:-}" == "download" ]]; then
  exit 0
fi
if [[ "${1:-}" == "build" ]]; then
  out=""
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == "-o" ]]; then
      out="$2"
      break
    fi
    shift
  done
  [[ -n "${out}" ]] || { echo "missing -o" >&2; exit 1; }
  printf '#!/usr/bin/env bash\necho fake bridge\n' > "${out}"
  chmod +x "${out}"
  exit 0
fi
echo "unexpected go args: $*" >&2
exit 1
`)
	targetBin := filepath.Join(tmp, "install", "weibo-ai-bridge")
	if err := os.MkdirAll(filepath.Dir(targetBin), 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(targetBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	cmd := exec.Command("bash", scriptPath, "--repo", "fake-repo", "--ref", "main", "--target-bin", targetBin)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WEIBO_AI_BRIDGE_TEST_OS=Darwin",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("self-update script failed: %v\n%s", err, output)
	}

	out := string(output)
	if strings.Contains(out, "service.sh restart") {
		t.Fatalf("macOS restart schedule should not use service restart:\n%s", output)
	}
	if !strings.Contains(out, "service.sh install") || !strings.Contains(out, "service.sh start") {
		t.Fatalf("macOS restart schedule should install then start, got:\n%s", output)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
