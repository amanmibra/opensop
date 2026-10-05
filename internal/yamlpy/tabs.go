package yamlpy

import (
	"fmt"
	"strings"
)

// checkTabs reports the first tab PyYAML would reject. PyYAML's scanner only skips spaces
// between tokens, so a tab anywhere outside a comment, a quoted scalar or a block scalar's
// content is "found character '\t' that cannot start any token". yaml.v3 (like libyaml)
// accepts many of these, so they are checked here with a small lexer.
func checkTabs(src string) error {
	if !strings.Contains(src, "\t") {
		return nil
	}
	lines := strings.Split(src, "\n")
	const (
		normal = iota
		single
		double
	)
	state := normal
	blockIndent := -1 // >= 0 while inside a block scalar's content
	for ln, raw := range lines {
		line := []rune(strings.TrimSuffix(raw, "\r"))
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		if blockIndent >= 0 && state == normal {
			blank := strings.Trim(string(line), " \t") == ""
			if blank || indent > blockIndent {
				continue
			}
			blockIndent = -1
		}
		lastToken := rune(0) // previous non-space character on this line (0: none yet)
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch state {
			case single:
				if c == '\'' {
					if i+1 < len(line) && line[i+1] == '\'' {
						i++
					} else {
						state = normal
						lastToken = c
					}
				}
				continue
			case double:
				if c == '\\' {
					i++
				} else if c == '"' {
					state = normal
					lastToken = c
				}
				continue
			}
			atTokenStart := lastToken == 0 || strings.ContainsRune("-:[{,?", lastToken)
			prevSpace := i == 0 || line[i-1] == ' ' || line[i-1] == '\t'
			switch {
			case c == '\t':
				return &Error{fmt.Sprintf("while scanning for the next token found character '\\t' that cannot start any token (line %d, column %d)", ln+1, i+1)}
			case c == '#' && prevSpace:
				i = len(line) // comment
			case (c == '\'' || c == '"') && prevSpace && atTokenStart:
				if c == '\'' {
					state = single
				} else {
					state = double
				}
			case (c == '|' || c == '>') && prevSpace && atTokenStart:
				rest := strings.TrimLeft(string(line[i+1:]), "0123456789+-")
				if t := strings.TrimLeft(rest, " \t"); t == "" || strings.HasPrefix(t, "#") {
					blockIndent = indent
					i = len(line)
				} else {
					lastToken = c
				}
			case c != ' ':
				lastToken = c
			}
		}
	}
	return nil
}
