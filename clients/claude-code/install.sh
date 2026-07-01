#!/usr/bin/env bash
# agentsmemory installer bootstrap.
#
# Copy-paste one-liner (from the landing page):
#
#   curl -fsSL https://raw.githubusercontent.com/atvirokodosprendimai/agentsmemory/main/clients/claude-code/install.sh | bash
#
# It detects your OS/arch, downloads the latest `aiagentmemory` binary from
# GitHub Releases into ~/.local/bin, and runs `aiagentmemory install`. Any extra
# arguments are forwarded to `install` — e.g. to do an isolated install with all
# recommended tools:
#
#   curl -fsSL <url>/install.sh | bash -s -- --sandbox myproject --recommended
#
# Environment:
#   AIAGENTMEMORY_VERSION     release tag to install (default: latest)
#   AIAGENTMEMORY_BIN_DIR     install dir (default: ~/.local/bin)
#   AIAGENTMEMORY_NO_INSTALL  set to any value to download only, skip `install`
set -euo pipefail

REPO="atvirokodosprendimai/agentsmemory"
BIN="aiagentmemory"
BIN_DIR="${AIAGENTMEMORY_BIN_DIR:-$HOME/.local/bin}"

info() { printf '==> %s\n' "$*"; }
err() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

# 1. Detect OS/arch and map to the release asset naming used by the build.
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
linux | darwin) ;;
*) err "unsupported OS '$os' (need linux or darwin)" ;;
esac
arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch="amd64" ;;
arm64 | aarch64) arch="arm64" ;;
*) err "unsupported arch '$arch' (need x86_64/amd64 or arm64/aarch64)" ;;
esac
asset="${BIN}-${os}-${arch}"

# 2. Resolve the download URL: a pinned tag, or GitHub's 'latest' redirect.
version="${AIAGENTMEMORY_VERSION:-latest}"
if [ "$version" = "latest" ]; then
	url="https://github.com/${REPO}/releases/latest/download/${asset}"
else
	url="https://github.com/${REPO}/releases/download/${version}/${asset}"
fi

# 3. Download to a temp file, then move it into place with the exec bit set.
info "downloading ${asset} (${version})"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fSL --progress-bar "$url" -o "$tmp" ||
	err "download failed: $url (has a release been published for ${os}/${arch}?)"

mkdir -p "$BIN_DIR"
install -m 0755 "$tmp" "$BIN_DIR/$BIN"
info "installed $BIN_DIR/$BIN"

# 4. PATH hint — ~/.local/bin is not always on PATH.
case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*)
	info "note: $BIN_DIR is not on your PATH. Add it, e.g.:"
	printf '      echo '\''export PATH="%s:$PATH"'\'' >> ~/.profile\n' "$BIN_DIR"
	;;
esac

# 5. Run the installer unless suppressed. Read from the terminal (/dev/tty) so
#    the token prompt works even though stdin is the curl pipe here.
if [ -n "${AIAGENTMEMORY_NO_INSTALL:-}" ]; then
	info "download-only (AIAGENTMEMORY_NO_INSTALL set). Run it yourself: $BIN install"
	exit 0
fi

info "running: $BIN install $*"
if [ -t 0 ]; then
	"$BIN_DIR/$BIN" install "$@"
elif [ -e /dev/tty ]; then
	"$BIN_DIR/$BIN" install "$@" </dev/tty
else
	# No terminal at all (CI): install non-interactively; the token prompt is
	# skipped and can be completed later with `aiagentmemory install --token …`.
	"$BIN_DIR/$BIN" install --yes "$@"
fi
