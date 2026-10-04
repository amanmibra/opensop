#!/bin/sh
# Install the sopkit CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/amanmibra/sopkit/main/install.sh | sh
#
# Installs uv (https://docs.astral.sh/uv/) first if it's missing, then installs sopkit as a uv tool.
# Options (environment variables):
#   SOPKIT_REF=<branch|tag|commit>   install a specific version (default: main)
#   SOPKIT_EXTRAS=""                 skip the HTTP server dependencies (default: server)

set -eu

REPO="https://github.com/amanmibra/sopkit"
REF="${SOPKIT_REF:-main}"
EXTRAS="${SOPKIT_EXTRAS-server}"

FRESH_UV=""

say() { printf 'sopkit: %s\n' "$1"; }
fail() { printf 'sopkit: error: %s\n' "$1" >&2; exit 1; }

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

if [ -n "$EXTRAS" ]; then
  SPEC="sopkit[$EXTRAS] @ git+$REPO@$REF"
else
  SPEC="sopkit @ git+$REPO@$REF"
fi

say "installing $SPEC"
uv tool install --force --quiet "$SPEC"

if command -v sopkit >/dev/null 2>&1; then
  say "installed: $(command -v sopkit)"
  if [ -n "$FRESH_UV" ]; then
    say "open a new terminal (or run: . \"\$HOME/.local/bin/env\") so your shell finds sopkit"
  fi
else
  BIN_DIR="$(uv tool dir --bin 2>/dev/null || echo "$HOME/.local/bin")"
  say "installed to $BIN_DIR, which isn't on your PATH yet."
  say "run 'uv tool update-shell' (or add $BIN_DIR to PATH) and open a new terminal."
fi

say "next: 'sopkit skills install', then run /sopkit-import in your coding agent"
