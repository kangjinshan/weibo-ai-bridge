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
REPO_URL="github.com/kangjinshan/weibo-ai-bridge"
INSTALL_DIR="/opt/${PROJECT_NAME}"
CONFIG_DIR="/etc/${PROJECT_NAME}"
BINARY_NAME="weibo-ai-bridge"
SERVICE_NAME="${PROJECT_NAME}.service"
CONFIG_FILE="${CONFIG_DIR}/config.toml"
SKILL_NAME="weibo-skill-api"
TARGET_USER="${SUDO_USER:-${USER}}"
TARGET_HOME=""
OS_NAME="$(uname -s)"

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

resolve_target_home() {
    local user="$1"

    if [[ "${user}" == "${USER}" && -n "${HOME}" ]]; then
        echo "${HOME}"
        return
    fi

    if command -v getent &> /dev/null; then
        local home
        home="$(getent passwd "${user}" | cut -d: -f6 || true)"
        if [[ -n "${home}" ]]; then
            echo "${home}"
            return
        fi
    fi

    if command -v dscl &> /dev/null; then
        local home
        home="$(dscl . -read "/Users/${user}" NFSHomeDirectory 2>/dev/null | awk '{print $2}' || true)"
        if [[ -n "${home}" ]]; then
            echo "${home}"
            return
        fi
    fi

    local expanded_home
    expanded_home="$(eval echo "~${user}" 2>/dev/null || true)"
    if [[ -n "${expanded_home}" && "${expanded_home}" != "~${user}" ]]; then
        echo "${expanded_home}"
        return
    fi

    echo ""
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
    CGO_ENABLED=0 go build -o "${BINARY_NAME}" ./cmd/server

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

# 安装附带资源
install_assets() {
    log_info "安装项目附带资源..."

    rm -rf "${INSTALL_DIR}/scripts" "${INSTALL_DIR}/skills"
    cp -r "${PROJECT_DIR}/scripts" "${INSTALL_DIR}/scripts"
    cp -r "${PROJECT_DIR}/skills" "${INSTALL_DIR}/skills"
    chmod +x "${INSTALL_DIR}/scripts/"*.sh
    find "${INSTALL_DIR}/skills" -type f -name '*.sh' -exec chmod +x {} \;

    log_success "附带资源安装完成"
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

# 创建服务（Linux: systemd, macOS: launchd）
create_service() {
    local service_script="${INSTALL_DIR}/scripts/service.sh"
    if [[ ! -x "${service_script}" ]]; then
        log_warning "未找到服务管理脚本，跳过服务创建: ${service_script}"
        return
    fi

    case "${OS_NAME}" in
        Linux)
            if ! command -v systemctl &> /dev/null; then
                log_warning "systemd 不可用，跳过服务创建"
                return
            fi

            log_info "创建 Linux systemd 服务..."
            WEIBO_AI_BRIDGE_BIN="${INSTALL_DIR}/${BINARY_NAME}" \
            WEIBO_AI_BRIDGE_CONFIG_PATH="${CONFIG_FILE}" \
            WEIBO_AI_BRIDGE_ENV_FILE="${CONFIG_DIR}/.env" \
            WEIBO_AI_BRIDGE_SERVICE_USER="${TARGET_USER}" \
            "${service_script}" install --scope system

            log_success "服务已创建: ${SERVICE_NAME}"
            log_info "启动服务: ${service_script} start --scope system"
            log_info "开机自启: 已通过 systemd enable 配置"
            ;;
        Darwin)
            if [[ -z "${TARGET_USER}" || -z "${TARGET_HOME}" ]]; then
                log_warning "无法解析 macOS 目标用户，跳过 launchd 服务创建"
                return
            fi

            log_info "创建 macOS launchd 服务（用户级）..."
            su - "${TARGET_USER}" -c "WEIBO_AI_BRIDGE_BIN='${INSTALL_DIR}/${BINARY_NAME}' WEIBO_AI_BRIDGE_CONFIG_PATH='${CONFIG_FILE}' WEIBO_AI_BRIDGE_ENV_FILE='${CONFIG_DIR}/.env' '${service_script}' install" || {
                log_warning "launchd 服务创建失败，请手动执行: su - ${TARGET_USER} -c '${service_script} install'"
                return
            }

            log_success "launchd 服务已创建"
            log_info "启动服务: su - ${TARGET_USER} -c '${service_script} start'"
            ;;
        *)
            log_warning "当前系统(${OS_NAME})不支持自动创建服务，跳过"
            ;;
    esac
}

install_user_skills() {
    if [[ -z "${TARGET_USER}" || -z "${TARGET_HOME}" ]]; then
        log_warning "无法解析目标用户，跳过 Codex/Claude skill 安装"
        return
    fi

    if [[ ! -x "${INSTALL_DIR}/scripts/install-skills.sh" ]]; then
        log_warning "未找到 skill 安装脚本，跳过 Codex/Claude skill 安装"
        return
    fi

    log_info "为用户 ${TARGET_USER} 安装 Codex/Claude 微博 skills..."
    su - "${TARGET_USER}" -c "${INSTALL_DIR}/scripts/install-skills.sh --repo-root ${INSTALL_DIR} --user-home ${TARGET_HOME}" || {
        log_warning "Codex/Claude skill 安装失败，请手动运行: ${INSTALL_DIR}/scripts/install-skills.sh --repo-root ${INSTALL_DIR} --user-home ${TARGET_HOME}"
        return
    }

    log_success "Codex/Claude 微博 skills 安装完成"
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
    log_info "已安装微博 skills: ${TARGET_HOME}/.codex/skills/${SKILL_NAME} 和 ${TARGET_HOME}/.claude/skills/${SKILL_NAME}"
    echo ""
    log_warning "下一步操作："
    echo "  1. 运行配置向导: ${INSTALL_DIR}/scripts/setup.sh"
    echo "  2. 或手动编辑配置文件: vi ${CONFIG_FILE}"
    echo "  3. 如需环境变量补充配置，可编辑: ${CONFIG_DIR}/.env"
    if [[ "${OS_NAME}" == "Linux" ]]; then
        echo "  4. 启动服务: ${INSTALL_DIR}/scripts/service.sh start --scope system"
        echo "  5. 查看状态: ${INSTALL_DIR}/scripts/service.sh status --scope system"
    elif [[ "${OS_NAME}" == "Darwin" ]]; then
        echo "  4. 启动服务: su - ${TARGET_USER} -c '${INSTALL_DIR}/scripts/service.sh start'"
        echo "  5. 查看状态: su - ${TARGET_USER} -c '${INSTALL_DIR}/scripts/service.sh status'"
    else
        echo "  4. 手动前台运行: ${INSTALL_DIR}/${BINARY_NAME}"
    fi
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
    TARGET_HOME="$(resolve_target_home "${TARGET_USER}")"
    check_dependencies
    create_directories
    download_project
    build_project
    install_binary
    install_assets
    install_config
    create_service
    install_user_skills
    cleanup
    show_completion

    echo ""
    log_success "安装流程完成！"
    echo ""
}

# 运行主函数
main "$@"
