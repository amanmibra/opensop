# sopc command reference

Every command and flag. `sopc --help` (or `sopc <command> --help`) prints the same information with examples; [FORMAT.md](FORMAT.md) describes the files sopc reads.

- [Install and uninstall](#install-and-uninstall)
- [How every command works](#how-every-command-works)
- Commands: [`sopc`](#sopc) · [`validate`](#sopc-validate) · [`lint`](#sopc-lint) · [`fmt`](#sopc-fmt) · [`plan`](#sopc-plan) · [`affected`](#sopc-affected) · [`agents`](#sopc-agents) · [`convert`](#sopc-convert) · [`overlap`](#sopc-overlap) · [`compare`](#sopc-compare) · [`skills`](#sopc-skills) · [`guide`](#sopc-guide)

## Install and uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/amanmibra/sopc/main/install.sh | sh
```

Installs one `sopc` binary into `~/.local/bin` (macOS, Linux and Windows; amd64 and arm64) and verifies its checksum.

| Variable | Effect |
|---|---|
| `SOPC_REF=v0.0.7` | Install a specific release (default: the latest) |
| `SOPC_INSTALL_DIR=DIR` | Install somewhere other than `~/.local/bin` |

From source, with Rust: `cargo install --git https://github.com/amanmibra/sopc`.

To uninstall, delete the binary: `rm ~/.local/bin/sopc`. If you installed the import skill, also delete `.claude/skills/sopc-import` and `.agents/skills/sopc-import` from your repo.

## How every command works

**The folder.** Every command works on the sopc folder, the one containing `sopc.yaml`: `./sops` if `sops/sopc.yaml` exists, otherwise the current folder if `sopc.yaml` is there. Use `--dir DIR` (short: `-C DIR`) for another folder. It works before or after the command: `sopc --dir path/to/sops validate` and `sopc validate --dir path/to/sops` are the same.

**Output.** Results go to stdout; errors, warnings and progress notes go to stderr, so `--json` and id lists can be piped safely. File paths in messages are relative to the folder you ran sopc from (`sops/procedures/x.md: error [...] line 11: ...`). Color is turned off when output is piped, when `NO_COLOR` is set, or when `TERM=dumb`.

**Exit codes.**

| Code | Meaning |
|---|---|
| `0` | Success. `sopc lint` exits 0 even when it finds something, because its findings are advisory. |
| `1` | Failure: invalid files, a build that's out of date (`--check`), a failed `compare`, files left unformatted to keep comments, or a git or file error. |
| `2` | Usage error: an unknown command or flag, a typo (sopc suggests the closest command), or a compile-only flag used with a command. |

A closed pipe (`sopc guide | head`) exits 0.

**Comparing with git.** `plan` and `affected` compare the current files with a git ref and print `Comparing with <ref> (<short sha>)` to stderr. Without `--against`, the ref is your default branch: `origin/HEAD`, then `origin/main`, `main`, `origin/master`, `master`. Git problems are reported in plain words, e.g. ``unknown git ref `origin/release`; check the name, or fetch it (`git fetch origin release`)``.

**Nothing destructive without `--yes`.** Commands that rewrite your files (`fmt`, `convert`) never remove comments silently. A file that would lose comments is left as it is and listed with those comments, and the command exits 1. Rerun with `-y`/`--yes` to allow it.

## sopc

Compiles every agent's prompt into `build/` inside the sopc folder: one `<agent>.prompt.md` per agent, a `<agent>.tool.json` for agents with tool-delivered SOPs, and `lock.json` (each prompt's hash and the hashes of the blocks it was built from).

```sh
sopc                         # compile ./sops into sops/build
sopc --check                 # exit 1 if sops/build is out of date (for CI)
sopc -C path/to/sops -o out  # compile another folder into out/
```

| Flag | Effect |
|---|---|
| `-o`, `--out DIR` | Write the build somewhere else (default: `DIR/build`) |
| `--check` | Write nothing; exit 1 and show what changed if the build is out of date |
| `--force` | Write into an `--out` folder that has other files but no `lock.json`. Only the files sopc builds are overwritten; nothing is removed |

Builds are written safely: files go to a temporary folder and are renamed into place, and only files the previous build created (as listed in its `lock.json`) are ever removed. Other files in the output folder are left alone. Without `--force`, sopc refuses to write into a non-empty folder it didn't build.

`-o` and `--check` apply only to compiling; `sopc --check validate` is a usage error.

## sopc validate

Finds errors in the files and prints every problem with its file, line (for Markdown SOPs) and a fix. Exits 1 if there are errors; warnings alone exit 0.

```sh
sopc validate
sopc validate --json
```

| Flag | Effect |
|---|---|
| `--json` | Prints `{"valid": bool, "issues": [{"code", "message", "path", "severity"}]}` to stdout instead, including files that fail to parse. Paths are relative to the sopc folder. |

The error codes are listed in [FORMAT.md](FORMAT.md#validation).

## sopc lint

Finds duplicated text and conflicting instructions in each agent's compiled prompt: the same sentence twice, the same sentence with different numbers (`10pm` vs `11pm`), "always X" vs "never X", near-identical sentences that have drifted, and variables an agent sets but never uses. The findings are advisory: lint exits 0.

```sh
sopc lint
sopc lint --json
```

| Flag | Effect |
|---|---|
| `--json` | Machine-readable output |

## sopc fmt

Rewrites Markdown SOP files in one canonical style: sections in the standard order (Steps, Never, Warning signs), numbered steps, `-` bullets, one blank line between blocks, and tool markers as `` `tool: name` `` then `` `required` ``. Front matter is kept as written. It never changes a compiled prompt.

```sh
sopc fmt
sopc fmt --check   # for CI
sopc fmt --yaml    # YAML SOPs too
```

| Flag | Effect |
|---|---|
| `--check` | Write nothing; list files that would change and exit 1 if there are any |
| `--yaml` | Also rewrite YAML SOP files: keys in a fixed order, default settings left out, multi-line text as `\|` blocks. YAML comments below the top of the file can't be kept, so those files need `--yes` |
| `-y`, `--yes` | Allow rewrites that remove comments |

A file that would lose comments is left as it is and listed:

```
sops/procedures/takeout.yaml: not formatted: it would remove 2 comment(s); edit it by hand, or rerun with --yes to remove them
  line 2: # the heading agents see
  line 6: # ask first
```

## sopc plan

Shows which agents a change affects and because of which block, with a diff of each affected prompt. Compares with your default branch unless you pass `--against`.

```sh
sopc plan
sopc plan --summary
sopc plan --against v0.3.0
```

```console
$ sopc plan --summary
Comparing with origin/main (3f9a2c1)
3 agents change:
  base `brand-voice` edited → 3 agents: luigis-trattoria, sakura-sushi, tonys-pizza
```

| Flag | Effect |
|---|---|
| `--against REF` | Compare with this git branch, tag or commit |
| `--summary` | Leave out the diffs |
| `--json` | Machine-readable output |

If no default branch can be found, `plan` stops and asks for `--against`.

## sopc affected

Lists the agents to test for a change: those whose compiled prompt differs from the default branch (or `--against`). Made for CI: run your tests for just these agents. See the [behavior gate example](examples/behavior-gate).

```sh
sopc affected
sopc affected --format platform-ids
sopc affected --ci --all-if-none
```

| Flag | Effect |
|---|---|
| `--against REF` | Compare with this git branch, tag or commit |
| `--agents "IDS"` | Select these agents instead (sopc ids or platform ids, separated by spaces or commas). `--against` is ignored |
| `--all-if-none` | Select every agent when nothing else is selected |
| `--format ids\|platform-ids\|json` | `ids`: sopc ids, one per line (default). `platform-ids`: each agent's own id on its platform. `json`: everything, including which SOPs changed for each agent |
| `--ci` | Also write GitHub Actions outputs (`ids`, `platform_ids`, `matrix`, `count`, `all`) and a step summary |

If no default branch can be found, `affected` selects every agent and prints a warning, unless `--agents` or `--all-if-none` was given.

## sopc agents

Lists every agent: its id, platform, platform id and SOPs.

```sh
sopc agents
sopc agents --json   # adds bases, tools and prompt hashes
```

| Flag | Effect |
|---|---|
| `--json` | Machine-readable output |

## sopc convert

Rewrites SOPs as Markdown or YAML. Converting never changes a compiled prompt; a file that can't be converted exactly is refused (`convert_failed`), e.g. a YAML goal with line breaks, which a Markdown field can't keep.

```sh
sopc convert --to md                # every YAML SOP
sopc convert --to yaml reservations # one SOP
```

| Argument or flag | Effect |
|---|---|
| `--to md\|yaml` | The format to write (required) |
| `IDS` | SOP ids to convert (default: every SOP not already in that format) |
| `-y`, `--yes` | Allow conversions that remove comments |

The new file is written before the old one is deleted. Comments at the top of a YAML file become front-matter comments, and back. Converting can change an SOP's hash in `lock.json` when only whitespace differs.

## sopc overlap

For importing existing prompts: shows the text that several prompts share and near-copies that differ only by a value (placeholder candidates, or drift).

```sh
sopc overlap prompts/
```

`PROMPTS` is a folder with one existing prompt per agent, named `<agent-id>.md` or `.txt`.

## sopc compare

For importing existing prompts: checks that each compiled prompt still says everything its original did. Exits 1 if a sentence was lost or changed, and shows the changed words. Sentences that were reworded (for example, one sentence split into steps) are listed but don't fail.

```sh
sopc compare --originals prompts/
```

| Flag | Effect |
|---|---|
| `--originals DIR` | Folder with the original prompts, `<agent-id>.md` or `.txt` (required) |

## sopc skills

Installs the `sopc-import` skill, which converts existing prompts into sopc files from inside your coding agent.

```sh
sopc skills install                  # Claude Code, Codex and OpenCode
sopc skills install --agent claude   # one agent
```

| Argument or flag | Effect |
|---|---|
| `install` | The only action |
| `--agent claude\|codex\|opencode` | Install for one agent; repeatable. Claude Code reads `.claude/skills`, Codex `.agents/skills`, OpenCode both |
| `--into FOLDER` | Install into another folder |

## sopc guide

Prints the format reference ([FORMAT.md](FORMAT.md)), so it's available offline and matches the installed version.

```sh
sopc guide | less
```
