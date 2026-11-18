package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fitglue/server/src/go/internal/oauthflow"
	"github.com/fitglue/server/src/go/pkg/domain/apikey"
	hevymap "github.com/fitglue/server/src/go/pkg/domain/sourcemap/hevy"
)

// `fitgluectl hevy-webhook` shows, and with --apply sets, where Hevy delivers
// workout events. Hevy keeps one webhook per account, managed through its API
// with the personal key `connect hevy` stored. --apply mints a fresh ingress
// key (stored hashed, like connect does), registers the box URL with it, and
// reads the subscription back. Neither key is ever printed.
//
// Why it exists: until 2026-11-18 Hevy posted to the cloud api-webhook, whose
// Cloud Pub/Sub messages were pushed to the box. Once the box moved to its
// own SQLite queue nothing read those messages, and Hevy workouts stopped.

type hevyWebhookFlags struct {
	project string
	uid     string
	url     string
	apply   bool
}

func parseHevyWebhookFlags(args []string, stderr io.Writer) (*hevyWebhookFlags, error) {
	f := &hevyWebhookFlags{}
	fs := flag.NewFlagSet("hevy-webhook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.StringVar(&f.url, "url", "", "webhook URL to register (default: the box's, from $"+oauthflow.PublicBaseURLEnv+")")
	fs.BoolVar(&f.apply, "apply", false, "register the URL with a new ingress key (default: show what is registered)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.uid == "" {
		return nil, errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	if f.url == "" {
		base, err := oauthflow.PublicBaseURL()
		if err != nil {
			return nil, err
		}
		f.url = boxWebhookURL(base, "hevy")
	}
	return f, nil
}

// boxWebhookURL is where a provider's webhook reaches api-webhook. Behind the
// box's Caddy the public base ends in /fg and webhooks live at
// /fg/webhook/<provider> (rewritten to /api/webhooks/<provider>); anywhere
// else api-webhook is reached directly at /api/webhooks/<provider>.
func boxWebhookURL(base, provider string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/fg") {
		return base + "/webhook/" + provider
	}
	return base + "/api/webhooks/" + provider
}

// hevySubscription is the body of Hevy's /v1/webhook-subscription.
type hevySubscription struct {
	URL       string `json:"url"`
	AuthToken string `json:"authToken,omitempty"`
}

type hevyWebhookDeps struct {
	store         integrationStore
	http          *http.Client
	base          string
	newIngressKey func() (raw, hash string, err error)
	now           func() time.Time
}

func hevyWebhookCommand(ctx context.Context, args []string, stdout, stderr io.Writer, openStore storeOpener) error {
	f, err := parseHevyWebhookFlags(args, stderr)
	if err != nil {
		return err
	}
	store, cleanup, err := openStore(ctx, f.project)
	if err != nil {
		return err
	}
	defer cleanup()
	return runHevyWebhook(ctx, f, hevyWebhookDeps{
		store:         store,
		http:          &http.Client{Timeout: 30 * time.Second},
		base:          hevymap.APIBase,
		newIngressKey: apikey.GenerateRandomIngressKey,
		now:           time.Now,
	}, stdout)
}

func runHevyWebhook(ctx context.Context, f *hevyWebhookFlags, d hevyWebhookDeps, stdout io.Writer) error {
	integ, err := d.store.GetIntegrations(ctx, f.uid)
	if err != nil {
		return fmt.Errorf("read integrations: %w", err)
	}
	key := integ.GetHevy().GetApiKey()
	if key == "" {
		return errors.New("hevy is not connected (fitgluectl connect hevy --api-key-stdin)")
	}
	current, err := hevyGetSubscription(ctx, d, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "registered: %s\n", orNone(current))
	fmt.Fprintf(stdout, "box:        %s\n", f.url)
	if !f.apply {
		if current == f.url {
			fmt.Fprintln(stdout, "Hevy already delivers to the box.")
		} else {
			fmt.Fprintln(stdout, "Dry run: --apply registers the box URL with a new ingress key.")
		}
		return nil
	}

	raw, hash, err := d.newIngressKey()
	if err != nil {
		return fmt.Errorf("generating ingress key: %w", err)
	}
	if err := d.store.CreateIngressKey(ctx, hash, f.uid, "hevy-webhook", []string{"webhook:hevy"}, d.now().UTC()); err != nil {
		return fmt.Errorf("storing ingress key: %w", err)
	}
	// Hevy keeps one subscription per account; delete first so the POST is a
	// create whether or not one exists. A 404 means there was none.
	if current != "" {
		if status, body, err := hevyDo(ctx, d, key, http.MethodDelete, nil); err != nil {
			return err
		} else if status/100 != 2 && status != http.StatusNotFound {
			return fmt.Errorf("hevy: DELETE webhook-subscription: %d %s", status, body)
		}
	}
	status, body, err := hevyDo(ctx, d, key, http.MethodPost, &hevySubscription{URL: f.url, AuthToken: raw})
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("hevy: POST webhook-subscription: %d %s", status, body)
	}
	after, err := hevyGetSubscription(ctx, d, key)
	if err != nil {
		return err
	}
	if after != f.url {
		return fmt.Errorf("hevy: registered %q but reads back %q", f.url, orNone(after))
	}
	fmt.Fprintf(stdout, "Hevy now delivers to %s (new ingress key stored, not shown).\n", after)
	return nil
}

// hevyGetSubscription returns the registered URL, or "" when there is none.
func hevyGetSubscription(ctx context.Context, d hevyWebhookDeps, key string) (string, error) {
	status, body, err := hevyDo(ctx, d, key, http.MethodGet, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", nil
	}
	if status/100 != 2 {
		return "", fmt.Errorf("hevy: GET webhook-subscription: %d %s", status, body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}
	var sub hevySubscription
	if err := json.Unmarshal(body, &sub); err != nil {
		return "", fmt.Errorf("hevy: GET webhook-subscription: %w", err)
	}
	return sub.URL, nil
}

func hevyDo(ctx context.Context, d hevyWebhookDeps, key, method string, in *hevySubscription) (int, []byte, error) {
	var reader io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(d.base, "/")+"/v1/webhook-subscription", reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("api-key", key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("hevy: %s webhook-subscription: %w", method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// Never echo the token back if Hevy includes it in an error body.
	if in != nil && in.AuthToken != "" {
		body = bytes.ReplaceAll(body, []byte(in.AuthToken), []byte("<redacted>"))
	}
	return resp.StatusCode, bytes.TrimSpace(body), nil
}
