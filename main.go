// Command nuon-ext-override-lint is a Nuon CLI extension that lints
// install-level Helm values overrides, catching overrides that change the
// values map but have no effect on the rendered manifest.
package main

import (
	"fmt"
	"os"

	"github.com/nuonco/nuon-ext-override-lint/cmd"
)

func main() {
	root := cmd.NewRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
