#!/bin/sh
# Install the opensop CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/amanmibra/opensop/main/install.sh | sh
#
# Installs uv (https://docs.astral.sh/uv/) first if it's missing, then installs opensop as a uv tool.
# Options (environment variables):
#   OPENSOP_REF=<branch|tag|commit>   install a specific version (default: main)

set -eu

REPO="https://github.com/amanmibra/opensop"
REF="${OPENSOP_REF:-main}"

FRESH_UV=""

say() { printf 'opensop: %s\n' "$1"; }
fail() { printf 'opensop: error: %s\n' "$1" >&2; exit 1; }

if ! command -v uv >/dev/null 2>&1; then
  say "uv not found; installing it from astral.sh"
  FRESH_UV=1
  if command -v curl >/dev/null 2>&1; then
    curl -LsSf https://astral.sh/uv/install.sh | sh
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- https://astral.sh/uv/install.sh | sh
  else
    fail "need curl or wget to install uv"
  fi
  # Make uv usable in this shell without restarting it.
  for dir in "${XDG_BIN_HOME:-}" "$HOME/.local/bin" "$HOME/.cargo/bin"; do
    if [ -n "$dir" ] && [ -x "$dir/uv" ]; then PATH="$dir:$PATH"; fi
  done
  command -v uv >/dev/null 2>&1 || fail "uv installed but not on PATH; open a new terminal and rerun"
fi

SPEC="opensop @ git+$REPO@$REF"

say "installing $SPEC"
uv tool install --force --quiet "$SPEC"

if command -v opensop >/dev/null 2>&1; then
  say "installed: $(command -v opensop)"
  if [ -n "$FRESH_UV" ]; then
    say "open a new terminal (or run: . \"\$HOME/.local/bin/env\") so your shell finds opensop"
  fi
else
  BIN_DIR="$(uv tool dir --bin 2>/dev/null || echo "$HOME/.local/bin")"
  say "installed to $BIN_DIR, which isn't on your PATH yet."
  say "run 'uv tool update-shell' (or add $BIN_DIR to PATH) and open a new terminal."
fi

say "next: 'opensop skills install', then run /opensop-import in your coding agent"
