#!/usr/bin/env bash

set -euo pipefail

PROJECT_NAME="weibo-ai-bridge"
BINARY_NAME="weibo-ai-bridge"
DEFAULT_REPO_URL="https://github.com/kangjinshan/weibo-ai-bridge.git"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
OS_NAME="${WEIBO_AI_BRIDGE_TEST_OS:-$(uname -s)}"
ARCH_NAME="${WEIBO_AI_BRIDGE_TEST_ARCH:-$(uname -m)}"

REPO_URL="${WEIBO_AI_BRIDGE_REPO_URL:-${DEFAULT_REPO_URL}}"
REF="${WEIBO_AI_BRIDGE_REF:-main}"
TARGET_BIN="${WEIBO_AI_BRIDGE_BIN:-}"
SERVICE_SCRIPT="${WEIBO_AI_BRIDGE_SERVICE_SCRIPT:-}"
RESTART_DELAY="${WEIBO_AI_BRIDGE_RESTART_DELAY:-8}"
SCOPE="${WEIBO_AI_BRIDGE_SCOPE:-auto}"
NO_RESTART=0
SELF_UPDATE_TMP_DIR=""

if [[ -t 1 ]]; then
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    RED='\033[0;31m'
    NC='\033[0m'
else
    GREEN=''
    YELLOW=''
    BLUE=''
    RED=''
    NC=''
fi

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

die() {
    echo -e "${RED}[ERROR]${NC} $1" >&2
    exit 1
}

cleanup() {
    if [[ -n "${SELF_UPDATE_TMP_DIR}" ]]; then
        rm -rf "${SELF_UPDATE_TMP_DIR}"
    fi
}

trap cleanup EXIT

