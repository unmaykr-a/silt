// Command gen writes CHANGELOG.md from the release history in the changelog
// package. Run it with `make changelog`.
package main

import (
	"fmt"
	"os"

	"github.com/unmaykr-a/silt/internal/changelog"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <path to CHANGELOG.md>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], []byte(changelog.Markdown()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
}
