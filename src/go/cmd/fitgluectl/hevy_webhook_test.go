package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fitglue/server/src/go/pkg/domain/apikey"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

// fakeHevy is Hevy's /v1/webhook-subscription: one subscription per account.
type fakeHevy struct {
	sub     *hevySubscription
	methods []string
	keys    []string
}

func (h *fakeHevy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/webhook-subscription" {
		http.NotFound(w, r)
		return
	}
	h.methods = append(h.methods, r.Method)
	h.keys = append(h.keys, r.Header.Get("api-key"))
	switch r.Method {
	case http.MethodGet:
		if h.sub == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(h.sub)
	case http.MethodDelete:
		h.sub = nil
		w.WriteHeader(http.StatusOK)
	case http.MethodPost:
		if h.sub != nil {
			http.Error(w, "subscription exists", http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var s hevySubscription
		_ = json.Unmarshal(body, &s)
		h.sub = &s
		w.WriteHeader(http.StatusCreated)
	}
}

func hevyFixture(t *testing.T, sub *hevySubscription) (*fakeHevy, *fakeStore, hevyWebhookDeps) {
	t.Helper()
	h := &fakeHevy{sub: sub}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	store := &fakeStore{integrations: &pbuser.UserIntegrations{Hevy: &pbuser.HevyIntegration{ApiKey: "personal-key"}}}
	return h, store, hevyWebhookDeps{
		store: store,
		http:  srv.Client(),
		base:  srv.URL,
		newIngressKey: func() (string, string, error) {
			return "raw-ingress", apikey.HashIngressKey("raw-ingress"), nil
		},
		now: func() time.Time { return time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC) },
	}
}

const boxHevyURL = "https://jarvis.example.ts.net/fg/webhook/hevy"

func TestHevyWebhookDryRunShowsCloudURL(t *testing.T) {
	h, store, d := hevyFixture(t, &hevySubscription{URL: "https://fitglue.tech/api/webhooks/hevy"})
	var out bytes.Buffer
	if err := runHevyWebhook(context.Background(), &hevyWebhookFlags{uid: "u1", url: boxHevyURL}, d, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "registered: https://fitglue.tech/api/webhooks/hevy") || !strings.Contains(out.String(), "Dry run") {
		t.Fatalf("output:\n%s", out.String())
	}
	if len(store.ingress) != 0 || strings.Join(h.methods, ",") != "GET" {
		t.Fatalf("dry run wrote: ingress=%d methods=%v", len(store.ingress), h.methods)
	}
	if h.keys[0] != "personal-key" {
		t.Fatalf("api-key header = %q", h.keys[0])
	}
}

func TestHevyWebhookApplyRepointsToBox(t *testing.T) {
	h, store, d := hevyFixture(t, &hevySubscription{URL: "https://fitglue.tech/api/webhooks/hevy", AuthToken: "old"})
	var out bytes.Buffer
	if err := runHevyWebhook(context.Background(), &hevyWebhookFlags{uid: "u1", url: boxHevyURL, apply: true}, d, &out); err != nil {
		t.Fatal(err)
	}
	if h.sub == nil || h.sub.URL != boxHevyURL || h.sub.AuthToken != "raw-ingress" {
		t.Fatalf("hevy subscription = %+v", h.sub)
	}
	if strings.Join(h.methods, ",") != "GET,DELETE,POST,GET" {
		t.Fatalf("methods = %v", h.methods)
	}
	if len(store.ingress) != 1 || store.ingress[0].hash != apikey.HashIngressKey("raw-ingress") || store.ingress[0].scopes[0] != "webhook:hevy" {
		t.Fatalf("ingress = %+v", store.ingress)
	}
	if strings.Contains(out.String(), "raw-ingress") || strings.Contains(out.String(), "personal-key") {
		t.Fatalf("a key was printed:\n%s", out.String())
	}
}

func TestHevyWebhookApplyWithNoSubscription(t *testing.T) {
	h, _, d := hevyFixture(t, nil)
	var out bytes.Buffer
	if err := runHevyWebhook(context.Background(), &hevyWebhookFlags{uid: "u1", url: boxHevyURL, apply: true}, d, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.methods, ",") != "GET,POST,GET" || h.sub.URL != boxHevyURL {
		t.Fatalf("methods = %v sub = %+v", h.methods, h.sub)
	}
}

func TestHevyWebhookNotConnected(t *testing.T) {
	_, store, d := hevyFixture(t, nil)
	store.integrations = &pbuser.UserIntegrations{}
	err := runHevyWebhook(context.Background(), &hevyWebhookFlags{uid: "u1", url: boxHevyURL}, d, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("err = %v", err)
	}
}

func TestBoxWebhookURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://jarvis.example.ts.net/fg":  "https://jarvis.example.ts.net/fg/webhook/hevy",
		"https://jarvis.example.ts.net/fg/": "https://jarvis.example.ts.net/fg/webhook/hevy",
		"https://fitglue.tech":              "https://fitglue.tech/api/webhooks/hevy",
	} {
		if got := boxWebhookURL(base, "hevy"); got != want {
			t.Errorf("boxWebhookURL(%q) = %q, want %q", base, got, want)
		}
	}
}
