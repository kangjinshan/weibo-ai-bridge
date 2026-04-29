#!/usr/bin/env bash

set -euo pipefail

repo_root="${1:-$(pwd)}"
cd "${repo_root}"

exec_violations="$(find . -mindepth 1 -maxdepth 1 -type f -perm -111 -print | sed 's|^\./||' | sort || true)"

binary_violations="$(
    find . -mindepth 1 -maxdepth 1 -type f -print0 | while IFS= read -r -d '' f; do
        type_desc="$(file -b "${f}")"
        if echo "${type_desc}" | grep -Eq 'ELF|Mach-O|PE32'; then
            printf '%s (%s)\n' "${f#./}" "${type_desc}"
        fi
    done | sort || true
)"

if [[ -n "${exec_violations}" || -n "${binary_violations}" ]]; then
    echo "Root directory must not contain executable artifacts."
    if [[ -n "${exec_violations}" ]]; then
        echo ""
        echo "[Executable bit violations]"
        echo "${exec_violations}"
    fi
    if [[ -n "${binary_violations}" ]]; then
        echo ""
        echo "[Binary signature violations]"
        echo "${binary_violations}"
    fi
    exit 1
fi

echo "Root executable artifact check passed."
