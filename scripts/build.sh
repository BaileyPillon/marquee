#!/usr/bin/env bash
# Builds Marquee on Linux, macOS or Git Bash: the web UI, brotli precompression,
# and a release archive for each platform, plus SHA256SUMS and a size table.
# This is the bash twin of scripts/build.ps1; CI and the release workflow run it.
#
#   scripts/build.sh                    everything below
#   scripts/build.sh --skip-ui          reuse the existing web/dist (backend-only work)
#   scripts/build.sh --check-size       exit 3 if a size budget is exceeded (CI does this)
#   scripts/build.sh --targets "linux/amd64 windows/amd64"
#                                       only these platforms (default: all five)
#
# Platforms: linux/amd64, linux/arm64, linux/armv7, darwin/arm64, windows/amd64.
#
# Output, under bin/ (override with MARQUEE_OUT):
#   marquee_<version>_linux_amd64.tar.gz   (and linux_arm64, linux_armv7, darwin_arm64)
#   marquee_<version>_windows_amd64.zip
#   SHA256SUMS
#   build/<os>_<arch>/marquee[.exe]        the bare binaries
#
# Environment:
#   MARQUEE_VERSION      version to stamp (default: the VERSION file). The release
#                        workflow sets it from the tag.
#   MARQUEE_OUT          output folder (default: bin)
#   SOURCE_DATE_EPOCH    if set, archive members get this modified time
#   CI                   if set, "npm ci" always runs (otherwise only without node_modules)
#
# Needs: Go (see go.mod), Node + npm unless --skip-ui, bash, tar, gzip, and one of
# sha256sum or shasum. The Windows .zip needs "zip" or python.

set -euo pipefail

ALL_TARGETS="linux/amd64 linux/arm64 linux/armv7 darwin/arm64 windows/amd64"
MIB=1048576
LINUX_AMD64_BUDGET=$((17 * MIB))   # the release archive for linux/amd64
WINDOWS_EXE_BUDGET=$((25 * MIB))   # the bare marquee.exe

skip_ui=0
check_size=0
targets="$ALL_TARGETS"

say() { printf '==> %s\n' "$*"; }
die() { printf 'Build FAILED: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-ui)     skip_ui=1 ;;
    --check-size)  check_size=1 ;;
    --targets)     shift; [[ $# -gt 0 ]] || die "--targets needs a value"; targets="$1" ;;
    -h|--help)     sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'; exit 0 ;;
    *)             die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

out="${MARQUEE_OUT:-bin}"
version="${MARQUEE_VERSION:-}"
if [[ -z "$version" ]]; then
  [[ -f VERSION ]] || die "VERSION file is missing"
  version="$(tr -d '[:space:]' < VERSION)"
fi
[[ -n "$version" ]] || die "the version is empty"
[[ "$version" =~ ^[0-9A-Za-z._+-]+$ ]] || die "the version \"$version\" has characters that do not belong in a file name"

[[ -n "${targets// /}" ]] || die "no targets to build"
command -v go >/dev/null 2>&1 || die "go is not on the PATH"
if [[ $skip_ui -eq 0 ]]; then
  command -v npm >/dev/null 2>&1 || die "npm is not on the PATH (use --skip-ui to reuse web/dist)"
fi

export CGO_ENABLED=0
printf 'Marquee %s\n' "$version"

# ---- web UI -------------------------------------------------------------------
if [[ $skip_ui -eq 1 ]]; then
  say "UI build skipped (--skip-ui); embedding the existing web/dist"
else
  say "web: npm ci, npm run build"
  (
    cd web
    if [[ ! -d node_modules || -n "${CI:-}" ]]; then
      npm ci
    fi
    npm run build
  )
fi
[[ -f web/dist/index.html ]] || die "web/dist/index.html is missing: build the UI first"

# ---- precompress ----------------------------------------------------------------
say "precompress web/dist"
go run ./tools/precompress web/dist

# ---- helpers ----------------------------------------------------------------------
file_size() { wc -c < "$1" | tr -d '[:space:]'; }

mib() { awk -v b="$1" 'BEGIN { printf "%.2f MiB", b / 1048576 }'; }

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$@"
  else die "neither sha256sum nor shasum is available"
  fi
}

# make_tar <out.tar.gz> <stage dir holding marquee, LICENSE, README.md>
make_tar() {
  local dest="$1" stage="$2" plain="${1%.gz}"
  rm -f "$dest" "$plain"
  if tar --version 2>/dev/null | grep -q 'GNU tar'; then
    local opts=(--owner=0 --group=0 --numeric-owner)
    if [[ -n "${SOURCE_DATE_EPOCH:-}" ]]; then opts+=("--mtime=@${SOURCE_DATE_EPOCH}"); fi
    # --mode applies to everything a call adds, so the program and the plain files
    # go in separately. (Git Bash on Windows cannot mark a file executable.)
    tar "${opts[@]}" --mode=0755 -cf "$plain" -C "$stage" marquee
    tar "${opts[@]}" --mode=0644 -rf "$plain" -C "$stage" LICENSE README.md
  else
    # bsdtar (macOS): the modes are real there, so set them and archive in one go.
    chmod 0755 "$stage/marquee"; chmod 0644 "$stage/LICENSE" "$stage/README.md"
    tar --uid 0 --gid 0 --uname root --gname root -cf "$plain" -C "$stage" marquee LICENSE README.md
  fi
  gzip -9 -n -f "$plain"
}

