#!/usr/bin/env bash

set -euo pipefail

PROJECT_NAME="weibo-ai-bridge"
SYSTEMD_UNIT_NAME="${PROJECT_NAME}.service"
LAUNCHD_LABEL="com.weibo-ai-bridge"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEPLOY_DIR="${REPO_ROOT}/deploy"

OS_NAME="$(uname -s)"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

SCOPE="${WEIBO_AI_BRIDGE_SCOPE:-auto}"
SERVICE_USER="${WEIBO_AI_BRIDGE_SERVICE_USER:-$(id -un)}"

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

die() {
    log_error "$1"
    exit 1
}

resolve_user_home() {
    local user="$1"

    if [[ "${user}" == "$(id -un)" ]]; then
        echo "${HOME}"
        return
    fi

    if command -v getent >/dev/null 2>&1; then
        local home
        home="$(getent passwd "${user}" | cut -d: -f6 || true)"
        if [[ -n "${home}" ]]; then
            echo "${home}"
            return
        fi
    fi

    if command -v dscl >/dev/null 2>&1; then
        local home
        home="$(dscl . -read "/Users/${user}" NFSHomeDirectory 2>/dev/null | awk '{print $2}' || true)"
        if [[ -n "${home}" ]]; then
            echo "${home}"
            return
        fi
    fi

    local expanded
    expanded="$(eval echo "~${user}" 2>/dev/null || true)"
    if [[ -n "${expanded}" && "${expanded}" != "~${user}" ]]; then
        echo "${expanded}"
        return
    fi

    echo ""
}

default_bin_path() {
    if [[ -n "${WEIBO_AI_BRIDGE_BIN:-}" ]]; then
        echo "${WEIBO_AI_BRIDGE_BIN}"
        return
    fi

    local canonical_path="${REPO_ROOT}/build/weibo-ai-bridge"
    if [[ -x "${canonical_path}" || -f "${canonical_path}" ]]; then
        echo "${canonical_path}"
        return
    fi

    # 兼容旧版本根目录产物（短期 fallback，一次过渡后可删除）
    local legacy_candidates=(
        "${REPO_ROOT}/weibo-ai-bridge"
        "${REPO_ROOT}/server"
    )

    local p
    for p in "${legacy_candidates[@]}"; do
        if [[ -x "${p}" || -f "${p}" ]]; then
            log_warn "检测到旧版根目录二进制: ${p}；建议迁移到 ${canonical_path}" >&2
            echo "${p}"
            return
        fi
    done

    echo "${canonical_path}"
}

default_config_path() {
    if [[ -n "${WEIBO_AI_BRIDGE_CONFIG_PATH:-}" ]]; then
        echo "${WEIBO_AI_BRIDGE_CONFIG_PATH}"
        return
    fi

    local candidates=(
        "/etc/${PROJECT_NAME}/config.toml"
        "${REPO_ROOT}/config/config.toml"
    )
    local p
    for p in "${candidates[@]}"; do
        if [[ -f "${p}" ]]; then
            echo "${p}"
            return
        fi
    done

    echo "${REPO_ROOT}/config/config.toml"
}

default_env_file() {
    if [[ -n "${WEIBO_AI_BRIDGE_ENV_FILE:-}" ]]; then
        echo "${WEIBO_AI_BRIDGE_ENV_FILE}"
        return
    fi

    local config_path
    config_path="$(default_config_path)"
    local config_dir
    config_dir="$(cd "$(dirname "${config_path}")" && pwd)"

    local candidates=(
        "${config_dir}/.env"
        "${REPO_ROOT}/.env"
    )
    local p
    for p in "${candidates[@]}"; do
        if [[ -f "${p}" ]]; then
            echo "${p}"
            return
        fi
    done

    echo "${config_dir}/.env"
}

default_path_value() {
    local value="${PATH}"
    local user_home
    user_home="$(resolve_user_home "${SERVICE_USER}")"

    if [[ -n "${user_home}" ]] && [[ "${value}" != *"${user_home}/.local/bin"* ]]; then
        value="${user_home}/.local/bin:${value}"
    fi

    if [[ "${OS_NAME}" == "Darwin" ]]; then
        if [[ "${value}" != *"/opt/homebrew/bin"* ]]; then
            value="/opt/homebrew/bin:${value}"
        fi
        if [[ "${value}" != *"/usr/local/bin"* ]]; then
            value="/usr/local/bin:${value}"
        fi
    fi

    echo "${value}"
}

