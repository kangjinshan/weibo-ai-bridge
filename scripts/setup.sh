#!/bin/bash

#############################################
# weibo-ai-bridge 配置向导脚本
# 用于引导用户完成配置
#############################################

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# 配置文件路径
CONFIG_DIR="/etc/weibo-ai-bridge"
CONFIG_FILE="${CONFIG_DIR}/.env"
TEMP_CONFIG="/tmp/.env.tmp"

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

# 显示欢迎信息
show_welcome() {
    echo ""
    echo "=========================================="
    echo "  weibo-ai-bridge 配置向导"
    echo "=========================================="
    echo ""
    log_info "此向导将帮助您配置 weibo-ai-bridge"
    log_info "配置将保存到: ${CONFIG_FILE}"
    echo ""
    read -p "按 Enter 键继续..."
    echo ""
}

# 检查配置文件
check_config_file() {
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        log_error "配置文件不存在: ${CONFIG_FILE}"
        log_info "请先运行安装脚本: /opt/weibo-ai-bridge/scripts/install.sh"
        exit 1
    fi

    # 复制到临时文件
    cp "${CONFIG_FILE}" "${TEMP_CONFIG}"
}

# 配置服务器
configure_server() {
    log_step "配置服务器"
    echo ""

    # 读取当前配置
    CURRENT_PORT=$(grep "^SERVER_PORT=" "${CONFIG_FILE}" | cut -d'=' -f2)
    CURRENT_HOST=$(grep "^SERVER_HOST=" "${CONFIG_FILE}" | cut -d'=' -f2)
    CURRENT_MODE=$(grep "^SERVER_MODE=" "${CONFIG_FILE}" | cut -d'=' -f2)

    log_info "当前配置:"
    echo "  服务器端口: ${CURRENT_PORT}"
    echo "  服务器主机: ${CURRENT_HOST}"
    echo "  运行模式: ${CURRENT_MODE}"
    echo ""

    read -p "是否修改服务器配置? (y/N): " modify_server
    if [[ "$modify_server" =~ ^[Yy]$ ]]; then
        read -p "输入服务器端口 [${CURRENT_PORT}]: " new_port
        new_port=${new_port:-$CURRENT_PORT}

        read -p "输入服务器主机 [${CURRENT_HOST}]: " new_host
        new_host=${new_host:-$CURRENT_HOST}

        echo "选择运行模式:"
        echo "  1) debug (调试模式)"
        echo "  2) release (生产模式)"
        read -p "输入选项 [1-2, 默认: 1]: " mode_option
        mode_option=${mode_option:-1}

        case $mode_option in
            1) new_mode="debug" ;;
            2) new_mode="release" ;;
            *) new_mode=$CURRENT_MODE ;;
        esac

        # 更新配置
        sed -i "s/^SERVER_PORT=.*/SERVER_PORT=${new_port}/" "${TEMP_CONFIG}"
        sed -i "s/^SERVER_HOST=.*/SERVER_HOST=${new_host}/" "${TEMP_CONFIG}"
        sed -i "s/^SERVER_MODE=.*/SERVER_MODE=${new_mode}/" "${TEMP_CONFIG}"

        log_success "服务器配置已更新"
    else
        log_info "跳过服务器配置"
    fi
    echo ""
}

