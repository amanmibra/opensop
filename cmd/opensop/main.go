// Command opensop builds and checks OpenSOP folders: modular, git-versioned instructions
// for teams managing multiple task-driven agents.
package main

import (
	"os"

	"github.com/amanmibra/opensop/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
