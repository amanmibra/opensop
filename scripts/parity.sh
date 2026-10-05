#!/usr/bin/env bash
# Run the Python CLI and the Go binary on the same inputs and diff everything they produce:
# stdout, stderr, exit code and any files written.
#
#   scripts/parity.sh            build bin/opensop, then compare every case
#   VERBOSE=1 scripts/parity.sh  print each case
#   OPENSOP_GO_BIN=path scripts/parity.sh  compare a prebuilt binary instead
#
# Needs: go, uv, git, python3. Each case runs both CLIs in separate copies of a scenario
# folder, with relative paths, so outputs can be compared byte for byte.
#
# Known, intentional differences are normalized before comparing (see `normalize`):
#   - invalid_yaml messages come from different YAML parsers; only the path, the code and
#     whether the "put the text in quotes" hint is present are compared.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/opensop-parity.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

(cd "$REPO" && go build -o "$WORK/opensop-go" ./cmd/opensop) || { echo "go build failed"; exit 1; }
(cd "$REPO" && uv sync --quiet) || { echo "uv sync failed"; exit 1; }
PY_BIN="$REPO/.venv/bin/opensop"
GO_BIN="${OPENSOP_GO_BIN:-$WORK/opensop-go}"  # override to test another build
export COLUMNS=80 PYTHONHASHSEED=0 GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
unset GITHUB_OUTPUT GITHUB_STEP_SUMMARY

FIXTURE="$REPO/tests/fixtures/restaurants"
EXAMPLE="$REPO/examples/livekit-restaurant/sops"
PASS=0
FAIL=0
FAILED=()

# --- helpers ----------------------------------------------------------------------------------

# A fresh scenario folder in $SCEN with sops/, originals/ and expected/ from the fixture.
new_scenario() {
  SCEN="$WORK/scenario"
  rm -rf "$SCEN"
  mkdir -p "$SCEN"
  cp -R "$FIXTURE/sops" "$SCEN/sops"
  cp -R "$FIXTURE/originals" "$SCEN/originals"
  cp -R "$FIXTURE/expected" "$SCEN/expected"
  cp -R "$EXAMPLE" "$SCEN/example"
}

# edit FILE OLD NEW: replace text (must be present).
edit() {
  python3 - "$SCEN/$1" "$2" "$3" <<'EOF' || { echo "edit failed: $1"; exit 1; }
import sys
path, old, new = sys.argv[1:]
text = open(path, encoding="utf-8").read()
assert old in text, f"{old!r} not in {path}"
open(path, "w", encoding="utf-8").write(text.replace(old, new))
EOF
}

write() { mkdir -p "$(dirname "$SCEN/$1")"; printf '%s' "$2" >"$SCEN/$1"; }

git_init() {
  (cd "$SCEN" && git init -q -b main && git add . && git -c user.email=t@t -c user.name=t commit -qm init)
}

normalize() {
  perl -pe 'if (/error \[invalid_yaml\]/) { my $h = /put the text in quotes/ ? " +hint" : ""; s/(error \[invalid_yaml\]) .*/$1 <message>$h/ }' "$1"
}

