#!/usr/bin/env bash
# Compares the Rust opensop binary with the Go one (v0.0.4) on every fixture, example and
# scenario the tests use.
#
#   scripts/compare-go.sh            # needs go, cargo, git and python3
#
# Built files (prompts, tool.json, lock.json) must be byte-identical: any difference fails the
# script. Differences in stdout, stderr or exit codes are printed for review; the expected
# ones are clap's wording for argument errors and help, YAML 1.2 (yes/no/on/off and 1:30 are
# text), YAML and field error wording, and the git/IO error prefix.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

if [ ! -d "$REPO/cmd/opensop" ]; then
  echo "The Go code isn't in this checkout. Point GO_SRC at a checkout that has it (e.g. tag v0.0.4)." >&2
  [ -n "${GO_SRC:-}" ] || exit 2
fi
GO_SRC="${GO_SRC:-$REPO}"
(cd "$GO_SRC" && go build -o "$WORK/opensop-go" ./cmd/opensop) || exit 2
(cd "$REPO" && cargo build --quiet --release) || exit 2
GO="$WORK/opensop-go"
RS="$REPO/target/release/opensop"

FIXTURE="$REPO/tests/fixtures/restaurants"
EXAMPLE="$REPO/examples/livekit-restaurant/sops"
same=0 differ=0 build_fail=0

# run NAME DIR ARGS...: runs both binaries in DIR and compares stdout, stderr and exit code.
run() {
  local name="$1" dir="$2"
  shift 2
  for bin in go rs; do
    local exe="$GO"
    [ "$bin" = rs ] && exe="$RS"
    (cd "$dir" && GITHUB_OUTPUT="${GH_OUT:+$GH_OUT.$bin}" GITHUB_STEP_SUMMARY="${GH_SUM:+$GH_SUM.$bin}" \
      "$exe" "$@" >"$WORK/out.$bin" 2>"$WORK/err.$bin"; echo "exit $?" >"$WORK/code.$bin")
  done
  local extra=()
  [ -n "${GH_OUT:-}" ] && extra+=("$GH_OUT" "$GH_SUM")
  local ok=1
  for f in out err code; do cmp -s "$WORK/$f.go" "$WORK/$f.rs" || ok=0; done
  for f in "${extra[@]}"; do cmp -s "$f.go" "$f.rs" || ok=0; done
  if [ $ok = 1 ]; then
    same=$((same + 1))
    echo "same   $name"
  else
    differ=$((differ + 1))
    echo "DIFF   $name   (opensop $*)"
    for f in code out err; do
      cmp -s "$WORK/$f.go" "$WORK/$f.rs" || diff -u --label "go $f" --label "rust $f" "$WORK/$f.go" "$WORK/$f.rs" | sed 's/^/       /' | head -40
    done
    for f in "${extra[@]}"; do
      cmp -s "$f.go" "$f.rs" || diff -u --label "go $(basename "$f")" --label "rust $(basename "$f")" "$f.go" "$f.rs" | sed 's/^/       /' | head -20
    done
  fi
}

# builds NAME ROOT: renders ROOT with both binaries and requires identical files.
builds() {
  local name="$1" root="$2"
  rm -rf "$WORK/b.go" "$WORK/b.rs"
  "$GO" render "$root" --out "$WORK/b.go" >/dev/null 2>&1
  "$RS" render "$root" --out "$WORK/b.rs" >/dev/null 2>&1
  if diff -r "$WORK/b.go" "$WORK/b.rs" >/dev/null 2>&1; then
    echo "same   build files: $name ($(ls "$WORK/b.go" 2>/dev/null | wc -l | tr -d ' ') files)"
  else
    build_fail=$((build_fail + 1))
    echo "FAIL   build files differ: $name"
    diff -r "$WORK/b.go" "$WORK/b.rs" | head -40 | sed 's/^/       /'
  fi
}

# fresh: a copy of the fixture's sops/ in $WORK/repo/sops, committed on main.
fresh() {
  rm -rf "$WORK/repo"
  mkdir -p "$WORK/repo"
  cp -R "$FIXTURE/sops" "$WORK/repo/sops"
  rm -rf "$WORK/repo/sops/build"
  (cd "$WORK/repo" && git init -q -b main && git add . && git -c user.email=t@t -c user.name=t commit -qm init)
}