usage() {
    cat <<EOF
用法: $(basename "$0") [--ref branch|tag|commit] [--repo url] [--target-bin path] [--scope system|user] [--no-restart]

从 GitHub 下载最新 weibo-ai-bridge，编译后原子替换当前二进制，并在成功回复后延迟重启服务。

环境变量:
  WEIBO_AI_BRIDGE_REPO_URL          Git 仓库地址，默认 ${DEFAULT_REPO_URL}
  WEIBO_AI_BRIDGE_REF               分支、tag 或 commit，默认 main
  WEIBO_AI_BRIDGE_BIN               目标二进制路径
  WEIBO_AI_BRIDGE_SERVICE_SCRIPT    服务管理脚本路径
  WEIBO_AI_BRIDGE_RESTART_DELAY     延迟重启秒数，默认 8
  WEIBO_AI_BRIDGE_SCOPE             Linux service scope，默认 auto
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --ref)
            [[ $# -ge 2 ]] || die "--ref 缺少参数"
            REF="$2"
            shift 2
            ;;
        --repo)
            [[ $# -ge 2 ]] || die "--repo 缺少参数"
            REPO_URL="$2"
            shift 2
            ;;
        --target-bin)
            [[ $# -ge 2 ]] || die "--target-bin 缺少参数"
            TARGET_BIN="$2"
            shift 2
            ;;
        --scope)
            [[ $# -ge 2 ]] || die "--scope 缺少参数"
            SCOPE="$2"
            shift 2
            ;;
        --no-restart)
            NO_RESTART=1
            shift
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

need_command() {
    command -v "$1" >/dev/null 2>&1 || die "缺少依赖: $1"
}

resolve_target_bin() {
    if [[ -n "${TARGET_BIN}" ]]; then
        echo "${TARGET_BIN}"
        return
    fi

    local repo_build="${REPO_ROOT}/build/${BINARY_NAME}"
    if [[ -f "${REPO_ROOT}/go.mod" || -f "${repo_build}" ]]; then
        echo "${repo_build}"
        return
    fi

    local installed="/opt/${PROJECT_NAME}/${BINARY_NAME}"
    if [[ -f "${installed}" ]]; then
        echo "${installed}"
        return
    fi

    if command -v "${BINARY_NAME}" >/dev/null 2>&1; then
        command -v "${BINARY_NAME}"
        return
    fi

    echo "${repo_build}"
}

asset_root_for_target() {
    local bin_path="$1"
    local raw_dir bin_dir parent_dir
    raw_dir="$(dirname "${bin_path}")"

    if [[ -d "${raw_dir}" ]]; then
        bin_dir="$(cd "${raw_dir}" && pwd)"
    else
        parent_dir="$(dirname "${raw_dir}")"
        if [[ -d "${parent_dir}" ]]; then
            bin_dir="$(cd "${parent_dir}" && pwd)/$(basename "${raw_dir}")"
        else
            bin_dir="${raw_dir}"
        fi
    fi

    if [[ "$(basename "${bin_dir}")" == "build" && -f "$(dirname "${bin_dir}")/go.mod" ]]; then
        dirname "${bin_dir}"
        return
    fi

    echo "${bin_dir}"
}

resolve_symlink_target() {
    local target="$1"
    if [[ ! -L "${target}" ]]; then
        echo "${target}"
        return
    fi

    local link_target
    link_target="$(readlink "${target}")"
    if [[ "${link_target}" = /* ]]; then
        echo "${link_target}"
        return
    fi

    echo "$(cd "$(dirname "${target}")" && pwd)/${link_target}"
}

checkout_source() {
    local dest="$1"

    log_info "下载源码: ${REPO_URL} (${REF})"
    if git clone --depth 1 --branch "${REF}" "${REPO_URL}" "${dest}" >/dev/null 2>&1; then
        return
    fi

    git clone "${REPO_URL}" "${dest}" >/dev/null
    git -C "${dest}" checkout "${REF}" >/dev/null
}

build_source() {
    local src="$1"
    local out="$2"

    log_info "下载 Go 依赖..."
    (cd "${src}" && go mod download)

    local version commit build_time
    version="$(git -C "${src}" describe --tags --always 2>/dev/null || echo "${REF}")"
    commit="$(git -C "${src}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
    build_time="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    log_info "编译 ${BINARY_NAME}: version=${version}, commit=${commit}"
    mkdir -p "$(dirname "${out}")"

    local build_env=(CGO_ENABLED=0)
    if [[ "${OS_NAME}" == "Darwin" && "${ARCH_NAME}" == "arm64" ]]; then
        build_env+=(GOOS=darwin GOARCH=arm64)
    fi

    (cd "${src}" && env "${build_env[@]}" go build \
        -ldflags "-X 'main.version=${version}' -X 'main.gitCommit=${commit}' -X 'main.buildTime=${build_time}'" \
        -o "${out}" ./cmd/server)
}

codesign_macos_binary() {
    local binary="$1"

    if [[ "${OS_NAME}" != "Darwin" ]]; then
        return
    fi

    local signer="${REPO_ROOT}/scripts/codesign-macos.sh"
    if [[ ! -x "${signer}" ]]; then
        signer="${SCRIPT_DIR}/codesign-macos.sh"
    fi
    if [[ ! -x "${signer}" ]]; then
        log_warn "未找到 macOS codesign 脚本，跳过签名"
        return
    fi

    if ! "${signer}" "${binary}"; then
        case "${WEIBO_AI_BRIDGE_CODESIGN:-auto}" in
            auto|"")
                log_warn "macOS codesign 失败，继续安装二进制: ${binary}"
                return
                ;;
            *)
                die "macOS codesign 失败: ${binary}"
                ;;
        esac
    fi
}

install_binary() {
    local built="$1"
    local target="$2"
    local target_dir tmp_target

    target_dir="$(dirname "${target}")"
    mkdir -p "${target_dir}"
    tmp_target="${target_dir}/.${BINARY_NAME}.new.$$"

    log_info "安装二进制: ${target}"
    cp "${built}" "${tmp_target}"
    chmod +x "${tmp_target}"
    codesign_macos_binary "${tmp_target}"
    mv "${tmp_target}" "${target}"
}

install_assets() {
    local src="$1"
    local asset_root="$2"

    if [[ -f "${asset_root}/go.mod" ]]; then
        log_info "检测到源码工作区，跳过覆盖 scripts/skills，仅更新构建产物"
        return
    fi

    log_info "更新附带资源: ${asset_root}/scripts, ${asset_root}/skills"
    rm -rf "${asset_root}/scripts" "${asset_root}/skills"
    cp -R "${src}/scripts" "${asset_root}/scripts"
    cp -R "${src}/skills" "${asset_root}/skills"
    chmod +x "${asset_root}/scripts/"*.sh
    find "${asset_root}/skills" -type f -name '*.sh' -exec chmod +x {} \;
}

resolve_service_script() {
    local asset_root="$1"

    if [[ -n "${SERVICE_SCRIPT}" && -f "${SERVICE_SCRIPT}" ]]; then
        echo "${SERVICE_SCRIPT}"
        return
    fi

    if [[ -f "${asset_root}/scripts/service.sh" ]]; then
        echo "${asset_root}/scripts/service.sh"
        return
    fi

    if [[ -f "${REPO_ROOT}/scripts/service.sh" ]]; then
        echo "${REPO_ROOT}/scripts/service.sh"
        return
    fi

    echo ""
}

schedule_restart() {
    local service_script="$1"

    if [[ "${NO_RESTART}" -eq 1 ]]; then
        log_warn "已按要求跳过自动重启"
        return
    fi
    if [[ -z "${service_script}" ]]; then
        log_warn "未找到服务管理脚本，无法自动重启；请手动重启服务"
        return
    fi

    local restart_log="${TMPDIR:-/tmp}/${PROJECT_NAME}-self-update-restart.log"
    local restart_command
    : > "${restart_log}"
    if [[ "${OS_NAME}" == "Linux" ]]; then
        restart_command="${service_script} restart --scope ${SCOPE}"
        log_info "将在 ${RESTART_DELAY}s 后重启服务: ${restart_command}"

        if command -v systemd-run >/dev/null 2>&1; then
            local systemd_scope="${SCOPE}"
            if [[ "${systemd_scope}" == "auto" ]]; then
                if [[ "${EUID}" -eq 0 ]]; then
                    systemd_scope="system"
                else
                    systemd_scope="user"
                fi
            fi

            local -a systemd_run_cmd
            systemd_run_cmd=(systemd-run)
            if [[ "${systemd_scope}" == "user" ]]; then
                systemd_run_cmd+=(--user)
            fi
            systemd_run_cmd+=(
                "--unit=${PROJECT_NAME}-self-update-restart"
                --collect
                "--on-active=${RESTART_DELAY}s"
                --timer-property=AccuracySec=1s
                "--description=${PROJECT_NAME} self-update restart"
                bash -c 'log="$1"; shift; exec "$@" >"${log}" 2>&1'
                _
                "${restart_log}"
                bash
                "${service_script}"
                restart
                --scope
                "${SCOPE}"
            )

            if "${systemd_run_cmd[@]}" >>"${restart_log}" 2>&1; then
                echo "WEIBO_AI_BRIDGE_RESTART_SCHEDULED=1"
                log_success "延迟重启已安排，日志: ${restart_log}"
                return
            fi
            log_warn "systemd-run 安排延迟重启失败，退回到后台进程方式；若当前脚本运行在服务 cgroup 内，可能只完成停止"
        fi

        nohup bash -c 'delay="$1"; shift; sleep "${delay}"; exec "$@"' _ "${RESTART_DELAY}" bash "${service_script}" restart --scope "${SCOPE}" >"${restart_log}" 2>&1 &
    else
        restart_command="${service_script} install && ${service_script} start"
        log_info "将在 ${RESTART_DELAY}s 后刷新并启动服务: ${restart_command}"
        nohup bash -c 'delay="$1"; service_script="$2"; sleep "${delay}"; bash "${service_script}" install && bash "${service_script}" start' _ "${RESTART_DELAY}" "${service_script}" >"${restart_log}" 2>&1 &
    fi
    echo "WEIBO_AI_BRIDGE_RESTART_SCHEDULED=1"
    log_success "延迟重启已安排，日志: ${restart_log}"
}

resolve_remote_commit() {
    local ref="$1"
    local output commit
    local patterns=(
        "${ref}"
        "refs/heads/${ref}"
        "refs/tags/${ref}^{}"
        "refs/tags/${ref}"
    )

    local pattern
    for pattern in "${patterns[@]}"; do
        output="$(git ls-remote "${REPO_URL}" "${pattern}" 2>/dev/null || true)"
        commit="$(printf '%s\n' "${output}" | awk 'NF >= 2 && $1 ~ /^[0-9a-fA-F]{40}$/ { print $1; exit }')"
        if [[ -n "${commit}" ]]; then
            echo "${commit}"
            return
        fi
    done

    if [[ "${ref}" =~ ^[0-9a-fA-F]{7,40}$ ]]; then
        echo "${ref}"
    fi
}

resolve_local_commit() {
    local target="$1"
    local commit=""

    if [[ -f "${target}" ]]; then
        commit="$(go version -m "${target}" 2>/dev/null | awk -F= '$1 ~ /vcs\.revision$/ && $2 ~ /^[0-9a-fA-F]{40}$/ { print $2; exit }')"
        if [[ -n "${commit}" ]]; then
            echo "${commit}"
            return
        fi
    fi

    if [[ -d "${REPO_ROOT}/.git" || -f "${REPO_ROOT}/.git" ]]; then
        git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || true
    fi
}

commits_match() {
    local local_commit="$1"
    local remote_commit="$2"

    local_commit="$(printf '%s' "${local_commit}" | tr '[:upper:]' '[:lower:]')"
    remote_commit="$(printf '%s' "${remote_commit}" | tr '[:upper:]' '[:lower:]')"

    [[ -n "${local_commit}" && -n "${remote_commit}" ]] || return 1
    [[ "${local_commit}" == "${remote_commit}" ]] && return 0

    if [[ ${#local_commit} -ge 7 && "${remote_commit}" == "${local_commit}"* ]]; then
        return 0
    fi
    if [[ ${#remote_commit} -ge 7 && "${local_commit}" == "${remote_commit}"* ]]; then
        return 0
    fi

    return 1
}

skip_if_current() {
    local target_bin="$1"
    local remote_commit local_commit

    remote_commit="$(resolve_remote_commit "${REF}")"
    local_commit="$(resolve_local_commit "${target_bin}")"

    if [[ -z "${remote_commit}" ]]; then
        log_warn "无法解析远端版本，继续执行完整升级流程"
        return 1
    fi
    if [[ -z "${local_commit}" ]]; then
        log_warn "无法解析本地版本，继续执行完整升级流程"
        return 1
    fi

    log_info "版本检查: local=${local_commit:0:12}, remote=${remote_commit:0:12}"
    if commits_match "${local_commit}" "${remote_commit}"; then
        log_success "本地版本已是最新: ${local_commit:0:12}"
        echo "WEIBO_AI_BRIDGE_ALREADY_UP_TO_DATE=1"
        return 0
    fi

    log_info "发现新版本: ${local_commit:0:12} -> ${remote_commit:0:12}"
    return 1
}

main() {
    need_command git
    need_command go

    local target_bin asset_root tmp_dir src built service_script
    target_bin="$(resolve_target_bin)"
    target_bin="$(resolve_symlink_target "${target_bin}")"
    asset_root="$(asset_root_for_target "${target_bin}")"

    log_info "目标二进制: ${target_bin}"
    if skip_if_current "${target_bin}"; then
        return
    fi

    tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/${PROJECT_NAME}-update.XXXXXX")"
    SELF_UPDATE_TMP_DIR="${tmp_dir}"
    src="${tmp_dir}/src"
    built="${tmp_dir}/${BINARY_NAME}"

    checkout_source "${src}"
    build_source "${src}" "${built}"
    install_binary "${built}" "${target_bin}"
    install_assets "${src}" "${asset_root}"

    service_script="$(resolve_service_script "${asset_root}")"
    schedule_restart "${service_script}"

    log_success "自更新完成"
}

main "$@"
