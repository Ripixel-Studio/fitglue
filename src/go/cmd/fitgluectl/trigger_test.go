package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type capturedPublish struct {
	project, topic string
	data           string
	attrs          map[string]string
}

func capturePublisher(c *capturedPublish, err error) triggerPublisher {
	return func(_ context.Context, project, topic string, data []byte, attrs map[string]string) (string, error) {
		*c = capturedPublish{project: project, topic: topic, data: string(data), attrs: attrs}
		if err != nil {
			return "", err
		}
		return "msg-1", nil
	}
}

func TestTrigger_RoundupPublishesSchedulerBody(t *testing.T) {
	// What terraform/scheduler.tf's weekly-roundup-generator sent:
	// data = jsonencode({ period_type = "week" }), no attributes.
	var got capturedPublish
	var out, errb bytes.Buffer
	err := triggerCommand(context.Background(), []string{"roundup", "--data", `{"period_type":"week"}`}, &out, &errb, capturePublisher(&got, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %s)", err, errb.String())
	}
	if got.topic != "topic-roundup-trigger" || got.project != "fitglue-server-prod" {
		t.Errorf("published to %s/%s", got.project, got.topic)
	}
	if got.data != `{"period_type":"week"}` {
		t.Errorf("data = %s", got.data)
	}
	if len(got.attrs) != 0 {
		t.Errorf("attrs = %v, want none", got.attrs)
	}
	if !strings.Contains(out.String(), "message msg-1") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestTrigger_ParkrunCheckDefaultsToEmptyObject(t *testing.T) {
	// parkrun-results-check sent base64encode("{}").
	var got capturedPublish
	var out, errb bytes.Buffer
	if err := triggerCommand(context.Background(), []string{"parkrun-check"}, &out, &errb, capturePublisher(&got, nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.topic != "topic-parkrun-results-trigger" || got.data != "{}" {
		t.Errorf("got %+v", got)
	}
}

func TestTrigger_FlagsBeforeTopicAndAttrs(t *testing.T) {
	var got capturedPublish
	var out, errb bytes.Buffer
	args := []string{"--project", "p-test", "--attr", "a=1", "topic-custom", "--attr", "b=x=y"}
	if err := triggerCommand(context.Background(), args, &out, &errb, capturePublisher(&got, nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.project != "p-test" || got.topic != "topic-custom" {
		t.Errorf("got %+v", got)
	}
	if got.attrs["a"] != "1" || got.attrs["b"] != "x=y" {
		t.Errorf("attrs = %v", got.attrs)
	}
}

func TestTrigger_Rejections(t *testing.T) {
	cases := map[string][]string{
		"no topic":             {},
		"unknown short name":   {"nightly"},
		"two topics":           {"roundup", "parkrun-check"},
		"roundup without type": {"roundup"},
		"roundup bad type":     {"roundup", "--data", `{"period_type":"day"}`},
		"data not json object": {"parkrun-check", "--data", `[1]`},
		"attr without equals":  {"parkrun-check", "--attr", "novalue"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var got capturedPublish
			var out, errb bytes.Buffer
			err := triggerCommand(context.Background(), args, &out, &errb, capturePublisher(&got, nil))
			if err == nil {
				t.Fatalf("expected an error, got none (published %+v)", got)
			}
			if got.topic != "" {
				t.Errorf("published despite error: %+v", got)
			}
		})
	}
}

func TestTrigger_DryRunNeverPublishes(t *testing.T) {
	var got capturedPublish
	var out, errb bytes.Buffer
	err := triggerCommand(context.Background(), []string{"roundup", "--data", `{"period_type":"month"}`, "--dry-run"}, &out, &errb, capturePublisher(&got, errors.New("must not be called")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.topic != "" {
		t.Errorf("dry run published: %+v", got)
	}
	if !strings.Contains(out.String(), "topic-roundup-trigger") || !strings.Contains(out.String(), `"month"`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestTrigger_PublishErrorIsReturned(t *testing.T) {
	var got capturedPublish
	var out, errb bytes.Buffer
	err := triggerCommand(context.Background(), []string{"parkrun-check"}, &out, &errb, capturePublisher(&got, errors.New("boom")))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}