# 配置微博
configure_weibo() {
    log_step "配置微博平台"
    echo ""

    # 显示获取凭证说明
    echo "=========================================="
    echo "  微博龙虾助手 Webhook 凭证获取说明"
    echo "=========================================="
    echo ""
    echo "1. 下载微博龙虾助手:"
    echo "   - 访问: https://github.com/YourUsername/weibo-lobster"
    echo "   - 或搜索 \"微博龙虾助手\" GitHub 仓库"
    echo ""
    echo "2. 安装并运行微博龙虾助手:"
    echo "   - 解压并运行应用程序"
    echo "   - 登录您的微博账号"
    echo ""
    echo "3. 配置 Webhook 推送:"
    echo "   - 在龙虾助手设置中找到 Webhook 配置"
    echo "   - 配置 Webhook URL:"
    echo "     http://your-server-ip:${CURRENT_PORT:-5533}/weibo/webhook"
    echo "   - 设置 Webhook Token (自定义一个安全字符串)"
    echo ""
    echo "4. 获取凭证信息:"
    echo "   - Webhook URL: 您配置的推送地址"
    echo "   - Webhook Token: 您设置的 Token"
    echo ""
    echo "=========================================="
    echo ""

    # 读取当前配置
    CURRENT_WEBHOOK=$(grep "^WEIBO_WEBHOOK_URL=" "${CONFIG_FILE}" | cut -d'=' -f2)
    CURRENT_TOKEN=$(grep "^WEIBO_TOKEN=" "${CONFIG_FILE}" | cut -d'=' -f2)

    log_info "当前配置:"
    echo "  Webhook URL: ${CURRENT_WEBHOOK}"
    echo "  Webhook Token: ${CURRENT_TOKEN}"
    echo ""

    read -p "是否修改微博配置? (y/N): " modify_weibo
    if [[ "$modify_weibo" =~ ^[Yy]$ ]]; then
        read -p "输入 Webhook URL: " new_webhook
        read -p "输入 Webhook Token: " new_token

        # 更新配置
        sed -i "s|^WEIBO_WEBHOOK_URL=.*|WEIBO_WEBHOOK_URL=${new_webhook}|" "${TEMP_CONFIG}"
        sed -i "s|^WEIBO_TOKEN=.*|WEIBO_TOKEN=${new_token}|" "${TEMP_CONFIG}"

        log_success "微博配置已更新"
    else
        log_info "跳过微博配置"
    fi
    echo ""
}

# 配置 Claude
configure_claude() {
    log_step "配置 Claude AI"
    echo ""

    # 显示配置说明
    echo "=========================================="
    echo "  Claude Code 配置说明"
    echo "=========================================="
    echo ""
    echo "Claude API Key 和模型配置由 Claude Code CLI 管理。"
    echo ""
    echo "配置方式："
    echo "  1. 环境变量：export ANTHROPIC_API_KEY=\"sk-ant-xxxxx\""
    echo "  2. 配置文件：~/.config/claude/config.json"
    echo "  3. Claude Code 首次运行时会自动引导配置"
    echo ""
    echo "获取 API Key："
    echo "  - 访问：https://console.anthropic.com/"
    echo "  - 创建 API 密钥并复制"
    echo ""
    echo "=========================================="
    echo ""

    # 读取当前配置
    CURRENT_ENABLED=$(grep "^CLAUDE_ENABLED=" "${CONFIG_FILE}" | cut -d'=' -f2)

    log_info "当前配置:"
    echo "  启用状态: ${CURRENT_ENABLED}"
    echo ""

    read -p "是否启用 Claude Agent? (Y/n): " enable_claude
    enable_claude=${enable_claude:-Y}
    if [[ "$enable_claude" =~ ^[Yy]$ ]]; then
        new_enabled="true"
    else
        new_enabled="false"
    fi

    # 更新配置
    sed -i "s|^CLAUDE_ENABLED=.*|CLAUDE_ENABLED=${new_enabled}|" "${TEMP_CONFIG}"

    log_success "Claude 配置已更新"
    echo ""
}

