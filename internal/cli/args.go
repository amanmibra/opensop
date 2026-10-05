package cli

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// A small argparse look-alike: the same option syntax (--opt VALUE, --opt=VALUE, unique
// prefixes like --sum), the same error messages and exit code 2, and help text copied from
// Python's argparse output at the default 80-column width.

type optSpec struct {
	flag     string // "--out"
	dest     string
	hasValue bool
	choices  []string
	repeat   bool // action="append"
	required bool
}

type posSpec struct {
	name     string
	optional bool // nargs="?"
	def      string
	choices  []string
}

type command struct {
	name  string
	usage string // "usage: ..." line(s), without trailing newline
	help  string // full -h output
	opts  []optSpec
	pos   []posSpec
}

type parsed struct {
	values map[string]string
	lists  map[string][]string
	flags  map[string]bool
}

func (p *parsed) str(k string) string    { return p.values[k] }
func (p *parsed) flag(k string) bool     { return p.flags[k] }
func (p *parsed) list(k string) []string { return p.lists[k] }

// exitError ends the program with a code after printing to the given stream.
type exitError struct {
	code   int
	stdout string
	stderr string
}

func (e *exitError) Error() string { return e.stderr }

func usageError(usage, prog, msg string) *exitError {
	return &exitError{code: 2, stderr: usage + "\n" + prog + ": error: " + msg + "\n"}
}

func quoteChoices(choices []string) string {
	q := make([]string, len(choices))
	for i, c := range choices {
		q[i] = "'" + c + "'"
	}
	return strings.Join(q, ", ")
}

func checkChoice(name, value string, choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	for _, c := range choices {
		if c == value {
			return ""
		}
	}
	return fmt.Sprintf("argument %s: invalid choice: '%s' (choose from %s)", name, value, quoteChoices(choices))
}

var negativeNumber = regexp.MustCompile(`^-\d+$|^-\d*\.\d+$`)

func looksLikeNegativeNumber(s string) bool { return negativeNumber.MatchString(s) }

// classify mirrors argparse's _parse_optional: returns (option spec index or -1 for help,
// explicit value, isOption, error message).
func classify(arg string, opts []optSpec) (idx int, explicit *string, isOpt bool, errMsg string) {
	if !strings.HasPrefix(arg, "-") || len(arg) == 1 {
		return 0, nil, false, ""
	}
	all := append([]string{"-h", "--help"}, flagsOf(opts)...)
	find := func(flag string) int {
		if flag == "-h" || flag == "--help" {
			return -1
		}
		for i, o := range opts {
			if o.flag == flag {
				return i
			}
		}
		return -2
	}
	if i := find(arg); i != -2 {
		return i, nil, true, ""
	}
	name, value, hasEq := strings.Cut(arg, "=")
	if hasEq {
		if i := find(name); i != -2 {
			return i, &value, true, ""
		}
	}
	if strings.HasPrefix(arg, "--") {
		var matches []string
		for _, f := range all {
			if strings.HasPrefix(f, "--") && strings.HasPrefix(f, name) {
				matches = append(matches, f)
			}
		}
		if len(matches) > 1 {
			return 0, nil, true, "ambiguous option: " + arg + " could match " + strings.Join(matches, ", ")
		}
		if len(matches) == 1 {
			if hasEq {
				return find(matches[0]), &value, true, ""
			}
			return find(matches[0]), nil, true, ""
		}
	}
	if looksLikeNegativeNumber(arg) || strings.Contains(arg, " ") {
		return 0, nil, false, ""
	}
	return -2, nil, true, "" // unknown option
}

func flagsOf(opts []optSpec) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.flag
	}
	return out
}

func isOptionLike(arg string, opts []optSpec) bool {
	_, _, isOpt, _ := classify(arg, opts)
	return isOpt
}

