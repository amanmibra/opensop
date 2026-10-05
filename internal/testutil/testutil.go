// Package testutil has helpers shared by the Go tests (the equivalents of the Python
// tests' fixtures).
package testutil

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// RepoRoot is the repository root.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Fixture is tests/fixtures/restaurants.
func Fixture() string { return filepath.Join(RepoRoot(), "tests", "fixtures", "restaurants") }

// Example is examples/livekit-restaurant/sops.
func Example() string { return filepath.Join(RepoRoot(), "examples", "livekit-restaurant", "sops") }

// CopyDir copies a folder tree.
func CopyDir(t testing.TB, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Repo is a writable copy of the fixture's sops/ folder.
func Repo(t testing.TB) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "sops")
	CopyDir(t, filepath.Join(Fixture(), "sops"), dst)
	return dst
}

// Edit replaces old with new in a file; old must be present.
func Edit(t testing.TB, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%q not in %s", old, filepath.Base(path))
	}
	Write(t, path, strings.ReplaceAll(string(data), old, new))
}

// Read reads a file.
func Read(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Write writes a file.
func Write(t testing.TB, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Git runs git in dir.
func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// GitRepo makes a repo at <tmp>/agent-repo with the fixture committed at sops/ on main.
func GitRepo(t testing.TB) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "agent-repo")
	CopyDir(t, filepath.Join(Fixture(), "sops"), filepath.Join(root, "sops"))
	Git(t, root, "init", "-q", "-b", "main")
	Git(t, root, "add", ".")
	Git(t, root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init")
	return root
}

// Chdir changes directory for the rest of the test.
func Chdir(t testing.TB, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}
