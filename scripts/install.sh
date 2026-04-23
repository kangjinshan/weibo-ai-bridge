#!/bin/bash

#############################################
# weibo-ai-bridge 安装脚本
# 用于自动安装和编译项目
#############################################

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 项目信息
PROJECT_NAME="weibo-ai-bridge"
REPO_URL="github.com/yourusername/weibo-ai-bridge"
INSTALL_DIR="/opt/${PROJECT_NAME}"
CONFIG_DIR="/etc/${PROJECT_NAME}"
BINARY_NAME="weibo-ai-bridge"
SERVICE_NAME="${PROJECT_NAME}.service"
CONFIG_FILE="${CONFIG_DIR}/config.toml"

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

# 检查是否以 root 权限运行
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "此脚本需要 root 权限运行"
        log_info "请使用: sudo $0"
        exit 1
    fi
}

# 检查依赖
check_dependencies() {
    log_info "检查依赖..."

    # 检查 Go
    if ! command -v go &> /dev/null; then
        log_error "Go 未安装"
        log_info "请访问 https://golang.org/dl/ 下载安装 Go 1.25 或更高版本"
        exit 1
    fi

    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    log_success "Go 已安装: $GO_VERSION"

    # 检查 Git
    if ! command -v git &> /dev/null; then
        log_warning "Git 未安装，某些功能可能受限"
        log_info "建议安装 Git: apt-get install git 或 yum install git"
    else
        log_success "Git 已安装"
    fi
}

# 创建必要的目录
create_directories() {
    log_info "创建目录结构..."

    mkdir -p "${INSTALL_DIR}"
    mkdir -p "${CONFIG_DIR}"
    mkdir -p "/var/log/${PROJECT_NAME}"

    log_success "目录创建完成"
}

# 下载项目（如果不在本地）
download_project() {
    if [[ -d "./${PROJECT_NAME}" ]] && [[ -f "./${PROJECT_NAME}/go.mod" ]]; then
        log_info "在本地找到项目，跳过下载"
        PROJECT_DIR="./${PROJECT_NAME}"
    else
        log_info "下载项目..."
        cd /tmp || exit 1

        if command -v git &> /dev/null; then
            git clone https://${REPO_URL}.git || {
                log_error "克隆项目失败"
                exit 1
            }
        else
            log_warning "Git 不可用，请手动下载项目到 ${INSTALL_DIR}"
            exit 1
        fi

        PROJECT_DIR="/tmp/${PROJECT_NAME}"
    fi
}

# 编译项目
build_project() {
    log_info "编译项目..."

    cd "${PROJECT_DIR}" || exit 1

    # 下载依赖
    log_info "下载 Go 依赖..."
    go mod download

    # 编译
    log_info "构建二进制文件..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "${BINARY_NAME}" ./cmd/server

    if [[ $? -eq 0 ]]; then
        log_success "编译成功"
    else
        log_error "编译失败"
        exit 1
    fi
}

# 安装二进制文件
install_binary() {
    log_info "安装二进制文件..."

    cp "${PROJECT_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/"
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

    # 创建软链接（可选）
    if [[ ! -L "/usr/local/bin/${BINARY_NAME}" ]]; then
        ln -sf "${INSTALL_DIR}/${BINARY_NAME}" "/usr/local/bin/${BINARY_NAME}"
        log_success "创建软链接: /usr/local/bin/${BINARY_NAME}"
    fi

    log_success "二进制文件安装完成"
}

# 安装配置文件
install_config() {
    log_info "安装配置文件..."

    if [[ -f "${CONFIG_FILE}" ]]; then
        log_warning "检测到已有配置文件，保留现有配置: ${CONFIG_FILE}"
        return
    fi

    if [[ -f "${PROJECT_DIR}/config/config.example.toml" ]]; then
        cp "${PROJECT_DIR}/config/config.example.toml" "${CONFIG_FILE}"
        chmod 600 "${CONFIG_FILE}"
        log_success "配置文件已安装: ${CONFIG_FILE}"
        log_warning "请编辑配置文件并填入真实 app_id / app_secret: ${CONFIG_FILE}"
        return
    fi

    log_error "未找到配置模板: ${PROJECT_DIR}/config/config.example.toml"
    exit 1
}

# 创建 systemd 服务（可选）
create_service() {
    if command -v systemctl &> /dev/null; then
        log_info "创建 systemd 服务..."

        cat > "/etc/systemd/system/${SERVICE_NAME}" << EOF
[Unit]
Description=Weibo AI Bridge Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${PROJECT_NAME}

# 环境变量
Environment="CONFIG_PATH=${CONFIG_FILE}"
EnvironmentFile=-${CONFIG_DIR}/.env

[Install]
WantedBy=multi-user.target
EOF

        systemctl daemon-reload
        log_success "服务已创建: ${SERVICE_NAME}"
        log_info "启动服务: systemctl start ${PROJECT_NAME}"
        log_info "开机自启: systemctl enable ${PROJECT_NAME}"
    else
        log_warning "systemd 不可用，跳过服务创建"
    fi
}

# 清理临时文件
cleanup() {
    if [[ -d "/tmp/${PROJECT_NAME}" ]] && [[ "${PROJECT_DIR}" == "/tmp/${PROJECT_NAME}" ]]; then
        log_info "清理临时文件..."
        rm -rf "/tmp/${PROJECT_NAME}"
        log_success "清理完成"
    fi
}

# 显示安装完成信息
show_completion() {
    echo ""
    log_success "=========================================="
    log_success "安装完成！"
    log_success "=========================================="
    echo ""
    log_info "安装位置: ${INSTALL_DIR}"
    log_info "配置文件: ${CONFIG_FILE}"
    log_info "日志目录: /var/log/${PROJECT_NAME}"
    echo ""
    log_warning "下一步操作："
    echo "  1. 编辑配置文件: vi ${CONFIG_FILE}"
    echo "  2. 如需环境变量补充配置，可编辑: ${CONFIG_DIR}/.env"
    echo "  3. 启动服务: systemctl start ${PROJECT_NAME}"
    echo "  4. 查看状态: systemctl status ${PROJECT_NAME}"
    echo ""
    log_info "获取帮助: ${INSTALL_DIR}/${BINARY_NAME} --help"
}

# 主函数
main() {
    echo ""
    echo "=========================================="
    echo "  ${PROJECT_NAME} 安装脚本"
    echo "=========================================="
    echo ""

    check_root
    check_dependencies
    create_directories
    download_project
    build_project
    install_binary
    install_config
    create_service
    cleanup
    show_completion

    echo ""
    log_success "安装流程完成！"
    echo ""
}

# 运行主函数
main "$@"