# run_case NAME ARGS...: run both CLIs in copies of $SCEN and compare.
run_case() {
  local name="$1"
  shift
  local d="$WORK/run"
  rm -rf "$d"
  mkdir -p "$d"
  cp -R "$SCEN" "$d/py"
  cp -R "$SCEN" "$d/go"
  (cd "$d/py" && "$PY_BIN" "$@" >"$d/py.out" 2>"$d/py.err"; echo $? >"$d/py.code")
  (cd "$d/go" && "$GO_BIN" "$@" >"$d/go.out" 2>"$d/go.err"; echo $? >"$d/go.code")
  local problems=""
  cmp -s "$d/py.code" "$d/go.code" || problems+=" exit($(cat "$d/py.code") vs $(cat "$d/go.code"))"
  cmp -s "$d/py.out" "$d/go.out" || problems+=" stdout"
  normalize "$d/py.err" >"$d/py.err.n"
  normalize "$d/go.err" >"$d/go.err.n"
  cmp -s "$d/py.err.n" "$d/go.err.n" || problems+=" stderr"
  diff -r -x .git "$d/py" "$d/go" >/dev/null || problems+=" files"
  if [ -z "$problems" ]; then
    PASS=$((PASS + 1))
    [ -n "${VERBOSE:-}" ] && echo "ok    $name"
  else
    FAIL=$((FAIL + 1))
    FAILED+=("$name")
    echo "FAIL  $name:$problems"
    echo "      opensop $*"
    diff "$d/py.out" "$d/go.out" | head -20 | sed 's/^/      /'
    diff "$d/py.err.n" "$d/go.err.n" | head -20 | sed 's/^/      /'
    diff -r -x .git "$d/py" "$d/go" | head -20 | sed 's/^/      /'
  fi
  return 0
}

# --- the fixture and the example, unchanged ---------------------------------------------------

new_scenario
run_case "validate fixture" validate sops
run_case "validate default root" validate
run_case "validate missing folder" validate nope
run_case "render fixture" render sops
run_case "render --out" render sops --out out
run_case "render --out= trailing slash" render sops/ --out=./out/
run_case "render --check (no build)" render sops --check
run_case "render --check against expected" render sops --check --out expected
run_case "plan (no build)" plan sops
run_case "plan --summary" plan sops --summary
run_case "plan --json" plan sops --json
run_case "plan prefix option" plan sops --sum
run_case "agents" agents sops
run_case "agents --json" agents sops --json
run_case "affected (all)" affected sops
run_case "affected --format json" affected sops --format json
run_case "affected --format platform-ids" affected sops --format platform-ids
run_case "affected --agents" affected sops --agents "tonys-pizza,sakura-sushi livekit:tonys-pizza"
run_case "affected unknown agent" affected sops --agents la-casa
run_case "affected --ci (no env)" affected sops --ci
GITHUB_OUTPUT=gh_out GITHUB_STEP_SUMMARY=gh_summary run_case "affected --ci writes outputs" affected sops --all-if-none --ci
run_case "check fixture" check sops
run_case "check --json" check sops --json
run_case "overlap originals" overlap originals
run_case "overlap expected" overlap expected
run_case "overlap empty dir" overlap sops/procedures
run_case "compare originals" compare sops --originals originals
run_case "compare expected" compare sops --originals expected
run_case "guide" guide
run_case "skills install" skills install
run_case "skills install --agent codex" skills install --agent codex
run_case "skills install two agents" skills install --agent claude --agent opencode
run_case "skills install --dir" skills install --dir custom/skills
run_case "example validate" validate example
run_case "example render --check" render example --check
run_case "example render" render example
run_case "example plan" plan example
run_case "example agents --json" agents example --json
run_case "example check" check example
run_case "example affected json" affected example --format json --agents la-casita

# --- argument parsing -------------------------------------------------------------------------

run_case "no args"
run_case "help" -h
run_case "help prefix" --he
for c in validate render plan affected agents overlap compare check skills guide; do
  run_case "$c -h" "$c" -h
done
run_case "bogus command" bogus
run_case "unknown option" render --foo
run_case "unknown top option" --foo validate sops
run_case "extra positional" render sops extra
run_case "invalid --format" affected sops --format xx
run_case "ambiguous option" affected sops --a x
run_case "missing --originals" compare sops
run_case "missing skills action" skills
run_case "bad skills action" skills foo
run_case "bad --agent" skills install --agent foo
run_case "missing option value" render --out
run_case "option value looks like option" affected sops --agents --ci
run_case "store_true with value" render sops --check=yes
run_case "missing overlap dir" overlap
run_case "options before root" render --check sops
run_case "double dash" validate -- sops

# --- validation errors and edge cases ---------------------------------------------------------