# 配置 Codex
configure_codex() {
    log_step "配置 Codex (可选)"
    echo ""

    # 读取当前配置
    CURRENT_API_KEY=$(grep "^CODEX_API_KEY=" "${CONFIG_FILE}" | cut -d'=' -f2)
    CURRENT_MODEL=$(grep "^CODEX_MODEL=" "${CONFIG_FILE}" | cut -d'=' -f2)
    CURRENT_ENABLED=$(grep "^CODEX_ENABLED=" "${CONFIG_FILE}" | cut -d'=' -f2)

    log_info "当前配置:"
    echo "  API Key: ${CURRENT_API_KEY:0:10}..."
    echo "  模型: ${CURRENT_MODEL}"
    echo "  启用状态: ${CURRENT_ENABLED}"
    echo ""

    read -p "是否配置 Codex? (y/N): " configure_codex
    if [[ "$configure_codex" =~ ^[Yy]$ ]]; then
        read -p "输入 Codex API Key: " new_api_key

        echo "选择模型:"
        echo "  1) gpt-4"
        echo "  2) gpt-4-turbo"
        echo "  3) gpt-3.5-turbo"
        echo "  4) 自定义模型"
        read -p "输入选项 [1-4, 默认: 1]: " model_option
        model_option=${model_option:-1}

        case $model_option in
            1) new_model="gpt-4" ;;
            2) new_model="gpt-4-turbo" ;;
            3) new_model="gpt-3.5-turbo" ;;
            4)
                read -p "输入自定义模型名称: " new_model
                ;;
            *) new_model=$CURRENT_MODEL ;;
        esac

        read -p "启用 Codex? (y/N): " enable_codex
        if [[ "$enable_codex" =~ ^[Yy]$ ]]; then
            new_enabled="true"
        else
            new_enabled="false"
        fi

        # 更新配置
        sed -i "s|^CODEX_API_KEY=.*|CODEX_API_KEY=${new_api_key}|" "${TEMP_CONFIG}"
        sed -i "s|^CODEX_MODEL=.*|CODEX_MODEL=${new_model}|" "${TEMP_CONFIG}"
        sed -i "s|^CODEX_ENABLED=.*|CODEX_ENABLED=${new_enabled}|" "${TEMP_CONFIG}"

        log_success "Codex 配置已更新"
    else
        log_info "跳过 Codex 配置"
    fi
    echo ""
}

# 保存配置
save_config() {
    log_step "保存配置"
    echo ""

    # 显示配置摘要
    log_info "配置摘要:"
    echo ""
    cat "${TEMP_CONFIG}"
    echo ""

    read -p "确认保存配置? (Y/n): " confirm
    confirm=${confirm:-Y}

    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        # 备份原配置
        cp "${CONFIG_FILE}" "${CONFIG_FILE}.backup.$(date +%Y%m%d_%H%M%S)"

        # 保存新配置
        cp "${TEMP_CONFIG}" "${CONFIG_FILE}"
        chmod 600 "${CONFIG_FILE}"

        log_success "配置已保存"
        log_info "备份文件: ${CONFIG_FILE}.backup.*"
    else
        log_warning "配置未保存"
        rm -f "${TEMP_CONFIG}"
        exit 0
    fi

    # 清理临时文件
    rm -f "${TEMP_CONFIG}"
}

# 显示完成信息
show_completion() {
    echo ""
    log_success "=========================================="
    log_success "配置完成！"
    log_success "=========================================="
    echo ""
    log_info "配置文件: ${CONFIG_FILE}"
    echo ""
    log_warning "下一步操作:"
    echo "  1. 重启服务: systemctl restart weibo-ai-bridge"
    echo "  2. 查看状态: systemctl status weibo-ai-bridge"
    echo "  3. 查看日志: journalctl -u weibo-ai-bridge -f"
    echo ""
    log_info "测试服务:"
    echo "  健康检查: curl http://localhost:${CURRENT_PORT:-5533}/health"
    echo "  统计信息: curl http://localhost:${CURRENT_PORT:-5533}/stats"
    echo ""
}

# 主函数
main() {
    show_welcome
    check_config_file
    configure_server
    configure_weibo
    configure_claude
    configure_codex
    save_config
    show_completion

    echo ""
    log_success "配置流程完成！"
    echo ""
}

# 运行主函数
main "$@"