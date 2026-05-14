#!/usr/bin/env bash

set -euo pipefail

PROJECT_NAME="weibo-ai-bridge"
DEFAULT_IDENTIFIER="com.weibo-ai-bridge"

log_info() {
    echo "[INFO] $1"
}

log_warn() {
    echo "[WARN] $1" >&2
}

die() {
    echo "[ERROR] $1" >&2
    exit 1
}

usage() {
    cat <<EOF
用法: $(basename "$0") <binary>

在 macOS 上为 weibo-ai-bridge 二进制添加稳定代码签名，减少重建或重启后反复触发系统权限确认。

环境变量:
  WEIBO_AI_BRIDGE_CODESIGN             auto|1|0，默认 auto
  WEIBO_AI_BRIDGE_CODESIGN_IDENTITY    指定 codesign identity；未指定时自动选择第一个可用 identity
  WEIBO_AI_BRIDGE_CODESIGN_IDENTIFIER  指定 bundle identifier，默认 ${DEFAULT_IDENTIFIER}
EOF
}

first_codesign_identity() {
    security find-identity -v -p codesigning 2>/dev/null |
        awk '
            /"[^"]+"/ && $0 !~ /CSSMERR/ {
                match($0, /"[^"]+"/)
                print substr($0, RSTART + 1, RLENGTH - 2)
                exit
            }
        '
}

main() {
    if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
        usage
        exit 0
    fi

    local binary="${1:-}"
    [[ -n "${binary}" ]] || die "缺少 binary 参数"

    if [[ "$(uname -s)" != "Darwin" ]]; then
        exit 0
    fi

    local mode="${WEIBO_AI_BRIDGE_CODESIGN:-auto}"
    case "${mode}" in
        0|false|False|FALSE|no|No|NO|off|Off|OFF)
            log_info "macOS codesign 已禁用"
            exit 0
            ;;
        auto|1|true|True|TRUE|yes|Yes|YES|on|On|ON)
            ;;
        *)
            die "WEIBO_AI_BRIDGE_CODESIGN 只能是 auto|1|0（当前: ${mode}）"
            ;;
    esac

    [[ -f "${binary}" ]] || die "未找到二进制: ${binary}"
    command -v codesign >/dev/null 2>&1 || die "缺少 codesign"

    local identity="${WEIBO_AI_BRIDGE_CODESIGN_IDENTITY:-}"
    if [[ -z "${identity}" ]]; then
        identity="$(first_codesign_identity || true)"
    fi

    if [[ -z "${identity}" ]]; then
        if [[ "${mode}" == "auto" ]]; then
            log_warn "未找到可用 codesign identity，跳过签名。可设置 WEIBO_AI_BRIDGE_CODESIGN_IDENTITY。"
            exit 0
        fi
        die "未找到可用 codesign identity"
    fi

    local identifier="${WEIBO_AI_BRIDGE_CODESIGN_IDENTIFIER:-${DEFAULT_IDENTIFIER}}"
    log_info "codesign ${PROJECT_NAME}: identity=${identity}, identifier=${identifier}"
    codesign --force --timestamp=none --identifier "${identifier}" --sign "${identity}" "${binary}" >/dev/null
    codesign --verify --strict "${binary}" >/dev/null
}

main "$@"
