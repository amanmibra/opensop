package pyx

import (
	"os"
	"strings"
)

// Path is str(pathlib.PurePosixPath(p)): collapses repeated slashes and "." parts and drops a
// trailing slash, but (unlike filepath.Clean) leaves ".." alone.
func Path(p string) string {
	if p == "" {
		return "."
	}
	lead := ""
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		lead = "//"
	} else if strings.HasPrefix(p, "/") {
		lead = "/"
	}
	var parts []string
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." {
			continue
		}
		parts = append(parts, part)
	}
	out := lead + strings.Join(parts, "/")
	if out == "" {
		return "."
	}
	return out
}

// Join is str(Path(a) / b).
func Join(a, b string) string {
	a = Path(a)
	if strings.HasPrefix(b, "/") {
		return Path(b)
	}
	if a == "." {
		return Path(b)
	}
	if strings.HasSuffix(a, "/") {
		return Path(a + b)
	}
	return Path(a + "/" + b)
}

// ReadText is Path.read_text(): the file's contents with universal newlines.
func ReadText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return UniversalNewlines(string(data)), nil
}