escape_sed() {
    printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

resolve_linux_scope() {
    local selected="${SCOPE}"
    if [[ "${selected}" == "auto" ]]; then
        if [[ "${EUID}" -eq 0 ]]; then
            selected="system"
        else
            selected="user"
        fi
    fi

    if [[ "${selected}" != "system" && "${selected}" != "user" ]]; then
        die "Linux 仅支持 --scope system|user（当前: ${selected}）"
    fi

    echo "${selected}"
}

linux_unit_path() {
    local scope="$1"
    if [[ "${scope}" == "system" ]]; then
        echo "/etc/systemd/system/${SYSTEMD_UNIT_NAME}"
        return
    fi

    echo "${HOME}/.config/systemd/user/${SYSTEMD_UNIT_NAME}"
}

systemctl_cmd() {
    local scope="$1"
    if [[ "${scope}" == "user" ]]; then
        echo "systemctl --user"
        return
    fi
    echo "systemctl"
}

install_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"

    local unit_path
    unit_path="$(linux_unit_path "${scope}")"
    local unit_dir
    unit_dir="$(dirname "${unit_path}")"

    if [[ "${scope}" == "system" && "${EUID}" -ne 0 ]]; then
        die "system scope 需要 root 权限，请使用 sudo 或改为 --scope user"
    fi

    mkdir -p "${unit_dir}"

    local template="${DEPLOY_DIR}/weibo-ai-bridge.service.tmpl"
    [[ -f "${template}" ]] || die "缺少模板: ${template}"

    local wanted_by="default.target"
    local user_directive="# User is managed by --user session"
    if [[ "${scope}" == "system" ]]; then
        wanted_by="multi-user.target"
        user_directive="User=${SERVICE_USER}"
    fi

    local workdir execstart path_value config_path env_file
    workdir="${REPO_ROOT}"
    execstart="$(default_bin_path)"
    path_value="$(default_path_value)"
    config_path="$(default_config_path)"
    env_file="$(default_env_file)"

    [[ -f "${execstart}" ]] || die "未找到可执行文件: ${execstart}（可通过 WEIBO_AI_BRIDGE_BIN 指定）"
    [[ -f "${config_path}" ]] || log_warn "配置文件不存在: ${config_path}（服务仍会尝试启动）"

    sed \
        -e "s/__USER_DIRECTIVE__/$(escape_sed "${user_directive}")/g" \
        -e "s/__WORKDIR__/$(escape_sed "${workdir}")/g" \
        -e "s/__EXECSTART__/$(escape_sed "${execstart}")/g" \
        -e "s/__PATH__/$(escape_sed "${path_value}")/g" \
        -e "s/__CONFIG_PATH__/$(escape_sed "${config_path}")/g" \
        -e "s/__ENV_FILE__/$(escape_sed "${env_file}")/g" \
        -e "s/__WANTED_BY__/$(escape_sed "${wanted_by}")/g" \
        "${template}" > "${unit_path}"

    local ctl
    ctl="$(systemctl_cmd "${scope}")"
    ${ctl} daemon-reload
    ${ctl} enable "${SYSTEMD_UNIT_NAME}"

    log_success "systemd unit 已安装: ${unit_path}"
}

start_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"
    local ctl
    ctl="$(systemctl_cmd "${scope}")"
    ${ctl} start "${SYSTEMD_UNIT_NAME}"
    log_success "已启动 ${SYSTEMD_UNIT_NAME} (${scope})"
}

stop_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"
    local ctl
    ctl="$(systemctl_cmd "${scope}")"
    ${ctl} stop "${SYSTEMD_UNIT_NAME}" || true
    log_success "已停止 ${SYSTEMD_UNIT_NAME} (${scope})"
}

status_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"
    local ctl
    ctl="$(systemctl_cmd "${scope}")"
    ${ctl} status "${SYSTEMD_UNIT_NAME}" --no-pager
}

logs_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"
    if [[ "${scope}" == "user" ]]; then
        journalctl --user -u "${SYSTEMD_UNIT_NAME}" -f
        return
    fi
    journalctl -u "${SYSTEMD_UNIT_NAME}" -f
}

uninstall_linux_systemd() {
    local scope
    scope="$(resolve_linux_scope)"
    local unit_path
    unit_path="$(linux_unit_path "${scope}")"
    local ctl
    ctl="$(systemctl_cmd "${scope}")"

    ${ctl} disable "${SYSTEMD_UNIT_NAME}" >/dev/null 2>&1 || true
    ${ctl} stop "${SYSTEMD_UNIT_NAME}" >/dev/null 2>&1 || true

    rm -f "${unit_path}"
    ${ctl} daemon-reload
    log_success "已卸载 ${SYSTEMD_UNIT_NAME} (${scope})"
}

launchd_plist_path() {
    echo "${HOME}/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"
}

launchd_stdout_log() {
    echo "${HOME}/Library/Logs/${PROJECT_NAME}/stdout.log"
}

launchd_stderr_log() {
    echo "${HOME}/Library/Logs/${PROJECT_NAME}/stderr.log"
}

