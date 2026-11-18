package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/fitglue/server/src/go/internal/infra"
	"github.com/fitglue/server/src/go/pkg/infrastructure/localqueue"
)

// `fitgluectl queue` looks at the on-box message queue (the SQLite table that
// replaced Cloud Pub/Sub): counts per topic, the dead letters, and a retry for
// one dead message. Read-only except `retry`.
//
//	fitgluectl queue [--json]
//	fitgluectl queue --dead [--limit N] [--json]
//	fitgluectl queue retry <id>
func queueCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	q, err := localqueue.Open(localqueue.DefaultPath(), infra.NewStderrLoggerWithComponent("fitgluectl"))
	if err != nil {
		return err
	}
	defer q.Close()
	return runQueueCommand(ctx, q, args, stdout, stderr)
}

func runQueueCommand(ctx context.Context, q *localqueue.Queue, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "retry" {
		if len(args) != 2 {
			return errors.New("usage: fitgluectl queue retry <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("bad id %q", args[1])
		}
		if err := q.Retry(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "message %d is due again\n", id)
		return nil
	}

	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	dead := fs.Bool("dead", false, "list dead-lettered messages instead of counts")
	limit := fs.Int("limit", 20, "with --dead: how many, newest first")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if *dead {
		msgs, err := q.Dead(ctx, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(stdout).Encode(msgs)
		}
		if len(msgs) == 0 {
			fmt.Fprintln(stdout, "no dead letters")
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTOPIC\tATTEMPTS\tDEAD AT\tLAST ERROR")
		for _, m := range msgs {
			fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n", m.ID, m.Topic, m.Attempts, m.DeadAt.Local().Format("2006-01-02 15:04"), oneLine(m.LastError, 80))
		}
		return tw.Flush()
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(stdout).Encode(stats)
	}
	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TOPIC\tREADY\tWAITING\tIN FLIGHT\tDEAD\tOLDEST")
	for _, s := range stats {
		oldest := "-"
		if s.OldestAt != nil {
			oldest = time.Since(*s.OldestAt).Round(time.Second).String() + " ago"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n", s.Topic, s.Ready, s.Waiting, s.Leased, s.Dead, oldest)
	}
	return tw.Flush()
}

func oneLine(s string, n int) string {
	out := make([]rune, 0, n)
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out = append(out, r)
		if len(out) >= n {
			break
		}
	}
	return string(out)
}