# edit FILE OLD NEW (in $WORK/repo/sops)
edit() {
  python3 - "$WORK/repo/sops/$1" "$2" "$3" <<'EOF'
import sys
path, old, new = sys.argv[1:]
text = open(path).read()
assert old in text, f"{old!r} not in {path}"
open(path, "w").write(text.replace(old, new))
EOF
}

S="$WORK/repo/sops"

echo "== fixture and example"
builds fixture "$FIXTURE/sops"
builds example "$EXAMPLE"
for root in "$FIXTURE/sops" "$EXAMPLE"; do
  n="$(basename "$(dirname "$root")")"
  run "$n validate" "$REPO" validate "$root"
  run "$n agents" "$REPO" agents "$root"
  run "$n agents --json" "$REPO" agents "$root" --json
  run "$n check" "$REPO" check "$root"
  run "$n check --json" "$REPO" check "$root" --json
  run "$n affected" "$REPO" affected "$root"
  run "$n affected --format json" "$REPO" affected "$root" --format json
  run "$n affected --format platform-ids" "$REPO" affected "$root" --format platform-ids
done
run "example render --check" "$REPO" render "$EXAMPLE" --check
run "example plan (vs build/)" "$REPO" plan "$EXAMPLE"
run "overlap originals" "$REPO" overlap "$FIXTURE/originals"
run "overlap expected" "$REPO" overlap "$FIXTURE/expected"
run "compare originals" "$REPO" compare "$FIXTURE/sops" --originals "$FIXTURE/originals"
run "compare expected" "$REPO" compare "$FIXTURE/sops" --originals "$FIXTURE/expected"
run "compare no prompts" "$REPO" compare "$FIXTURE/sops" --originals "$REPO/spec"
run "guide" "$REPO" guide

echo "== render paths"
fresh
run "render ./sops/" "$WORK/repo" render ./sops/
run "render --out" "$WORK/repo" render sops --out out//x/
run "render --check up to date" "$WORK/repo" render sops --check
edit bases/closing.md "repeat the order total" "repeat the order and total"
run "render --check out of date" "$WORK/repo" render sops --check
run "plan vs build/" "$WORK/repo" plan sops
run "plan vs build/ --summary" "$WORK/repo" plan sops --summary

