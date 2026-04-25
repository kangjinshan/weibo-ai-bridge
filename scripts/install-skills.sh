#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

SKILL_NAME="weibo-skill-api"
SKILL_SOURCE_DIR="${REPO_ROOT}/skills/${SKILL_NAME}"
TARGET_HOME="${HOME}"
INSTALL_CODEX="true"
INSTALL_CLAUDE="true"

usage() {
  cat <<'EOF'
Usage:
  install-skills.sh [--repo-root PATH] [--user-home PATH] [--no-codex] [--no-claude]

Installs the bundled weibo skill package into:
  - Codex personal skills:   ~/.codex/skills/
  - Claude personal skills:  ~/.claude/skills/
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      shift
      REPO_ROOT="${1:-}"
      ;;
    --user-home)
      shift
      TARGET_HOME="${1:-}"
      ;;
    --no-codex)
      INSTALL_CODEX="false"
      ;;
    --no-claude)
      INSTALL_CLAUDE="false"
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

if [[ -z "${REPO_ROOT}" || -z "${TARGET_HOME}" ]]; then
  echo "repo root and user home must be set" >&2
  exit 1
fi

SKILL_SOURCE_DIR="${REPO_ROOT}/skills/${SKILL_NAME}"
if [[ ! -d "${SKILL_SOURCE_DIR}" ]]; then
  echo "Skill source not found: ${SKILL_SOURCE_DIR}" >&2
  exit 1
fi

copy_skill() {
  local base_dir="$1"
  local dest_dir="${base_dir}/skills/${SKILL_NAME}"

  mkdir -p "${base_dir}/skills"
  rm -rf "${dest_dir}"
  cp -r "${SKILL_SOURCE_DIR}" "${dest_dir}"
  find "${dest_dir}/scripts" -type f -name '*.sh' -exec chmod +x {} \;
  echo "Installed ${SKILL_NAME} -> ${dest_dir}"
}

if [[ "${INSTALL_CODEX}" == "true" ]]; then
  copy_skill "${TARGET_HOME}/.codex"
fi

if [[ "${INSTALL_CLAUDE}" == "true" ]]; then
  copy_skill "${TARGET_HOME}/.claude"
fi
