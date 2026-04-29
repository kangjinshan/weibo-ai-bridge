#!/bin/bash

#############################################
# weibo-ai-bridge 配置向导脚本
# 用于引导用户完成 config.toml 配置
#############################################

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

PROJECT_NAME="weibo-ai-bridge"
CONFIG_DIR="${WEIBO_AI_BRIDGE_CONFIG_DIR:-/etc/${PROJECT_NAME}}"
CONFIG_FILE="${CONFIG_DIR}/config.toml"
ENV_FILE="${CONFIG_DIR}/.env"
TEMP_CONFIG="$(mktemp "/tmp/${PROJECT_NAME}-config.XXXXXX")"

# 日志函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "${CYAN}[STEP]${NC} $1"
}

cleanup() {
    rm -f "${TEMP_CONFIG}" "${TEMP_CONFIG}.tmp"
}

trap cleanup EXIT

read_with_default() {
    local prompt="$1"
    local default_value="$2"
    local input

    if [[ -n "${default_value}" ]]; then
        read -r -p "${prompt} [${default_value}]: " input
        echo "${input:-${default_value}}"
        return
    fi

    read -r -p "${prompt}: " input
    echo "${input}"
}

prompt_bool() {
    local prompt="$1"
    local default_value="$2"
    local default_hint="y/N"
    local input

    if [[ "${default_value}" == "true" ]]; then
        default_hint="Y/n"
    fi

    read -r -p "${prompt} (${default_hint}): " input

    if [[ -z "${input}" ]]; then
        echo "${default_value}"
        return
    fi

    local normalized_input
    normalized_input="$(printf '%s' "${input}" | tr '[:upper:]' '[:lower:]')"

    case "${normalized_input}" in
        y|yes|true|1|是|好|好的|确认) echo "true" ;;
        n|no|false|0|否|不|取消) echo "false" ;;
        *)
            log_warning "输入不合法，沿用默认值: ${default_value}"
            echo "${default_value}"
            ;;
    esac
}

