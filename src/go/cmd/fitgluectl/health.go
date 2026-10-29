package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	firestore "cloud.google.com/go/firestore"

	"github.com/fitglue/server/src/go/internal/health"
	"github.com/fitglue/server/src/go/pkg/infrastructure/database"
	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"
)

// healthCounts is what health-check needs: the routes' reader and a census
// of the apps writing each data type. Injectable for tests.
type healthCounts interface {
	Read(ctx context.Context, kind health.Kind, days int) (interface{}, error)
	Sources(ctx context.Context, dataType string, days int) (map[string]int, error)
}

type healthFlags struct {
	project string
	uid     string
	sleep   int
	body    int
	food    int
}

// healthCheckCommand runs the sleep, body and nutrition routes' code against
// the live grant and prints counts and date ranges only — never a value —
// plus which apps wrote weight, body fat and food. Read-only: GETs and
// dailyRollUp queries (the oauth transport may refresh the access token).
func healthCheckCommand(ctx context.Context, args []string, stdout, stderr io.Writer, open func(ctx context.Context, f *healthFlags) (healthCounts, func(), error)) error {
	f := &healthFlags{}
	fs := flag.NewFlagSet("health-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding Firestore (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.IntVar(&f.sleep, "sleep-days", 7, "sleep window")
	fs.IntVar(&f.body, "body-days", 30, "body window")
	fs.IntVar(&f.food, "nutrition-days", 7, "nutrition window")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if f.uid == "" {
		return errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	r, cleanup, err := open(ctx, f)
	if err != nil {
		return err
	}
	defer cleanup()

	report := func(kind health.Kind, days int, show func(interface{})) {
		v, err := r.Read(ctx, kind, days)
		switch {
		case errors.Is(err, health.ErrNotConnected):
			fmt.Fprintf(stdout, "%-10s days=%-3d 404 not connected: %v\n", kind, days, err)
		case err != nil:
			fmt.Fprintf(stdout, "%-10s days=%-3d error: %v\n", kind, days, err)
		default:
			fmt.Fprintf(stdout, "%-10s days=%-3d 200\n", kind, days)
			show(v)
		}
	}
	report(health.KindSleep, f.sleep, func(v interface{}) {
		s := v.(*health.SleepResponse)
		var dates []string
		staged, naps := 0, 0
		for _, n := range s.Nights {
			dates = append(dates, n.Date)
			if n.Stages != nil {
				staged++
			}
			if n.Nap {
				naps++
			}
		}
		fmt.Fprintf(stdout, "  nights      %s (with stages %d, naps %d)\n", countRange(dates), staged, naps)
	})
	report(health.KindBody, f.body, func(v interface{}) {
		b := v.(*health.BodyResponse)
		line := func(name string, n int, date func(int) string) {
			var dates []string
			for i := 0; i < n; i++ {
				dates = append(dates, date(i))
			}
			fmt.Fprintf(stdout, "  %-11s %s\n", name, countRange(dates))
		}
		line("weights", len(b.Weights), func(i int) string { return b.Weights[i].Date })
		line("body_fat", len(b.BodyFat), func(i int) string { return b.BodyFat[i].Date })
		line("resting_hr", len(b.RestingHR), func(i int) string { return b.RestingHR[i].Date })
		line("hrv", len(b.HRV), func(i int) string { return b.HRV[i].Date })
	})
	report(health.KindNutrition, f.food, func(v interface{}) {
		n := v.(*health.NutritionResponse)
		var dates []string
		meals, typed := 0, 0
		for _, d := range n.Days {
			dates = append(dates, d.Date)
			meals += len(d.Meals)
			for _, m := range d.Meals {
				if m.Type != nil {
					typed++
				}
			}
		}
		fmt.Fprintf(stdout, "  days        %s\n  meals       %d (with a meal type %d)\n", countRange(dates), meals, typed)
	})

	fmt.Fprintln(stdout, "sources (raw data points by writing app, all-sources):")
	for _, c := range []struct {
		dataType string
		days     int
	}{{"weight", f.body}, {"body-fat", f.body}, {"nutrition-log", f.food}} {
		src, err := r.Sources(ctx, c.dataType, c.days)
		if err != nil {
			fmt.Fprintf(stdout, "  %-13s error: %v\n", c.dataType, err)
			continue
		}
		fmt.Fprintf(stdout, "  %-13s %s\n", c.dataType, formatSources(src))
	}
	return nil
}

// countRange is "n (first..last)" for sorted-or-not YYYY-MM-DD dates.
func countRange(dates []string) string {
	if len(dates) == 0 {
		return "0"
	}
	d := append([]string(nil), dates...)
	sort.Strings(d)
	return fmt.Sprintf("%d (%s..%s)", len(d), d[0], d[len(d)-1])
}

func formatSources(src map[string]int) string {
	if len(src) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		label := k
		switch k {
		case "com.sbs.diet":
			label += " (MacroFactor)"
		case "com.renpho.health":
			label += " (Renpho)"
		case "":
			label = "unlabelled"
		}
		parts = append(parts, fmt.Sprintf("%s=%d", label, src[k]))
	}
	return strings.Join(parts, " ")
}

// liveHealth is healthCounts over Firestore and the Google Health API.
type liveHealth struct {
	*health.Reader
	grant health.GrantSource
}

func (l *liveHealth) Sources(ctx context.Context, dataType string, days int) (map[string]int, error) {
	g, err := l.grant(ctx)
	if err != nil {
		return nil, err
	}
	today := googlehealth.DateOf(l.Reader.Now().In(l.Reader.Location))
	return googlehealth.New(g.HTTP).Sources(ctx, dataType, today.AddDays(-(days - 1)))
}

func openLiveHealth(ctx context.Context, f *healthFlags) (healthCounts, func(), error) {
	fs, err := firestore.NewClient(ctx, f.project)
	if err != nil {
		return nil, nil, fmt.Errorf("firestore client: %w", err)
	}
	grant := health.FirestoreGrant(database.NewFirestoreAdapter(fs), f.uid)
	return &liveHealth{Reader: health.NewReader(grant), grant: grant}, func() { fs.Close() }, nil
}
