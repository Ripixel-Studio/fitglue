package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fitglue/server/src/go/internal/health"
)

type fakeCounts struct{}

func (fakeCounts) Read(ctx context.Context, kind health.Kind, days int) (interface{}, error) {
	switch kind {
	case health.KindSleep:
		return &health.SleepResponse{Nights: []health.Night{
			{Date: "2026-10-29", MinutesAsleep: 432, Stages: &health.Stages{Deep: 64}},
			{Date: "2026-10-27", MinutesAsleep: 401, Nap: true},
		}}, nil
	case health.KindBody:
		return &health.BodyResponse{Weights: []health.Weight{{Date: "2026-10-28", Kg: 78.41}, {Date: "2026-10-01", Kg: 79.9}}}, nil
	}
	return nil, fmt.Errorf("%w: the grant lacks nutrition", health.ErrNotConnected)
}

func (fakeCounts) Sources(ctx context.Context, dataType string, days int) (map[string]int, error) {
	if dataType == "nutrition-log" {
		return map[string]int{"com.sbs.diet": 58}, nil
	}
	return map[string]int{"com.renpho.health": 23, "GOOGLE_WEB_API": 25}, nil
}

func TestHealthCheckPrintsCountsNotValues(t *testing.T) {
	var out, errOut bytes.Buffer
	open := func(ctx context.Context, f *healthFlags) (healthCounts, func(), error) {
		if f.uid != "u1" || f.body != 30 {
			t.Errorf("flags = %+v", f)
		}
		return fakeCounts{}, func() {}, nil
	}
	if err := healthCheckCommand(context.Background(), []string{"--uid", "u1"}, &out, &errOut, open); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"nights      2 (2026-10-27..2026-10-29) (with stages 1, naps 1)",
		"weights     2 (2026-10-01..2026-10-28)",
		"body_fat    0",
		"nutrition  days=7   404 not connected",
		"weight        GOOGLE_WEB_API=25 com.renpho.health (Renpho)=23",
		"nutrition-log com.sbs.diet (MacroFactor)=58",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	for _, value := range []string{"432", "401", "78.41", "79.9", "64"} {
		if strings.Contains(s, value) {
			t.Errorf("printed a value %q:\n%s", value, s)
		}
	}
}

func TestHealthCheckNeedsUID(t *testing.T) {
	t.Setenv(ownerUIDEnv, "")
	var out, errOut bytes.Buffer
	err := healthCheckCommand(context.Background(), nil, &out, &errOut, nil)
	if err == nil || !strings.Contains(err.Error(), "--uid") {
		t.Errorf("err = %v", err)
	}
}
