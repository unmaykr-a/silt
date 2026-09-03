// Command silt-demo writes a populated Silt database, for development and for
// the end-to-end suite.
//
// Not shipped: the image builds ./cmd/silt only.
//
//	go run ./cmd/silt-demo /tmp/demo/silt.db
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/unmaykr-a/silt/internal/demo"
	"github.com/unmaykr-a/silt/internal/store"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: silt-demo <path to silt.db>")
		os.Exit(2)
	}
	ctx := context.Background()

	db, err := store.Open(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	if err := demo.Seed(ctx, db); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
	fmt.Printf("seeded %d projects into %s\n", len(demo.Stacks()), os.Args[1])
}