get_toml_value() {
    local section="$1"
    local key="$2"

    awk -v section="${section}" -v key="${key}" '
        BEGIN { in_section = 0 }
        $0 ~ "^\\[" section "\\]$" { in_section = 1; next }
        in_section && $0 ~ /^\[/ { in_section = 0 }
        in_section && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
            value = $0
            sub(/^[^=]*=[[:space:]]*/, "", value)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
            if (value ~ /^".*"$/) {
                sub(/^"/, "", value)
                sub(/"$/, "", value)
            }
            print value
            exit
        }
    ' "${TEMP_CONFIG}"
}

set_toml_value() {
    local section="$1"
    local key="$2"
    local value_type="$3" # string|raw
    local value="$4"

    awk -v section="${section}" -v key="${key}" -v value_type="${value_type}" -v value="${value}" '
        function render(v, rendered) {
            rendered = v
            if (value_type == "string") {
                gsub(/\\/, "\\\\", rendered)
                gsub(/"/, "\\\"", rendered)
                return key " = \"" rendered "\""
            }
            return key " = " rendered
        }

        BEGIN {
            in_section = 0
            section_seen = 0
            key_written = 0
        }

        $0 ~ "^\\[" section "\\]$" {
            in_section = 1
            section_seen = 1
            print
            next
        }

        in_section && $0 ~ /^\[/ {
            if (!key_written) {
                print render(value)
                key_written = 1
            }
            in_section = 0
        }

        in_section && $0 ~ "^[[:space:]]*" key "[[:space:]]*=" {
            print render(value)
            key_written = 1
            next
        }

        { print }

        END {
            if (!section_seen) {
                print ""
                print "[" section "]"
                print render(value)
            } else if (in_section && !key_written) {
                print render(value)
            }
        }
    ' "${TEMP_CONFIG}" > "${TEMP_CONFIG}.tmp"

    mv "${TEMP_CONFIG}.tmp" "${TEMP_CONFIG}"
}

set_env_value() {
    local key="$1"
    local value="$2"

    mkdir -p "${CONFIG_DIR}"
    touch "${ENV_FILE}"

    awk -v key="${key}" -v value="${value}" '
        BEGIN { written = 0 }
        $0 ~ "^[[:space:]]*" key "=" {
            print key "=" value
            written = 1
            next
        }
        { print }
        END {
            if (!written) {
                print key "=" value
            }
        }
    ' "${ENV_FILE}" > "${ENV_FILE}.tmp"

    mv "${ENV_FILE}.tmp" "${ENV_FILE}"
    chmod 600 "${ENV_FILE}"
}

mask_secret() {
    local value="$1"
    if [[ -z "${value}" ]]; then
        echo ""
        return
    fi
    if [[ ${#value} -le 6 ]]; then
        echo "******"
        return
    fi
    echo "${value:0:3}***${value: -3}"
}

# 显示欢迎信息
show_welcome() {
    echo ""
    echo "=========================================="
    echo "  weibo-ai-bridge 配置向导"
    echo "=========================================="
    echo ""
    log_info "此向导将帮助您配置 weibo-ai-bridge"
    log_info "主配置文件: ${CONFIG_FILE}"
    log_info "可选环境变量覆盖文件: ${ENV_FILE}"
    echo ""
    read -r -p "按 Enter 键继续..."
    echo ""
}

# 检查配置文件
check_config_file() {
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        log_error "配置文件不存在: ${CONFIG_FILE}"
        if [[ -f "${ENV_FILE}" ]]; then
            log_warning "检测到旧版环境变量文件: ${ENV_FILE}"
            log_info "当前版本默认使用 TOML 配置文件（config.toml）"
        fi
        log_info "请先运行安装脚本: /opt/weibo-ai-bridge/scripts/install.sh"
        exit 1
    fi

    cp "${CONFIG_FILE}" "${TEMP_CONFIG}"
}

configure_weibo() {
    log_step "配置微博平台 (platform.weibo)"
    echo ""

    local current_app_id current_app_secret current_token_url current_ws_url current_timeout
    current_app_id="$(get_toml_value "platform.weibo" "app_id")"
    current_app_secret="$(get_toml_value "platform.weibo" "app_secret")"
    current_token_url="$(get_toml_value "platform.weibo" "token_url")"
    current_ws_url="$(get_toml_value "platform.weibo" "ws_url")"
    current_timeout="$(get_toml_value "platform.weibo" "timeout")"

    current_token_url="${current_token_url:-http://open-im.api.weibo.com/open/auth/ws_token}"
    current_ws_url="${current_ws_url:-ws://open-im.api.weibo.com/ws/stream}"
    current_timeout="${current_timeout:-30}"

    local new_app_id new_app_secret new_token_url new_ws_url new_timeout
    new_app_id="$(read_with_default "输入微博 App ID" "${current_app_id}")"
    new_app_secret="$(read_with_default "输入微博 App Secret" "${current_app_secret}")"
    new_token_url="$(read_with_default "输入 Token URL" "${current_token_url}")"
    new_ws_url="$(read_with_default "输入 WebSocket URL" "${current_ws_url}")"
    new_timeout="$(read_with_default "输入请求超时(秒)" "${current_timeout}")"

    set_toml_value "platform.weibo" "app_id" "string" "${new_app_id}"
    set_toml_value "platform.weibo" "app_secret" "string" "${new_app_secret}"
    set_toml_value "platform.weibo" "token_url" "string" "${new_token_url}"
    set_toml_value "platform.weibo" "ws_url" "string" "${new_ws_url}"
    set_toml_value "platform.weibo" "timeout" "raw" "${new_timeout}"

    log_success "微博平台配置已更新"
    echo ""
}

configure_agents() {
    log_step "配置 Agent"
    echo ""

    local current_claude_enabled current_codex_enabled current_codex_model current_codex_api_key
    current_claude_enabled="$(get_toml_value "agent.claude" "enabled")"
    current_codex_enabled="$(get_toml_value "agent.codex" "enabled")"
    current_codex_model="$(get_toml_value "agent.codex" "model")"
    current_codex_api_key="$(get_toml_value "agent.codex" "api_key")"

    current_claude_enabled="${current_claude_enabled:-true}"
    current_codex_enabled="${current_codex_enabled:-false}"

    local new_claude_enabled new_codex_enabled new_codex_model new_codex_api_key
    new_claude_enabled="$(prompt_bool "是否启用 Claude Agent" "${current_claude_enabled}")"
    new_codex_enabled="$(prompt_bool "是否启用 Codex Agent" "${current_codex_enabled}")"
    new_codex_model="$(read_with_default "输入 Codex 模型（留空沿用本机 codex CLI 默认）" "${current_codex_model}")"
    new_codex_api_key="$(read_with_default "输入 Codex API Key（可留空）" "${current_codex_api_key}")"

    set_toml_value "agent.claude" "enabled" "raw" "${new_claude_enabled}"
    set_toml_value "agent.codex" "enabled" "raw" "${new_codex_enabled}"
    set_toml_value "agent.codex" "model" "string" "${new_codex_model}"
    set_toml_value "agent.codex" "api_key" "string" "${new_codex_api_key}"

    log_success "Agent 配置已更新"
    echo ""
}

configure_log() {
    log_step "配置日志 (log)"
    echo ""

    local current_level current_format current_output
    current_level="$(get_toml_value "log" "level")"
    current_format="$(get_toml_value "log" "format")"
    current_output="$(get_toml_value "log" "output")"

    current_level="${current_level:-info}"
    current_format="${current_format:-json}"
    current_output="${current_output:-stdout}"

    local modify_log
    modify_log="$(prompt_bool "是否修改日志配置" "false")"
    if [[ "${modify_log}" != "true" ]]; then
        log_info "跳过日志配置"
        echo ""
        return
    fi

    local new_level new_format new_output
    new_level="$(read_with_default "日志级别(debug/info/warn/error)" "${current_level}")"
    new_format="$(read_with_default "日志格式(json/text)" "${current_format}")"
    new_output="$(read_with_default "日志输出(stdout/stderr/文件路径)" "${current_output}")"

    set_toml_value "log" "level" "string" "${new_level}"
    set_toml_value "log" "format" "string" "${new_format}"
    set_toml_value "log" "output" "string" "${new_output}"

    log_success "日志配置已更新"
    echo ""
}

configure_server_port_override() {
    log_step "配置可选端口覆盖 (.env)"
    echo ""

    local current_port="5533"
    if [[ -f "${ENV_FILE}" ]]; then
        current_port="$(awk -F'=' '/^[[:space:]]*SERVER_PORT=/{print $2; exit}' "${ENV_FILE}")"
        current_port="${current_port:-5533}"
    fi

    local modify_port
    modify_port="$(prompt_bool "是否写入 SERVER_PORT 到 ${ENV_FILE}" "false")"
    if [[ "${modify_port}" != "true" ]]; then
        log_info "跳过端口覆盖配置"
        echo ""
        return
    fi

    local new_port
    new_port="$(read_with_default "输入 HTTP 端口" "${current_port}")"
    set_env_value "SERVER_PORT" "${new_port}"
    log_success "已更新 ${ENV_FILE} 中的 SERVER_PORT"
    echo ""
}

# 保存配置
save_config() {
    log_step "保存配置"
    echo ""

    local summary_app_id summary_app_secret summary_claude summary_codex
    summary_app_id="$(get_toml_value "platform.weibo" "app_id")"
    summary_app_secret="$(get_toml_value "platform.weibo" "app_secret")"
    summary_claude="$(get_toml_value "agent.claude" "enabled")"
    summary_codex="$(get_toml_value "agent.codex" "enabled")"

    log_info "配置摘要:"
    echo "  app_id: ${summary_app_id}"
    echo "  app_secret: $(mask_secret "${summary_app_secret}")"
    echo "  claude.enabled: ${summary_claude}"
    echo "  codex.enabled: ${summary_codex}"
    echo ""

    local confirm
    confirm="$(prompt_bool "确认保存配置" "true")"

    if [[ "${confirm}" != "true" ]]; then
        log_warning "配置未保存"
        exit 0
    fi

    cp "${CONFIG_FILE}" "${CONFIG_FILE}.backup.$(date +%Y%m%d_%H%M%S)"
    cp "${TEMP_CONFIG}" "${CONFIG_FILE}"
    chmod 600 "${CONFIG_FILE}"

    log_success "配置已保存: ${CONFIG_FILE}"
    log_info "备份文件: ${CONFIG_FILE}.backup.*"
}

# 显示完成信息
show_completion() {
    echo ""
    log_success "=========================================="
    log_success "配置完成！"
    log_success "=========================================="
    echo ""
    log_info "主配置文件: ${CONFIG_FILE}"
    if [[ -f "${ENV_FILE}" ]]; then
        log_info "环境变量覆盖文件: ${ENV_FILE}"
    fi
    echo ""
    log_warning "下一步操作:"
    echo "  1. 重启服务: systemctl restart weibo-ai-bridge"
    echo "  2. 查看状态: systemctl status weibo-ai-bridge"
    echo "  3. 查看日志: journalctl -u weibo-ai-bridge -f"
    echo ""
}

# 主函数
main() {
    show_welcome
    check_config_file
    configure_weibo
    configure_agents
    configure_log
    configure_server_port_override
    save_config
    show_completion

    echo ""
    log_success "配置流程完成！"
    echo ""
}

main "$@"