# make_zip <out.zip> <stage dir holding marquee.exe, LICENSE, README.md>
make_zip() {
  local dest="$1" stage="$2"
  rm -f "$dest"
  case "$dest" in /*) ;; *) dest="$root/$dest" ;; esac
  if command -v zip >/dev/null 2>&1; then
    (cd "$stage" && zip -q -9 -X "$dest" marquee.exe LICENSE README.md)
  else
    local py=""
    for c in python3 python; do
      if command -v "$c" >/dev/null 2>&1 && "$c" -c 'import zipfile' >/dev/null 2>&1; then py="$c"; break; fi
    done
    [[ -n "$py" ]] || die "need \"zip\" or python to make the Windows archive"
    "$py" - "$dest" "$stage" <<'PY'
import os, sys, zipfile
dest, stage = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for name in ("marquee.exe", "LICENSE", "README.md"):
        z.write(os.path.join(stage, name), name)
PY
  fi
}

# ---- binaries and archives -----------------------------------------------------------
mkdir -p "$out/build"
archives=()
rows=()      # "label|bytes|budget"

for target in $targets; do
  os="${target%%/*}"
  arch="${target##*/}"
  goarch="$arch"; goarm=""
  case "$arch" in
    armv7) goarch=arm; goarm=7 ;;
    amd64|arm64) ;;
    *) die "unknown architecture in target \"$target\"" ;;
  esac
  case "$os" in
    linux|darwin|windows) ;;
    *) die "unknown operating system in target \"$target\"" ;;
  esac
  exe="marquee"; [[ "$os" == windows ]] && exe="marquee.exe"

  dir="$out/build/${os}_${arch}"
  mkdir -p "$dir"
  say "go build ($target)"
  envs=(GOOS="$os" GOARCH="$goarch" CGO_ENABLED=0)
  if [[ -n "$goarm" ]]; then envs+=(GOARM="$goarm"); fi
  env "${envs[@]}" go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=${version}" -o "$dir/$exe" ./cmd/marquee \
    || die "go build failed for $target"

  stage="$dir/.stage"
  rm -rf "$stage"; mkdir -p "$stage"
  cp "$dir/$exe" "$stage/$exe"
  cp LICENSE README.md "$stage/"

  if [[ "$os" == windows ]]; then
    archive="$out/marquee_${version}_${os}_${arch}.zip"
    say "zip ($target)"
    make_zip "$archive" "$stage"
  else
    archive="$out/marquee_${version}_${os}_${arch}.tar.gz"
    say "tar.gz ($target)"
    make_tar "$archive" "$stage"
  fi
  rm -rf "$stage"
  archives+=("$(basename "$archive")")

  exe_budget=""; archive_budget=""
  if [[ "$target" == "windows/amd64" ]]; then exe_budget="$WINDOWS_EXE_BUDGET"; fi
  if [[ "$target" == "linux/amd64" ]]; then archive_budget="$LINUX_AMD64_BUDGET"; fi
  rows+=("$dir/$exe|$(file_size "$dir/$exe")|$exe_budget")
  rows+=("$archive|$(file_size "$archive")|$archive_budget")
done

# ---- checksums ---------------------------------------------------------------------------
if [[ ${#archives[@]} -gt 0 ]]; then
  say "SHA256SUMS"
  (cd "$out" && sha256 "${archives[@]}" > SHA256SUMS)
fi

# ---- size table ----------------------------------------------------------------------------
over=0
printf '\nSizes\n'
printf '%-58s %12s %12s  %s\n' "artifact" "bytes" "size" "budget"
for row in "${rows[@]}"; do
  IFS='|' read -r label bytes budget <<<"$row"
  label="${label#"$out"/}"   # show paths relative to the output folder
  note=""
  if [[ -n "$budget" ]]; then
    if (( bytes <= budget )); then note="ok (<= $((budget / MIB)) MiB)"
    else note="OVER $((budget / MIB)) MiB"; over=1
    fi
  fi
  printf '%-58s %12s %12s  %s\n' "$label" "$bytes" "$(mib "$bytes")" "$note"
done
printf '\n'

if [[ $over -eq 1 && $check_size -eq 1 ]]; then
  printf 'Build FAILED: a size budget is exceeded (see the table).\n' >&2
  exit 3
fi
printf 'Build OK\n'
