// Command gen writes release text from the history in the changelog package.
//
//	gen CHANGELOG.md      the whole file, as `make changelog` does
//	gen --notes v0.14.0   one release, as the body of a GitHub release
//	gen --version         the newest version, for `make release` to tag
//
// The second exists so a published release and the changelog cannot disagree:
// they are the same data, rendered twice.
package main

import (
	"fmt"
	"os"

	"github.com/unmaykr-a/silt/internal/changelog"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(changelog.Current())
		return
	}
	if len(args) == 2 && args[0] == "--notes" {
		notes, ok := changelog.Notes(args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "gen: no release %s in the changelog\n", args[1])
			os.Exit(1)
		}
		fmt.Print(notes)
		return
	}

	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: gen <path to CHANGELOG.md> | gen --notes <version> | gen --version")
		os.Exit(2)
	}
	if err := os.WriteFile(args[0], []byte(changelog.Markdown()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
}
