package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/fitglue/server/src/go/pkg/docstore"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

// fitgluectl snapshot --out PATH: a consistent copy of the box's SQLite file
// (documents + queue) for the nightly restic backup to pick up
// (fitglue-snapshot.timer). The live file is never backed up directly: a
// copy taken mid-write would not open.
func snapshotCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dbPath := fl.String("db", localqueue.DefaultPath(), "SQLite file to snapshot")
	out := fl.String("out", "", "where to write the copy (replaced atomically)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("snapshot: --out is required")
	}
	c, err := docstore.Open(*dbPath)
	if err != nil {
		return err
	}
	defer c.Close()
	n, err := c.Snapshot(ctx, *out)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "snapshot: %d bytes\n", n)
	return nil
}
