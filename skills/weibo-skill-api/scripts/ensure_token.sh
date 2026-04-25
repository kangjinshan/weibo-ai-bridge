#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${SKILL_DIR}/../.." 2>/dev/null && pwd || true)"

DEFAULT_TOKEN_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/weibo-ai-bridge/weibo-skill"
TOKEN_FILE="${WEIBO_SKILL_TOKEN_FILE:-${DEFAULT_TOKEN_DIR}/token-cache.json}"
PRINT_MODE="token"

usage() {
  cat <<'EOF'
Usage:
  ensure_token.sh [--token-file PATH] [--print-token | --print-token-file]

Behavior:
  - Reuse weibo-ai-bridge's existing Weibo app_id/app_secret configuration
  - Refresh the token when cache is missing or expired
  - Print either the token value or the token cache file path
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --token-file)
      shift
      TOKEN_FILE="${1:-}"
      if [[ -z "${TOKEN_FILE}" ]]; then
        echo "Missing value for --token-file" >&2
        exit 1
      fi
      ;;
    --print-token)
      PRINT_MODE="token"
      ;;
    --print-token-file)
      PRINT_MODE="token_file"
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

if [[ "${PRINT_MODE}" == "token_file" ]]; then
  mkdir -p "$(dirname "${TOKEN_FILE}")"
  printf '%s\n' "${TOKEN_FILE}"
  exit 0
fi

find_config_path() {
  local candidates=()

  if [[ -n "${CONFIG_PATH:-}" ]]; then
    candidates+=("${CONFIG_PATH}")
  fi

  candidates+=(
    "/etc/weibo-ai-bridge/config.toml"
  )

  if [[ -n "${REPO_ROOT}" ]]; then
    candidates+=("${REPO_ROOT}/config/config.toml")
  fi

  local path
  for path in "${candidates[@]}"; do
    if [[ -n "${path}" && -f "${path}" ]]; then
      printf '%s\n' "${path}"
      return 0
    fi
  done

  return 1
}

read_config_field() {
  local config_path="$1"
  local field="$2"
  python3 - "$config_path" "$field" <<'PY'
import re
import sys

config_path = sys.argv[1]
field = sys.argv[2]

with open(config_path, "r", encoding="utf-8") as fh:
    lines = fh.readlines()

in_section = False
for line in lines:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        continue
    if stripped.startswith("[") and stripped.endswith("]"):
        in_section = stripped == "[platform.weibo]"
        continue
    if not in_section:
        continue
    match = re.match(r'^([A-Za-z0-9_]+)\s*=\s*"(.*)"\s*$', stripped)
    if match and match.group(1) == field:
        print(match.group(2))
        raise SystemExit(0)

raise SystemExit(1)
PY
}

resolve_weibo_config() {
  local config_path="$1"
  local app_id=""
  local app_secret=""
  local token_url=""

  app_id="$(read_config_field "${config_path}" "app_id" 2>/dev/null || true)"
  app_secret="$(read_config_field "${config_path}" "app_secret" 2>/dev/null || true)"
  token_url="$(read_config_field "${config_path}" "token_url" 2>/dev/null || true)"

  if [[ -n "${WEIBO_APP_ID:-}" ]]; then
    app_id="${WEIBO_APP_ID}"
  fi
  if [[ -n "${WEIBO_APP_SECRET:-}" ]]; then
    app_secret="${WEIBO_APP_SECRET}"
  elif [[ -n "${WEIBO_APP_Secret:-}" ]]; then
    app_secret="${WEIBO_APP_Secret}"
  fi
  if [[ -n "${WEIBO_TOKEN_URL:-}" ]]; then
    token_url="${WEIBO_TOKEN_URL}"
  fi

  if [[ -z "${token_url}" ]]; then
    token_url="http://open-im.api.weibo.com/open/auth/ws_token"
  fi

  if [[ -z "${app_id}" || -z "${app_secret}" ]]; then
    echo "Unable to resolve weibo-ai-bridge app_id/app_secret. Set CONFIG_PATH or WEIBO_APP_ID / WEIBO_APP_SECRET." >&2
    exit 1
  fi

  printf '%s\n%s\n%s\n' "${app_id}" "${app_secret}" "${token_url}"
}

read_cached_token() {
  local token_file="$1"
  python3 - "$token_file" <<'PY'
import json
import os
import sys
import time

path = sys.argv[1]
if not os.path.exists(path):
    raise SystemExit(1)

with open(path, "r", encoding="utf-8") as fh:
    data = json.load(fh)

token_data = data.get("data", {}) if isinstance(data, dict) else {}
token = token_data.get("token")
expire_in = token_data.get("expire_in")
cached_at = data.get("_cached_at")

if not token or not expire_in:
    raise SystemExit(1)

if cached_at is None:
    cached_at = os.path.getmtime(path)

if time.time() >= float(cached_at) + int(expire_in) - 60:
    raise SystemExit(1)

print(token)
PY
}

refresh_token() {
  local app_id="$1"
  local app_secret="$2"
  local token_url="$3"
  local token_file="$4"
  local token_dir
  local payload
  local response

  token_dir="$(dirname "${token_file}")"
  mkdir -p "${token_dir}"

  payload="$(python3 - "$app_id" "$app_secret" <<'PY'
import json
import sys

print(json.dumps({
    "app_id": sys.argv[1],
    "app_secret": sys.argv[2],
}, ensure_ascii=False))
PY
)"

  response="$(curl -fsS -X POST "${token_url}" -H 'Content-Type: application/json' -d "${payload}")"

  python3 - "$response" "$token_file" <<'PY'
import json
import sys
import time

raw = sys.argv[1]
path = sys.argv[2]

data = json.loads(raw)
if data.get("code") != 0:
    raise SystemExit(f"token error: {data.get('message', 'unknown error')} (code: {data.get('code')})")

data["_cached_at"] = int(time.time())
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh, ensure_ascii=False, indent=2)

token = data.get("data", {}).get("token")
if not token:
    raise SystemExit("token response missing data.token")

print(token)
PY
}

CONFIG_FILE_PATH="$(find_config_path || true)"
if [[ -z "${CONFIG_FILE_PATH}" ]]; then
  echo "Unable to find weibo-ai-bridge config file. Set CONFIG_PATH or install weibo-ai-bridge first." >&2
  exit 1
fi

mapfile -t WEIBO_CFG < <(resolve_weibo_config "${CONFIG_FILE_PATH}")
APP_ID="${WEIBO_CFG[0]}"
APP_SECRET="${WEIBO_CFG[1]}"
TOKEN_URL="${WEIBO_CFG[2]}"

TOKEN="$(read_cached_token "${TOKEN_FILE}" 2>/dev/null || true)"
if [[ -z "${TOKEN}" ]]; then
  TOKEN="$(refresh_token "${APP_ID}" "${APP_SECRET}" "${TOKEN_URL}" "${TOKEN_FILE}")"
fi

case "${PRINT_MODE}" in
  token)
    printf '%s\n' "${TOKEN}"
    ;;
  token_file)
    printf '%s\n' "${TOKEN_FILE}"
    ;;
esac