// parseCommand parses a subcommand's arguments. Unrecognized arguments are returned as extras.
func parseCommand(c *command, args []string) (*parsed, []string, *exitError) {
	prog := "opensop " + c.name
	p := &parsed{values: map[string]string{}, lists: map[string][]string{}, flags: map[string]bool{}}
	var extras, positionals []string
	seen := map[string]bool{}
	dashdash := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !dashdash && arg == "--" {
			dashdash = true
			continue
		}
		if dashdash {
			positionals = append(positionals, arg)
			continue
		}
		idx, explicit, isOpt, errMsg := classify(arg, c.opts)
		if errMsg != "" {
			return nil, nil, usageError(c.usage, prog, errMsg)
		}
		if !isOpt {
			positionals = append(positionals, arg)
			continue
		}
		if idx == -1 {
			return nil, nil, &exitError{code: 0, stdout: c.help}
		}
		if idx == -2 {
			extras = append(extras, arg)
			continue
		}
		o := c.opts[idx]
		seen[o.dest] = true
		if !o.hasValue {
			if explicit != nil {
				return nil, nil, usageError(c.usage, prog, fmt.Sprintf("argument %s: ignored explicit argument '%s'", o.flag, *explicit))
			}
			p.flags[o.dest] = true
			continue
		}
		var value string
		if explicit != nil {
			value = *explicit
		} else {
			if i+1 >= len(args) || isOptionLike(args[i+1], c.opts) {
				return nil, nil, usageError(c.usage, prog, "argument "+o.flag+": expected one argument")
			}
			i++
			value = args[i]
		}
		if msg := checkChoice(o.flag, value, o.choices); msg != "" {
			return nil, nil, usageError(c.usage, prog, msg)
		}
		if o.repeat {
			p.lists[o.dest] = append(p.lists[o.dest], value)
		} else {
			p.values[o.dest] = value
		}
	}
	for i, ps := range c.pos {
		if i < len(positionals) {
			if msg := checkChoice(ps.name, positionals[i], ps.choices); msg != "" {
				return nil, nil, usageError(c.usage, prog, msg)
			}
			p.values[ps.name] = positionals[i]
			seen[ps.name] = true
		} else if ps.optional {
			p.values[ps.name] = ps.def
		}
	}
	if len(positionals) > len(c.pos) {
		extras = append(extras, positionals[len(c.pos):]...)
	}
	var missing []string
	for _, o := range c.opts {
		if o.required && !seen[o.dest] {
			missing = append(missing, o.flag)
		}
	}
	for _, ps := range c.pos {
		if !ps.optional && !seen[ps.name] {
			missing = append(missing, ps.name)
		}
	}
	if len(missing) > 0 {
		return nil, nil, usageError(c.usage, prog, "the following arguments are required: "+strings.Join(missing, ", "))
	}
	return p, extras, nil
}

// parseArgs parses the whole command line.
func parseArgs(argv []string) (*command, *parsed, *exitError) {
	var extras []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		idx, _, isOpt, errMsg := classify(arg, nil)
		if errMsg != "" {
			return nil, nil, usageError(topUsage, "opensop", errMsg)
		}
		if isOpt {
			if idx == -1 {
				return nil, nil, &exitError{code: 0, stdout: topHelp}
			}
			extras = append(extras, arg)
			continue
		}
		var cmd *command
		for _, c := range commands {
			if c.name == arg {
				cmd = c
			}
		}
		if cmd == nil {
			names := make([]string, len(commands))
			for i, c := range commands {
				names[i] = c.name
			}
			return nil, nil, usageError(topUsage, "opensop", fmt.Sprintf("argument command: invalid choice: '%s' (choose from %s)", arg, quoteChoices(names)))
		}
		p, subExtras, err := parseCommand(cmd, argv[i+1:])
		if err != nil {
			return nil, nil, err
		}
		extras = append(extras, subExtras...)
		if len(extras) > 0 {
			return nil, nil, usageError(topUsage, "opensop", "unrecognized arguments: "+strings.Join(extras, " "))
		}
		return cmd, p, nil
	}
	return nil, nil, usageError(topUsage, "opensop", "the following arguments are required: command")
}

func writeExit(e *exitError, stdout, stderr io.Writer) int {
	io.WriteString(stdout, e.stdout)
	io.WriteString(stderr, e.stderr)
	return e.code
}