new_scenario; edit sops/agents/sakura-sushi.yaml "exclude: [delivery-handling]" "exclude: [delivery-handling, brand-voice]"
run_case "locked" validate sops
run_case "locked (render)" render sops
new_scenario; edit sops/agents/sakura-sushi.yaml "exclude: [delivery-handling]" "exclude: [delivery-handling, closing]"
run_case "unlocked exclude" render sops
new_scenario; edit sops/bases/restaurant-host.md $'---\n---' $'---\ninherits: [pizza-context]\n---'
run_case "inheritance cycle" validate sops
new_scenario; edit sops/agents/tonys-pizza.yaml "inherits: [pizza-context]" "inherits: [pasta-context]"
run_case "unknown base" validate sops
new_scenario; edit sops/procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[sakura-sushi, luigis]"
run_case "unknown agent" validate sops
new_scenario; edit sops/procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" '[sakura-sushi, "livekit:tonys-pizza"]'
run_case "platform ref targeting" render sops
new_scenario; edit sops/agents/luigis-trattoria.yaml $'  menu_allergen_link: luigis.com/menu#allergens\n' ""
run_case "unset variable" validate sops
new_scenario; edit sops/agents/sakura-sushi.yaml "livekit: sakura-sushi" "livekit: tonys-pizza"
run_case "duplicate platform ref" validate sops
new_scenario; edit sops/agents/sakura-sushi.yaml "livekit: sakura-sushi" $'livekit: sakura-sushi\nvapi: asst_123'
run_case "two platforms" validate sops
new_scenario; edit sops/agents/sakura-sushi.yaml "livekit: sakura-sushi" "livekit: ''"
run_case "no platform" validate sops
new_scenario; edit sops/procedures/reservations.yaml "name: Reservations" $'id: bookings\nname: Reservations'
run_case "id mismatch" validate sops
new_scenario; edit sops/procedures/reservations.yaml "name: Reservations" $'id: 5\nname: Reservations'
run_case "id mismatch int" validate sops
new_scenario; edit sops/procedures/reservations.yaml "name: Reservations" $'name: Reservations\nsteps: []'
run_case "unknown field" validate sops
new_scenario; edit sops/procedures/reservations.yaml $'description: The customer has a confirmed table, or knows exactly why one isn\'t available.\n' ""
run_case "missing goal" validate sops
run_case "missing goal (render)" render sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" "  - Never say: we're fully booked"
run_case "colon in step" validate sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" "  - Never say: «complet» — ñ"
run_case "colon in step, non-ASCII" validate sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" $'  - Never say:\n  - yes: no\n  - 2024-01-01: [a, 1.5]'
run_case "colon in step, typed values" validate sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" '  - "Never say: fully booked"'
run_case "quoted colon" render sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" $'  - no\n  -'
run_case "unquoted bool and empty step" validate sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" $'  - 10:30\n  - 017\n  - 0x1F\n  - 1_000\n  - 1.5\n  - .inf\n  - 1.0e+20\n  - On\n  - 1e5\n  - ~'
run_case "YAML 1.1 numbers and booleans" validate sops
new_scenario; edit sops/procedures/reservations.yaml "  - Never double-book a table" $'  - 2024-01-01\n  - [a]\n  - {text: a, bad: 1}\n  - {tool: x}\n  - {text: a, required: maybe, tool: 5}'
run_case "bad step types" validate sops
new_scenario; edit sops/procedures/reservations.yaml "scope: The customer wants" "scope: Note: the customer wants"
run_case "colon in field (hint)" validate sops
new_scenario; edit sops/procedures/reservations.yaml "name: Reservations" $'name: Reservations\n  bad: [indent'
run_case "invalid yaml" validate sops
new_scenario; write sops/procedures/reservations.yaml $'- a\n- b\n'
run_case "yaml not a mapping" validate sops
new_scenario; write sops/opensop.yaml ""
run_case "empty opensop.yaml" render sops
new_scenario; write sops/opensop.yaml $'0\n'
run_case "falsy opensop.yaml" validate sops
new_scenario; rm "$SCEN/sops/opensop.yaml"
run_case "missing opensop.yaml" validate sops
new_scenario; write sops/opensop.yaml $'version: 2\nvariables: {a: 1, 2: b, c: ~}\nsops_heading: ~\nsop_order: x\nextra: 1\n'
run_case "bad config fields" validate sops
new_scenario; write sops/opensop.yaml $'version: yes\nsop_order: [allergen-check, nope]\n'
run_case "unknown sop / bool version" validate sops
new_scenario; write sops/bases/reservations.md $'---\nagents: "*"\n---\nShared text.\n'
run_case "duplicate id" validate sops
new_scenario; edit sops/agents/tonys-pizza.yaml "inherits: [pizza-context]" $'inherits: [pizza-context]\nexclude: [reservations, nope]'
run_case "useless exclude and unknown block" validate sops
new_scenario; write sops/bases/brand-voice.md $'---\nagents: x\nlocked: maybe\nposition: middle\ninherits: a\nexclude: [1]\nyes: 1\n---\nText.\n'
run_case "bad base fields" validate sops
new_scenario; write sops/agents/new.yaml $'vapi: 5\ninherits: [1]\nexclude: x\nvariables: [a]\nzz: 1\n'
run_case "bad agent fields" validate sops
new_scenario; write sops/agents/new.yaml $'elevenlabs: el_1\nvariables: {restaurant_name: "Café ñ \\u00e9 😀 \\"q\\"", menu_allergen_link: x}\nlocked: true\n'
run_case "agent extra field" validate sops
new_scenario; write sops/agents/new.yaml $'elevenlabs: el_1\nvariables:\n  restaurant_name: "Café \\"Ñ\\" \\\\ 😀\\ttab"\n  menu_allergen_link: "x\\ny"\n'
run_case "unicode variables" render sops
run_case "unicode variables agents" agents sops
run_case "unicode variables json" agents sops --json
new_scenario; edit sops/procedures/reservations.yaml "name: Reservations" $'name: Reservations\ndelivery: tool'
edit sops/agents/sakura-sushi.yaml "staff_transfer: the head chef" 'staff_transfer: "le chef «principal» \"Ñ\""'
run_case "tool delivery with unicode" render sops
new_scenario; write sops/procedures/merge.yaml $'defaults: &d\n  agents: "*"\n  locked: true\nname: Merged\n<<: *d\ndescription: Uses a merge key and an anchor.\nprocedureSteps:\n  - &s Say hello to the caller\n  - *s\n'
run_case "merge keys and anchors" render sops
new_scenario; write sops/procedures/dup.yaml $'name: First\nname: Second\nagents: "*"\ndescription: Duplicate keys keep the last value.\n'
run_case "duplicate keys" render sops
new_scenario; write sops/bases/odd.md $'---\n---\n---\nagents: "*"\n---\n  Body with leading dashes.  \n'
run_case "odd front matter" render sops
new_scenario; write sops/bases/crlf.md $'---\r\nagents: "*"\r\n---\r\nWindows line endings.\r\n'
run_case "CRLF base" render sops
new_scenario; write sops/procedures/vars.yaml $'name: Vars\nagents: "*"\ndescription: "Uses {{ spaced_var }} and {{unset.one}}"\n'
run_case "variables with spaces / unset" validate sops
new_scenario; write sops/procedures/order.yaml $'name: Order\nagents: "*"\ndescription: Listed twice in sop_order.\n'
write sops/opensop.yaml $'variables:\n  staff_transfer: the manager on duty\nsop_order: [order, allergen-check, order]\nsops_heading: "# Procedimientos"\n'
run_case "sop_order duplicates" render sops

