// Code generated from Python argparse output (COLUMNS=80). DO NOT EDIT.

package cli

const topHelp = "usage: opensop [-h]\n               {validate,render,plan,affected,agents,overlap,compare,check,skills,guide}\n               ...\n\nModular, git-versioned instructions for teams managing multiple task-driven\nagents.\n\npositional arguments:\n  {validate,render,plan,affected,agents,overlap,compare,check,skills,guide}\n    validate            check the files and print problems\n    render              write one full prompt per agent into build/\n    plan                show which agents change and why\n    affected            which agents a change affects (for tests and CI)\n    agents              list agents with their platform ids, SOPs and tools\n    overlap             show text shared across existing prompts (for\n                        importing)\n    compare             check rendered prompts still contain everything the\n                        originals said\n    check               find duplicated text and mechanical conflicts in each\n                        agent's prompt\n    skills              install the opensop skills for coding agents\n    guide               print the format reference (FORMAT.md)\n\noptions:\n  -h, --help            show this help message and exit\n"
const topUsage = "usage: opensop [-h]\n               {validate,render,plan,affected,agents,overlap,compare,check,skills,guide}\n               ..."

var helpTexts = map[string]string{
	"validate": "usage: opensop validate [-h] [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help  show this help message and exit\n",
	"render":   "usage: opensop render [-h] [--out OUT] [--check] [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help  show this help message and exit\n  --out OUT   output folder (default: ROOT/build)\n  --check     fail if build/ is out of date instead of writing it\n",
	"plan":     "usage: opensop plan [-h] [--against REF] [--summary] [--json] [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help     show this help message and exit\n  --against REF  git ref to compare with (default: the committed build/\n                 folder)\n  --summary      omit the diffs\n  --json         machine-readable output (for CI)\n",
	"affected": "usage: opensop affected [-h] [--against REF] [--agents AGENTS] [--all-if-none]\n                        [--format {ids,platform-ids,json}] [--ci]\n                        [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help            show this help message and exit\n  --against REF         git ref to compare with; agents whose prompt changed\n                        are selected\n  --agents AGENTS       select these agents instead (OpenSOP ids or platform\n                        ids, space or comma separated)\n  --all-if-none         select every agent when nothing else is selected\n  --format {ids,platform-ids,json}\n                        ids: OpenSOP ids (file names); platform-ids: the\n                        platform's own ids; json: everything\n  --ci                  also write GitHub Actions outputs and a step summary\n",
	"agents":   "usage: opensop agents [-h] [--json] [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help  show this help message and exit\n  --json\n",
	"overlap":  "usage: opensop overlap [-h] dir\n\npositional arguments:\n  dir         folder with one existing prompt per agent, named <agent-id>.md\n              or .txt\n\noptions:\n  -h, --help  show this help message and exit\n",
	"compare":  "usage: opensop compare [-h] --originals ORIGINALS [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help            show this help message and exit\n  --originals ORIGINALS\n                        folder with <agent-id>.md or .txt originals\n",
	"check":    "usage: opensop check [-h] [--json] [root]\n\npositional arguments:\n  root\n\noptions:\n  -h, --help  show this help message and exit\n  --json\n",
	"skills":   "usage: opensop skills [-h] [--agent {claude,codex,opencode}] [--dir DIR]\n                      {install}\n\npositional arguments:\n  {install}\n\noptions:\n  -h, --help            show this help message and exit\n  --agent {claude,codex,opencode}\n                        install for this coding agent only (repeatable).\n                        Default: Claude Code, Codex and OpenCode\n  --dir DIR             install into this folder instead\n",
	"guide":    "usage: opensop guide [-h]\n\noptions:\n  -h, --help  show this help message and exit\n",
}

var usageTexts = map[string]string{
	"validate": "usage: opensop validate [-h] [root]",
	"render":   "usage: opensop render [-h] [--out OUT] [--check] [root]",
	"plan":     "usage: opensop plan [-h] [--against REF] [--summary] [--json] [root]",
	"affected": "usage: opensop affected [-h] [--against REF] [--agents AGENTS] [--all-if-none]\n                        [--format {ids,platform-ids,json}] [--ci]\n                        [root]",
	"agents":   "usage: opensop agents [-h] [--json] [root]",
	"overlap":  "usage: opensop overlap [-h] dir",
	"compare":  "usage: opensop compare [-h] --originals ORIGINALS [root]",
	"check":    "usage: opensop check [-h] [--json] [root]",
	"skills":   "usage: opensop skills [-h] [--agent {claude,codex,opencode}] [--dir DIR]\n                      {install}",
	"guide":    "usage: opensop guide [-h]",
}
