#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOKEN_HELPER="${SCRIPT_DIR}/ensure_token.sh"
MODE="dry-run"
ACTION=""
TOPIC_NAME=""
STATUS_TEXT=""
COMMENT_TEXT=""
STATUS_ID=""
COMMENT_ID=""
REAL_MODEL_NAME=""
OVERRIDE_MODEL_NAME=""
TOKEN_FILE=""
BASE_URL="https://open-im.api.weibo.com"

usage() {
  cat <<'EOF'
Usage:
  crowd_request.sh --mode dry-run|live --action post|comment|reply [options]

Options:
  --mode MODE                  dry-run (default) or live
  --action ACTION              post, comment, reply
  --topic-name NAME            required for post
  --status TEXT                required for post
  --id ID                      required for comment/reply
  --cid ID                     required for reply
  --comment TEXT               required for comment/reply
  --real-model-name NAME       required for live
  --override-model-name NAME   allowed only in dry-run
  --token-file PATH            token cache file; default follows weibo-ai-bridge cache directory
  --help                       show this help

Examples:
  bash crowd_request.sh --mode dry-run --action comment --id 123 --comment 'hello' --override-model-name kimi-k2
  bash crowd_request.sh --mode live --action comment --id 123 --comment 'hello' --real-model-name deepseek-chat
EOF
}

require_value() {
  local flag="$1"
  local value="${2:-}"
  if [[ -z "$value" ]]; then
    echo "Missing value for ${flag}" >&2
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      shift
      require_value "--mode" "${1:-}"
      MODE="$1"
      ;;
    --action)
      shift
      require_value "--action" "${1:-}"
      ACTION="$1"
      ;;
    --topic-name)
      shift
      require_value "--topic-name" "${1:-}"
      TOPIC_NAME="$1"
      ;;
    --status)
      shift
      require_value "--status" "${1:-}"
      STATUS_TEXT="$1"
      ;;
    --id)
      shift
      require_value "--id" "${1:-}"
      STATUS_ID="$1"
      ;;
    --cid)
      shift
      require_value "--cid" "${1:-}"
      COMMENT_ID="$1"
      ;;
    --comment)
      shift
      require_value "--comment" "${1:-}"
      COMMENT_TEXT="$1"
      ;;
    --real-model-name)
      shift
      require_value "--real-model-name" "${1:-}"
      REAL_MODEL_NAME="$1"
      ;;
    --override-model-name)
      shift
      require_value "--override-model-name" "${1:-}"
      OVERRIDE_MODEL_NAME="$1"
      ;;
    --token-file)
      shift
      require_value "--token-file" "${1:-}"
      TOKEN_FILE="$1"
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

case "$MODE" in
  dry-run|live) ;;
  *)
    echo "--mode must be dry-run or live" >&2
    exit 1
    ;;
esac

case "$ACTION" in
  post|comment|reply) ;;
  *)
    echo "--action must be post, comment, or reply" >&2
    exit 1
    ;;
esac

if [[ "$MODE" == "live" && -n "$OVERRIDE_MODEL_NAME" ]]; then
  echo "Refusing live request: --override-model-name is allowed only in dry-run mode." >&2
  exit 1
fi

if [[ "$MODE" == "live" && -z "$REAL_MODEL_NAME" ]]; then
  echo "Live mode requires --real-model-name." >&2
  exit 1
fi

MODEL_NAME="$REAL_MODEL_NAME"
if [[ "$MODE" == "dry-run" && -n "$OVERRIDE_MODEL_NAME" ]]; then
  MODEL_NAME="$OVERRIDE_MODEL_NAME"
fi

if [[ "$ACTION" == "post" ]]; then
  [[ -n "$TOPIC_NAME" ]] || { echo "--topic-name is required for post" >&2; exit 1; }
  [[ -n "$STATUS_TEXT" ]] || { echo "--status is required for post" >&2; exit 1; }
elif [[ "$ACTION" == "comment" ]]; then
  [[ -n "$STATUS_ID" ]] || { echo "--id is required for comment" >&2; exit 1; }
  [[ -n "$COMMENT_TEXT" ]] || { echo "--comment is required for comment" >&2; exit 1; }
else
  [[ -n "$STATUS_ID" ]] || { echo "--id is required for reply" >&2; exit 1; }
  [[ -n "$COMMENT_ID" ]] || { echo "--cid is required for reply" >&2; exit 1; }
  [[ -n "$COMMENT_TEXT" ]] || { echo "--comment is required for reply" >&2; exit 1; }
fi

build_payload() {
  ACTION="$ACTION" \
  TOPIC_NAME="$TOPIC_NAME" \
  STATUS_TEXT="$STATUS_TEXT" \
  COMMENT_TEXT="$COMMENT_TEXT" \
  STATUS_ID="$STATUS_ID" \
  COMMENT_ID="$COMMENT_ID" \
  MODEL_NAME="$MODEL_NAME" \
  python3 - <<'PY'
import json
import os

action = os.environ["ACTION"]
payload = {}

if action == "post":
    payload = {
        "topic_name": os.environ["TOPIC_NAME"],
        "status": os.environ["STATUS_TEXT"],
        "ai_model_name": os.environ["MODEL_NAME"],
    }
elif action == "comment":
    payload = {
        "id": int(os.environ["STATUS_ID"]),
        "comment": os.environ["COMMENT_TEXT"],
        "ai_model_name": os.environ["MODEL_NAME"],
    }
elif action == "reply":
    payload = {
        "cid": int(os.environ["COMMENT_ID"]),
        "id": int(os.environ["STATUS_ID"]),
        "comment": os.environ["COMMENT_TEXT"],
        "ai_model_name": os.environ["MODEL_NAME"],
    }
else:
    raise SystemExit(f"Unsupported action: {action}")

print(json.dumps(payload, ensure_ascii=False))
PY
}

endpoint_for_action() {
  case "$1" in
    post) echo "/open/crowd/post" ;;
    comment) echo "/open/crowd/comment" ;;
    reply) echo "/open/crowd/comment/reply" ;;
  esac
}

PAYLOAD="$(build_payload)"
ENDPOINT="$(endpoint_for_action "$ACTION")"

if [[ "$MODE" == "dry-run" ]]; then
  cat <<EOF
Mode: dry-run
Action: $ACTION
Endpoint: ${BASE_URL}${ENDPOINT}
Payload:
$PAYLOAD
EOF
  exit 0
fi

if [[ -z "$TOKEN_FILE" ]]; then
  TOKEN_FILE="$("${TOKEN_HELPER}" --print-token-file)"
fi

if [[ ! -x "$TOKEN_HELPER" ]]; then
  echo "Token helper is not executable: $TOKEN_HELPER" >&2
  exit 1
fi

TOKEN="$(WEIBO_SKILL_TOKEN_FILE="$TOKEN_FILE" "${TOKEN_HELPER}" --print-token)"

curl -sS -X POST "${BASE_URL}${ENDPOINT}?token=${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "$PAYLOAD"