# --- check, overlap and compare scenarios (from the Python tests) ------------------------------

new_scenario
edit sops/agents/tonys-pizza.yaml "Pickup only after 10pm. Cash and card." "Pickup only after 11pm. Cash and card. Speak warmly and briefly. Always confirm the delivery address."
edit sops/agents/tonys-pizza.yaml "  menu_allergen_link: tonys.com/allergens" $'  menu_allergen_link: tonys.com/allergens\n  old_phone: 555-0100'
python3 - "$SCEN/sops/bases/pizza-context.md" <<'EOF'
import sys; p = sys.argv[1]; t = open(p).read().rstrip() + " Pickup only after 10pm.\n"; open(p, "w").write(t)
EOF
edit sops/procedures/delivery-handling.yaml "procedureSteps:" $'forbiddenActions:\n  - Never confirm the delivery address\nprocedureSteps:'
run_case "check conflicts" check sops
run_case "check conflicts --json" check sops --json
new_scenario
python3 - "$SCEN/sops/bases/closing.md" <<'EOF'
import sys; p = sys.argv[1]; t = open(p).read().rstrip() + " Before hanging up, never repeat the order total and the pickup or delivery time.\n"; open(p, "w").write(t)
EOF
run_case "check shared conflict" check sops
new_scenario; edit sops/agents/tonys-pizza.yaml "Pickup only after 10pm. Cash and card." "Pickup and delivery until 11pm. Delivery until 10pm. Cash and card."
run_case "check number in longer sentence" check sops
new_scenario
printf '\nGift cards can be bought at the counter on weekends.\n' >>"$SCEN/originals/sakura-sushi.md"
edit originals/sakura-sushi.md "Ask one question at a time." "Ask up to two questions at a time."
printf 'A brand new prompt. It has three sentences here! Does it? Yes it does.\n' >"$SCEN/originals/extra-agent.txt"
run_case "compare changed/missing/reworded" compare sops --originals originals
run_case "overlap with txt" overlap originals

