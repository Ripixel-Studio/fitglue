package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"cloud.google.com/go/pubsub" //nolint:staticcheck // v1 client, same as the services
)

// `fitgluectl trigger` publishes the one-shot messages Cloud Scheduler used to
// (terraform/scheduler.tf, deleted in F3.4). The box's systemd timers call it:
//
//	fitglue-roundup-weekly.timer   → trigger roundup --data '{"period_type":"week"}'
//	fitglue-roundup-monthly.timer  → trigger roundup --data '{"period_type":"month"}'
//	fitglue-roundup-yearly.timer   → trigger roundup --data '{"period_type":"year"}'
//	fitglue-parkrun-check.timer    → trigger parkrun-check            (data "{}")
//
// The message body and attributes are exactly what the Scheduler jobs sent, so
// the push handlers (activity /pubsub/roundup, pipeline /pubsub/parkrun-check)
// are untouched. Publishing uses ADC: on the box GOOGLE_APPLICATION_CREDENTIALS
// is the fitglue-box key, which has pubsub.publisher.

// triggerTopics maps the short names the timers use to the real topic ids. A
// full `topic-…` name is accepted as well so a new topic needs no code change.
var triggerTopics = map[string]string{
	"roundup":       "topic-roundup-trigger",
	"parkrun-check": "topic-parkrun-results-trigger",
}

type triggerFlags struct {
	topic   string
	project string
	data    string
	attrs   map[string]string
	dryRun  bool
}

type attrList map[string]string

func (a attrList) String() string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+a[k])
	}
	return strings.Join(parts, ",")
}

func (a attrList) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("attribute %q is not key=value", v)
	}
	a[k] = val
	return nil
}

func parseTriggerFlags(args []string, stderr io.Writer) (*triggerFlags, error) {
	f := &triggerFlags{attrs: map[string]string{}}
	fs := flag.NewFlagSet("trigger", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: fitgluectl trigger <topic> [--data <json>] [--attr k=v ...] [--project P] [--dry-run]")
		fmt.Fprintln(stderr, "topics: roundup (topic-roundup-trigger), parkrun-check (topic-parkrun-results-trigger), or any full topic-… name")
		fs.PrintDefaults()
	}
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding the topic (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.data, "data", "{}", "message body; must be a JSON object")
	fs.Var(attrList(f.attrs), "attr", "message attribute key=value (repeatable); the Scheduler jobs sent none")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print what would be published and exit without touching Pub/Sub")

	// The topic is positional and may come before or after the flags.
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	if len(positional) != 1 {
		fs.Usage()
		return nil, errors.New("exactly one topic is required")
	}
	short := positional[0]
	if full, ok := triggerTopics[short]; ok {
		f.topic = full
	} else if strings.HasPrefix(short, "topic-") {
		f.topic = short
	} else {
		return nil, fmt.Errorf("unknown topic %q (roundup, parkrun-check, or a full topic-… name)", short)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(f.data), &body); err != nil {
		return nil, fmt.Errorf("--data is not a JSON object: %w", err)
	}
	if f.topic == triggerTopics["roundup"] {
		pt, _ := body["period_type"].(string)
		switch pt {
		case "week", "month", "year":
		default:
			return nil, fmt.Errorf(`roundup trigger needs --data '{"period_type":"week"|"month"|"year"}' (got %q)`, pt)
		}
	}
	return f, nil
}

// triggerPublisher is the one Pub/Sub call the command makes; a func so the
// test can capture the message without a client.
type triggerPublisher func(ctx context.Context, project, topic string, data []byte, attrs map[string]string) (string, error)

func triggerCommand(ctx context.Context, args []string, stdout, stderr io.Writer, publish triggerPublisher) error {
	f, err := parseTriggerFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.dryRun {
		fmt.Fprintf(stdout, "dry-run: would publish to projects/%s/topics/%s data=%s attrs=%s\n", f.project, f.topic, f.data, attrList(f.attrs).String())
		return nil
	}
	id, err := publish(ctx, f.project, f.topic, []byte(f.data), f.attrs)
	if err != nil {
		return fmt.Errorf("publish to %s: %w", f.topic, err)
	}
	fmt.Fprintf(stdout, "published %s to %s (message %s)\n", f.data, f.topic, id)
	return nil
}

// publishToPubSub is the real publisher: one message, wait for the server id.
func publishToPubSub(ctx context.Context, project, topic string, data []byte, attrs map[string]string) (string, error) {
	client, err := pubsub.NewClient(ctx, project)
	if err != nil {
		return "", fmt.Errorf("pubsub client: %w", err)
	}
	defer client.Close()
	t := client.Topic(topic)
	defer t.Stop()
	msg := &pubsub.Message{Data: data}
	if len(attrs) > 0 {
		msg.Attributes = attrs
	}
	return t.Publish(ctx, msg).Get(ctx)
}