echo "== edits: build files, plan, affected and check against main"
scenario() { # name, then file/old/new triples
  local name="$1"
  shift
  fresh
  while [ $# -gt 0 ]; do edit "$1" "$2" "$3"; shift 3; done
  builds "$name" "$S"
  run "$name: validate" "$WORK/repo" validate sops
  run "$name: plan --against main" "$WORK/repo" plan sops --against main
  run "$name: plan --json" "$WORK/repo" plan sops --against main --json
  run "$name: affected" "$WORK/repo" affected sops --against main
  run "$name: affected --all-if-none --format json" "$WORK/repo" affected sops --against main --all-if-none --format json
  GH_OUT="$WORK/gh_out" GH_SUM="$WORK/gh_sum" run "$name: affected --ci" "$WORK/repo" affected sops --against main --all-if-none --ci
  rm -f "$WORK"/gh_out.* "$WORK"/gh_sum.*
  run "$name: affected --ci (no GITHUB_OUTPUT)" "$WORK/repo" affected sops --against main --ci
  run "$name: check" "$WORK/repo" check sops
  run "$name: check --json" "$WORK/repo" check sops --json
}
scenario "no change"
scenario "brand voice" bases/brand-voice.md briefly concisely
scenario "brand voice + reservations" bases/brand-voice.md briefly concisely \
  procedures/reservations.yaml "Never double-book a table" "Never double-book or overbook a table"
scenario "allergen step" procedures/allergen-check.yaml "Name the specific allergen" "Repeat the specific allergen"
scenario "targeting removed" procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[sakura-sushi]"
scenario "default variable" opensop.yaml "the manager on duty" "the shift lead"
scenario "pizza context" bases/pizza-context.md '12" and 16"' '10", 12" and 16"'
scenario "exclude unlocked base" agents/sakura-sushi.yaml "exclude: [delivery-handling]" "exclude: [delivery-handling, closing]"
scenario "platform ref targeting" procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" '[sakura-sushi, "livekit:tonys-pizza"]'
scenario "vapi agent" agents/luigis-trattoria.yaml "livekit: luigis-trattoria" "vapi: asst_9f3e"
scenario "quoted colon" procedures/reservations.yaml "  - Never double-book a table" '  - "Never say: fully booked"'
scenario "delivery tool" procedures/large-orders.yaml "delivery: auto" "delivery: tool"
scenario "non-ASCII text" bases/brand-voice.md "briefly" "briefly — ¡olé! 😀"
scenario "useless exclude" agents/tonys-pizza.yaml "inherits: [pizza-context]" "inherits: [pizza-context]
exclude: [reservations]"
scenario "check conflicts" \
  agents/tonys-pizza.yaml "Pickup only after 10pm. Cash and card." "Pickup only after 11pm. Cash and card. Speak warmly and briefly. Always confirm the delivery address." \
  agents/tonys-pizza.yaml "  menu_allergen_link: tonys.com/allergens" "  menu_allergen_link: tonys.com/allergens
  old_phone: 555-0100" \
  procedures/delivery-handling.yaml "procedureSteps:" "forbiddenActions:
  - Never confirm the delivery address
procedureSteps:"
scenario "shared negation" bases/closing.md "pickup or delivery time." "pickup or delivery time. Before hanging up, never repeat the order total and the pickup or delivery time."
scenario "number in longer sentence" agents/tonys-pizza.yaml "Pickup only after 10pm. Cash and card." "Pickup and delivery until 11pm. Delivery until 10pm. Cash and card."

echo "== added and removed agents"
fresh
rm "$S/agents/sakura-sushi.yaml"
sed 's/livekit: luigis-trattoria/livekit: luigis-brooklyn/' "$S/agents/luigis-trattoria.yaml" >"$S/agents/luigis-brooklyn.yaml"
edit procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[luigis-trattoria]"
builds "added/removed" "$S"
run "added/removed: plan" "$WORK/repo" plan sops --against main
run "added/removed: plan --json" "$WORK/repo" plan sops --against main --json
run "added/removed: affected --format json" "$WORK/repo" affected sops --against main --format json

echo "== requested agents"
fresh
edit agents/luigis-trattoria.yaml "livekit: luigis-trattoria" "vapi: asst_9f3e"
run "requested ids" "$WORK/repo" affected sops --agents "asst_9f3e, sakura-sushi vapi:asst_9f3e" --format json
run "requested platform ids" "$WORK/repo" affected sops --agents "asst_9f3e sakura-sushi" --format platform-ids
run "requested empty" "$WORK/repo" affected sops --agents "" --all-if-none
run "requested unknown" "$WORK/repo" affected sops --agents "la-casa"
run "against a ref without files" "$WORK/repo" affected sops --against "$(cd "$WORK/repo" && git hash-object -t tree /dev/null)"
run "bad ref" "$WORK/repo" plan sops --against nope

echo "== validation errors"
invalid() { # name file old new
  fresh
  edit "$2" "$3" "$4"
  run "$1: validate" "$WORK/repo" validate sops
  run "$1: render" "$WORK/repo" render sops
}
invalid "locked" agents/sakura-sushi.yaml "exclude: [delivery-handling]" "exclude: [delivery-handling, brand-voice]"
invalid "inheritance_cycle" bases/restaurant-host.md $'---\n---' $'---\ninherits: [pizza-context]\n---'
invalid "unknown_base (agent)" agents/tonys-pizza.yaml "inherits: [pizza-context]" "inherits: [pasta-context]"
invalid "unknown_base (base)" bases/pizza-context.md "inherits: [restaurant-host]" "inherits: [nope]"
invalid "unknown_agent" procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[sakura-sushi, luigis]"
invalid "unknown_agent (exclude)" procedures/reservations.yaml "agents: [sakura-sushi, luigis-trattoria]" "agents: [sakura-sushi, luigis-trattoria]
exclude: [nobody]"
invalid "unknown_block" agents/tonys-pizza.yaml "inherits: [pizza-context]" "inherits: [pizza-context]
exclude: [nothing]"
invalid "unknown_sop" opensop.yaml "sop_order: [allergen-check]" "sop_order: [allergen-check, nope]"
invalid "unset_variable" agents/luigis-trattoria.yaml "  menu_allergen_link: luigis.com/menu#allergens
" ""
invalid "duplicate_platform_ref" agents/sakura-sushi.yaml "livekit: sakura-sushi" "livekit: tonys-pizza"
invalid "duplicate_id" bases/closing.md "Before hanging up" "Before hanging up"
cp "$S/bases/closing.md" "$S/procedures/closing.yaml" 2>/dev/null
printf 'name: Closing\ndescription: x\n' >"$S/procedures/closing.yaml"
run "duplicate_id: validate" "$WORK/repo" validate sops
invalid "two platforms" agents/sakura-sushi.yaml "livekit: sakura-sushi" "livekit: sakura-sushi
vapi: asst_123"
invalid "no platform" agents/sakura-sushi.yaml "livekit: sakura-sushi" ""
invalid "id_mismatch" procedures/reservations.yaml "name: Reservations" "id: bookings
name: Reservations"
invalid "unknown field" procedures/reservations.yaml "name: Reservations" "name: Reservations
steps: []"
invalid "missing name" procedures/reservations.yaml "name: Reservations" ""
invalid "wrong type" procedures/reservations.yaml "name: Reservations" "name: [Reservations]"
invalid "bad delivery" procedures/large-orders.yaml "delivery: auto" "delivery: sometimes"
invalid "bad position" bases/closing.md "position: bottom" "position: middle"
invalid "locked yes" bases/brand-voice.md "locked: true" "locked: yes"
invalid "missing_goal" procedures/reservations.yaml "description: The customer has a confirmed table, or knows exactly why one isn't available.
" ""
invalid "colon_in_step" procedures/reservations.yaml "  - Never double-book a table" "  - Never say: we're fully booked"
invalid "colon in field" procedures/reservations.yaml "scope: The customer wants" "scope: Note: the customer wants"
invalid "unquoted no (YAML 1.2: text)" procedures/reservations.yaml "  - Never double-book a table" "  - no"
invalid "unquoted 10" procedures/reservations.yaml "  - Never double-book a table" "  - 10"
invalid "unquoted true" procedures/reservations.yaml "  - Never double-book a table" "  - true"
invalid "1:30 (YAML 1.2: text)" procedures/reservations.yaml "  - Never double-book a table" "  - 1:30"
invalid "empty_step" procedures/reservations.yaml "  - Never double-book a table" "  -"
invalid "bad YAML" procedures/reservations.yaml "name: Reservations" "name: [Reservations"
invalid "not a mapping" procedures/reservations.yaml "name: Reservations" "- just a list"
invalid "version 2" opensop.yaml "version: 1" "version: 2"
invalid "variables not text" agents/tonys-pizza.yaml "  menu_allergen_link: tonys.com/allergens" "  menu_allergen_link: tonys.com/allergens
  count: 10"
fresh
rm "$S/opensop.yaml"
run "missing_config" "$WORK/repo" validate sops

echo "== skills"
mkdir -p "$WORK/skills.go" "$WORK/skills.rs"
for bin in go rs; do
  exe="$GO"
  [ "$bin" = rs ] && exe="$RS"
  (cd "$WORK/skills.$bin" && "$exe" skills install >out.txt && "$exe" skills install --agent codex --dir custom >>out.txt)
done
if diff -r "$WORK/skills.go" "$WORK/skills.rs" >/dev/null; then echo "same   skills install (files and output)"; same=$((same + 1)); else
  echo "DIFF   skills install"; diff -r "$WORK/skills.go" "$WORK/skills.rs" | head -20; differ=$((differ + 1)); fi

echo "== argument errors and help (clap wording expected to differ)"
run "no command" "$REPO"
run "unknown command" "$REPO" bogus
run "unknown flag" "$REPO" render --foo
run "bad --format" "$REPO" affected --format xx
run "missing --originals" "$REPO" compare
run "--out without value" "$REPO" render --out
run "bad skills action" "$REPO" skills foo
run "render -h" "$REPO" render -h

echo
echo "$same same, $differ with output differences, $build_fail with different build files"
[ "$build_fail" = 0 ]