# --- git: plan and affected against a ref ------------------------------------------------------

new_scenario; git_init
edit sops/procedures/allergen-check.yaml "Name the specific allergen" "Repeat the specific allergen"
edit sops/bases/brand-voice.md "briefly" "concisely"
run_case "plan --against main" plan sops --against main
run_case "plan --against main --summary" plan sops --against main --summary
run_case "plan --against main --json" plan sops --against main --json
run_case "affected --against main" affected sops --against main
GITHUB_OUTPUT=gh_out GITHUB_STEP_SUMMARY=gh_summary run_case "affected --against --ci" affected sops --against main --all-if-none --ci
run_case "plan --against bad ref" plan sops --against nope
run_case "affected nothing changed" affected example --against main
run_case "affected nothing changed --all-if-none" affected example --against main --all-if-none
run_case "plan from repo root" plan sops --against HEAD

new_scenario
(cd "$SCEN" && git init -q -b main && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m empty)
run_case "plan against ref without files" plan sops --against main
run_case "affected against ref without files" affected sops --against main

new_scenario; git_init
rm "$SCEN/sops/agents/sakura-sushi.yaml"
cp "$SCEN/sops/agents/luigis-trattoria.yaml" "$SCEN/sops/agents/luigis-brooklyn.yaml"
edit sops/agents/luigis-brooklyn.yaml "livekit: luigis-trattoria" "livekit: luigis-brooklyn"
edit sops/procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[luigis-trattoria]"
edit sops/opensop.yaml "the manager on duty" "the shift lead"
run_case "plan added/removed agents + workspace" plan sops --against main
run_case "affected added agent json" affected sops --against main --format json

new_scenario; git_init
edit sops/procedures/reservations.yaml "[sakura-sushi, luigis-trattoria]" "[sakura-sushi]"
(cd "$SCEN" && git -c user.email=t@t -c user.name=t commit -qam targeting)
run_case "plan targeting change HEAD~1" plan sops --against HEAD~1
(cd "$SCEN/sops" && "$PY_BIN" render . >/dev/null)
edit sops/agents/tonys-pizza.yaml "Cash and card." "Cash only."
run_case "plan vs committed build" plan sops
run_case "render --check stale build" render sops --check

echo
echo "parity: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
  printf '  - %s\n' "${FAILED[@]}"
  exit 1
fi
