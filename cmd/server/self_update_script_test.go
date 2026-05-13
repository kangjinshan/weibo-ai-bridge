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

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