install_macos_launchd() {
    if [[ "${EUID}" -eq 0 ]]; then
        die "macOS launchd 建议按业务用户安装。请切换到目标用户后执行。"
    fi

    local plist_path
    plist_path="$(launchd_plist_path)"
    local plist_dir
    plist_dir="$(dirname "${plist_path}")"
    mkdir -p "${plist_dir}"
    mkdir -p "$(dirname "$(launchd_stdout_log)")"

    local template="${DEPLOY_DIR}/com.weibo-ai-bridge.plist.tmpl"
    [[ -f "${template}" ]] || die "缺少模板: ${template}"

    local workdir execstart path_value config_path stdout_log stderr_log env_file
    workdir="${REPO_ROOT}"
    execstart="$(default_bin_path)"
    path_value="$(default_path_value)"
    config_path="$(default_config_path)"
    env_file="$(default_env_file)"
    stdout_log="$(launchd_stdout_log)"
    stderr_log="$(launchd_stderr_log)"

    [[ -f "${execstart}" ]] || die "未找到可执行文件: ${execstart}（可通过 WEIBO_AI_BRIDGE_BIN 指定）"
    [[ -f "${config_path}" ]] || log_warn "配置文件不存在: ${config_path}（服务仍会尝试启动）"
    [[ -f "${env_file}" ]] || log_warn "环境变量文件不存在: ${env_file}（可按需创建）"

    sed \
        -e "s/__LABEL__/$(escape_sed "${LAUNCHD_LABEL}")/g" \
        -e "s/__WORKDIR__/$(escape_sed "${workdir}")/g" \
        -e "s/__EXECSTART__/$(escape_sed "${execstart}")/g" \
        -e "s/__PATH__/$(escape_sed "${path_value}")/g" \
        -e "s/__CONFIG_PATH__/$(escape_sed "${config_path}")/g" \
        -e "s/__STDOUT_LOG__/$(escape_sed "${stdout_log}")/g" \
        -e "s/__STDERR_LOG__/$(escape_sed "${stderr_log}")/g" \
        "${template}" > "${plist_path}"

    log_success "launchd plist 已安装: ${plist_path}"
}

start_macos_launchd() {
    local plist_path
    plist_path="$(launchd_plist_path)"
    [[ -f "${plist_path}" ]] || die "未安装 plist：${plist_path}，请先执行 install"

    local target="gui/$(id -u)"
    launchctl bootout "${target}/${LAUNCHD_LABEL}" >/dev/null 2>&1 || true
    launchctl bootstrap "${target}" "${plist_path}"
    launchctl kickstart -k "${target}/${LAUNCHD_LABEL}"
    log_success "已启动 ${LAUNCHD_LABEL}"
}

stop_macos_launchd() {
    local target="gui/$(id -u)"
    launchctl bootout "${target}/${LAUNCHD_LABEL}" >/dev/null 2>&1 || true
    log_success "已停止 ${LAUNCHD_LABEL}"
}

status_macos_launchd() {
    launchctl print "gui/$(id -u)/${LAUNCHD_LABEL}"
}

logs_macos_launchd() {
    local stdout_log stderr_log
    stdout_log="$(launchd_stdout_log)"
    stderr_log="$(launchd_stderr_log)"
    mkdir -p "$(dirname "${stdout_log}")"
    touch "${stdout_log}" "${stderr_log}"
    tail -f "${stdout_log}" "${stderr_log}"
}

uninstall_macos_launchd() {
    stop_macos_launchd
    rm -f "$(launchd_plist_path)"
    log_success "已卸载 ${LAUNCHD_LABEL}"
}

usage() {
    cat <<EOF
用法: $(basename "$0") <install|start|stop|restart|status|logs|uninstall> [--scope system|user]

参数:
  --scope system|user   仅 Linux 生效。auto(默认): root -> system, 非 root -> user

可选环境变量:
  WEIBO_AI_BRIDGE_BIN          指定二进制路径
  WEIBO_AI_BRIDGE_CONFIG_PATH  指定 config.toml 路径
  WEIBO_AI_BRIDGE_ENV_FILE     指定 .env 路径
  WEIBO_AI_BRIDGE_SERVICE_USER 指定 Linux systemd system scope 的运行用户
  WEIBO_AI_BRIDGE_SCOPE        默认 scope（system|user|auto）
EOF
}

run_command() {
    local cmd="$1"

    case "${OS_NAME}" in
        Linux)
            case "${cmd}" in
                install) install_linux_systemd ;;
                start) start_linux_systemd ;;
                stop) stop_linux_systemd ;;
                restart) stop_linux_systemd; start_linux_systemd ;;
                status) status_linux_systemd ;;
                logs) logs_linux_systemd ;;
                uninstall) uninstall_linux_systemd ;;
                *) usage; exit 1 ;;
            esac
            ;;
        Darwin)
            case "${cmd}" in
                install) install_macos_launchd ;;
                start) start_macos_launchd ;;
                stop) stop_macos_launchd ;;
                restart) stop_macos_launchd; start_macos_launchd ;;
                status) status_macos_launchd ;;
                logs) logs_macos_launchd ;;
                uninstall) uninstall_macos_launchd ;;
                *) usage; exit 1 ;;
            esac
            ;;
        *)
            die "暂不支持的系统: ${OS_NAME}"
            ;;
    esac
}

main() {
    if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
        usage
        exit 0
    fi

    local cmd="${1:-}"
    shift || true

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --scope)
                [[ $# -ge 2 ]] || die "--scope 缺少参数"
                SCOPE="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                die "未知参数: $1"
                ;;
        esac
    done

    [[ -n "${cmd}" ]] || { usage; exit 1; }
    run_command "${cmd}"
}

main "$@"
