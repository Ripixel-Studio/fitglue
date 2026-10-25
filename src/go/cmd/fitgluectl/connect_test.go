package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/oauthflow"
	"github.com/fitglue/server/src/go/pkg/domain/apikey"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

// fakeStore records what connect writes and serves what connections reads.
type fakeStore struct {
	integrations *pbuser.UserIntegrations
	getErr       error
	setErr       error
	keyErr       error
	set          map[string]map[string]interface{}
	setUID       string
	ingress      []ingressWrite
}

type ingressWrite struct {
	hash, uid, label string
	scopes           []string
	at               time.Time
}

func (f *fakeStore) GetIntegrations(ctx context.Context, userID string) (*pbuser.UserIntegrations, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.integrations, nil
}

func (f *fakeStore) SetIntegration(ctx context.Context, userID, provider string, data interface{}) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.set == nil {
		f.set = map[string]map[string]interface{}{}
	}
	f.setUID = userID
	f.set[provider] = data.(map[string]interface{})
	return nil
}

func (f *fakeStore) CreateIngressKey(ctx context.Context, keyHash, userID, label string, scopes []string, createdAt time.Time) error {
	if f.keyErr != nil {
		return f.keyErr
	}
	f.ingress = append(f.ingress, ingressWrite{keyHash, userID, label, scopes, createdAt})
	return nil
}

func openFake(store *fakeStore) storeOpener {
	return func(ctx context.Context, project string) (integrationStore, func(), error) {
		return store, func() {}, nil
	}
}

func noStore(t *testing.T) storeOpener {
	return func(ctx context.Context, project string) (integrationStore, func(), error) {
		t.Fatal("store should not be opened")
		return nil, nil, nil
	}
}

func fixedNow() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) }

func TestParseConnectFlags(t *testing.T) {
	t.Setenv(ownerUIDEnv, "")
	t.Setenv(projectEnv, "")
	cases := []struct {
		name    string
		args    []string
		uidEnv  string
		wantErr string
		check   func(t *testing.T, f *connectFlags)
	}{
		{name: "no provider", args: nil, wantErr: "provider is required"},
		{name: "flag before provider", args: []string{"--uid", "u", "strava"}, wantErr: "provider is required"},
		{name: "uid required", args: []string{"strava"}, wantErr: "--uid is required"},
		{name: "uid from env", args: []string{"strava"}, uidEnv: "owner", check: func(t *testing.T, f *connectFlags) {
			if f.uid != "owner" || f.project != defaultProject || f.provider != "strava" {
				t.Errorf("flags = %+v", f)
			}
		}},
		{name: "uid flag wins", args: []string{"fitbit", "--uid", "flag-uid", "--project", "p"}, uidEnv: "owner", check: func(t *testing.T, f *connectFlags) {
			if f.uid != "flag-uid" || f.project != "p" {
				t.Errorf("flags = %+v", f)
			}
		}},
		{name: "unsupported", args: []string{"garmin", "--uid", "u"}, wantErr: "unsupported provider"},
		{name: "trainingpeaks explained", args: []string{"trainingpeaks", "--uid", "u"}, wantErr: "no authorise configuration"},
		{name: "oauth rejects api key", args: []string{"strava", "--uid", "u", "--api-key", "k"}, wantErr: "do not apply"},
		{name: "oauth rejects athlete id", args: []string{"github", "--uid", "u", "--athlete-id", "i1"}, wantErr: "do not apply"},
		{name: "intervals needs athlete", args: []string{"intervals", "--uid", "u", "--api-key", "k"}, wantErr: "--athlete-id is required"},
		{name: "hevy rejects athlete", args: []string{"hevy", "--uid", "u", "--api-key", "k", "--athlete-id", "i1"}, wantErr: "does not apply"},
		{name: "key flag and stdin exclusive", args: []string{"hevy", "--uid", "u", "--api-key", "k", "--api-key-stdin"}, wantErr: "mutually exclusive"},
		{name: "unknown flag", args: []string{"hevy", "--uid", "u", "--bogus"}, wantErr: "bogus"},
		{name: "hevy ok", args: []string{"hevy", "--uid", "u", "--api-key-stdin"}, check: func(t *testing.T, f *connectFlags) {
			if !f.apiKeyStdin || f.provider != "hevy" {
				t.Errorf("flags = %+v", f)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ownerUIDEnv, tc.uidEnv)
			var stderr bytes.Buffer
			f, err := parseConnectFlags(tc.args, &stderr)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error()+stderr.String(), tc.wantErr) {
					t.Fatalf("err = %v (stderr %q), want containing %q", err, stderr.String(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, f)
		})
	}
}

func TestConnectOAuthPrintsAuthoriseURL(t *testing.T) {
	t.Setenv(ownerUIDEnv, "owner-uid")
	t.Setenv(oauthflow.StateSecretEnv, "shared-secret")
	t.Setenv(oauthflow.PublicBaseURLEnv, "https://jarvis.tail3065f7.ts.net/fg")
	t.Setenv("STRAVA_CLIENT_ID", "4242")

	var stdout, stderr bytes.Buffer
	err := connectCommand(context.Background(), []string{"strava"}, strings.NewReader(""), &stdout, &stderr, noStore(t))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(stdout.String())
	if strings.Contains(line, "\n") {
		t.Fatalf("expected exactly one line, got %q", line)
	}
	u, err := url.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "www.strava.com" || q.Get("client_id") != "4242" ||
		q.Get("redirect_uri") != "https://jarvis.tail3065f7.ts.net/fg/api/v2/oauth/strava/callback" ||
		q.Get("scope") != "read_all,activity:read_all,activity:write" {
		t.Errorf("url = %s", line)
	}
	// The callback verifies the state with the same secret and recovers the owner uid.
	uid, err := oauthflow.VerifyState([]byte("shared-secret"), q.Get("state"), time.Now())
	if err != nil || uid != "owner-uid" {
		t.Errorf("state = %q, %v", uid, err)
	}

	// Same URL api-client's handler would produce for the same state.
	cfg := oauthflow.Provider("strava")
	want, _ := oauthflow.AuthorizeURL("strava", cfg, oauthflow.CallbackURL("https://jarvis.tail3065f7.ts.net/fg", "strava"), q.Get("state"))
	if want != line {
		t.Errorf("CLI url differs from shared builder:\n%s\n%s", line, want)
	}
}

func TestConnectOAuthPreconditions(t *testing.T) {
	t.Setenv(ownerUIDEnv, "owner-uid")
	t.Setenv(oauthflow.PublicBaseURLEnv, "https://fitglue.tech")
	t.Setenv("FITBIT_CLIENT_ID", "")
	t.Setenv(oauthflow.StateSecretEnv, "")

	run := func() error {
		var stdout, stderr bytes.Buffer
		return connectCommand(context.Background(), []string{"fitbit"}, strings.NewReader(""), &stdout, &stderr, noStore(t))
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), oauthflow.StateSecretEnv) {
		t.Errorf("missing secret: err = %v", err)
	}
	t.Setenv(oauthflow.StateSecretEnv, "s")
	if err := run(); err == nil || !strings.Contains(err.Error(), "client id") {
		t.Errorf("missing client id: err = %v", err)
	}
	t.Setenv("FITBIT_CLIENT_ID", "fb")
	t.Setenv(oauthflow.PublicBaseURLEnv, "")
	t.Setenv(oauthflow.LegacyBaseURLEnv, "")
	if err := run(); err == nil || !strings.Contains(err.Error(), oauthflow.PublicBaseURLEnv) {
		t.Errorf("missing base url: err = %v", err)
	}
}

func TestConnectHevyStoresKeyAndIngressKey(t *testing.T) {
	t.Setenv(oauthflow.PublicBaseURLEnv, "https://fitglue.tech")
	store := &fakeStore{}
	f := &connectFlags{provider: "hevy", uid: "owner", apiKeyStdin: true}
	deps := connectDeps{
		store: store,
		now:   fixedNow,
		newIngressKey: func() (string, string, error) {
			return "raw-ingress", apikey.HashIngressKey("raw-ingress"), nil
		},
	}
	var stdout bytes.Buffer
	if err := runConnect(context.Background(), f, deps, strings.NewReader("  hevy-personal-key \nignored second line\n"), &stdout); err != nil {
		t.Fatal(err)
	}

	got := store.set["hevy"]
	if store.setUID != "owner" || got == nil {
		t.Fatalf("SetIntegration not called for owner/hevy: %+v", store.set)
	}
	// Same document the web app's PUT /users/me/integrations/hevy wrote.
	for k, want := range map[string]interface{}{
		"apiKey":        "hevy-personal-key",
		"enabled":       true,
		"consent_given": true,
		"connected_at":  "2026-10-25T12:00:00Z",
		"created_at":    "2026-10-25T12:00:00Z",
	} {
		if got[k] != want {
			t.Errorf("hevy[%q] = %v, want %v", k, got[k], want)
		}
	}
	if len(got) != 5 {
		t.Errorf("unexpected extra fields: %v", got)
	}

	if len(store.ingress) != 1 {
		t.Fatalf("ingress writes = %d", len(store.ingress))
	}
	iw := store.ingress[0]
	if iw.hash != apikey.HashIngressKey("raw-ingress") || iw.uid != "owner" || iw.label != "hevy-webhook" ||
		strings.Join(iw.scopes, ",") != "webhook:hevy" || !iw.at.Equal(fixedNow()) {
		t.Errorf("ingress write = %+v", iw)
	}
	if iw.hash == "raw-ingress" {
		t.Error("raw key must never be stored")
	}
	out := stdout.String()
	if !strings.Contains(out, "raw-ingress") || !strings.Contains(out, "https://fitglue.tech/api/webhooks/hevy") {
		t.Errorf("stdout should show the raw ingress key and the webhook URL once:\n%s", out)
	}
	if strings.Contains(out, "hevy-personal-key") {
		t.Error("stdout must not echo the Hevy API key")
	}
}

func TestConnectHevyErrors(t *testing.T) {
	deps := connectDeps{now: fixedNow, newIngressKey: func() (string, string, error) { return "r", "h", nil }}

	// empty key
	deps.store = &fakeStore{}
	err := runConnect(context.Background(), &connectFlags{provider: "hevy", uid: "u", apiKeyStdin: true}, deps, strings.NewReader("\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("empty stdin: %v", err)
	}

	// store failure surfaces and no ingress key is minted
	store := &fakeStore{setErr: errors.New("firestore down")}
	deps.store = store
	err = runConnect(context.Background(), &connectFlags{provider: "hevy", uid: "u", apiKey: "k"}, deps, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "firestore down") || len(store.ingress) != 0 {
		t.Errorf("set error: %v, ingress=%d", err, len(store.ingress))
	}

	// ingress key failure
	store = &fakeStore{keyErr: errors.New("key write failed")}
	deps.store = store
	err = runConnect(context.Background(), &connectFlags{provider: "hevy", uid: "u", apiKey: "k"}, deps, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "key write failed") {
		t.Errorf("ingress error: %v", err)
	}

	// key generator failure
	deps.store = &fakeStore{}
	deps.newIngressKey = func() (string, string, error) { return "", "", errors.New("no entropy") }
	err = runConnect(context.Background(), &connectFlags{provider: "hevy", uid: "u", apiKey: "k"}, deps, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no entropy") {
		t.Errorf("generator error: %v", err)
	}
}

func TestConnectIntervals(t *testing.T) {
	store := &fakeStore{}
	var verified []string
	deps := connectDeps{
		store: store,
		now:   fixedNow,
		verifyIntervals: func(ctx context.Context, apiKey, athleteID string) error {
			verified = append(verified, apiKey+"@"+athleteID)
			if athleteID == "bad" {
				return errors.New("401")
			}
			return nil
		},
	}
	var stdout bytes.Buffer
	f := &connectFlags{provider: "intervals", uid: "owner", apiKey: "ikey", athleteID: "i123"}
	if err := runConnect(context.Background(), f, deps, strings.NewReader(""), &stdout); err != nil {
		t.Fatal(err)
	}
	if strings.Join(verified, ",") != "ikey@i123" {
		t.Errorf("verified = %v", verified)
	}
	got := store.set["intervals"]
	if got["api_key"] != "ikey" || got["athlete_id"] != "i123" || got["enabled"] != true || got["consent_given"] != true || got["connected_at"] != "2026-10-25T12:00:00Z" {
		t.Errorf("intervals doc = %v", got)
	}
	if len(store.ingress) != 0 {
		t.Error("intervals must not mint an ingress key")
	}

	store = &fakeStore{}
	deps.store = store
	f.athleteID = "bad"
	err := runConnect(context.Background(), f, deps, strings.NewReader(""), &stdout)
	if err == nil || !strings.Contains(err.Error(), "could not verify") || len(store.set) != 0 {
		t.Errorf("failed verification should not store: err=%v set=%v", err, store.set)
	}
}

func TestConnectCommandOpensStoreForKeyProviders(t *testing.T) {
	t.Setenv(ownerUIDEnv, "owner")
	t.Setenv(oauthflow.PublicBaseURLEnv, "https://fitglue.tech")
	opened := 0
	store := &fakeStore{}
	opener := func(ctx context.Context, project string) (integrationStore, func(), error) {
		opened++
		if project != "my-project" {
			t.Errorf("project = %q", project)
		}
		return store, func() {}, nil
	}
	var stdout, stderr bytes.Buffer
	err := connectCommand(context.Background(), []string{"hevy", "--project", "my-project", "--api-key", "k"}, strings.NewReader(""), &stdout, &stderr, opener)
	if err != nil || opened != 1 || store.set["hevy"] == nil || len(store.ingress) != 1 {
		t.Errorf("err=%v opened=%d set=%v ingress=%d", err, opened, store.set, len(store.ingress))
	}

	failing := func(ctx context.Context, project string) (integrationStore, func(), error) {
		return nil, nil, errors.New("no credentials")
	}
	err = connectCommand(context.Background(), []string{"hevy", "--api-key", "k"}, strings.NewReader(""), &stdout, &stderr, failing)
	if err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("opener error: %v", err)
	}
}

func sampleIntegrations() *pbuser.UserIntegrations {
	created := timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	expires := timestamppb.New(time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC))
	return &pbuser.UserIntegrations{
		Strava:    &pbuser.StravaIntegration{Enabled: true, AccessToken: "secret-token", RefreshToken: "r", ExpiresAt: expires, AthleteId: 98765, CreatedAt: created},
		Hevy:      &pbuser.HevyIntegration{Enabled: true, ApiKey: "hevy-secret"},
		Fitbit:    &pbuser.FitbitIntegration{Enabled: false, FitbitUserId: "ABC123", CreatedAt: created},
		Intervals: &pbuser.IntervalsIntegration{Enabled: true, ApiKey: "ik", AthleteId: "i1"},
	}
}

func TestListConnections(t *testing.T) {
	rows := listConnections(sampleIntegrations())
	byProvider := map[string]connection{}
	var order []string
	for _, r := range rows {
		byProvider[r.Provider] = r
		order = append(order, r.Provider)
	}
	if strings.Join(order, ",") != "fitbit,hevy,intervals,strava" {
		t.Errorf("order = %v", order)
	}
	s := byProvider["strava"]
	if !s.Enabled || s.ConnectedAt != "2026-01-02T03:04:05Z" || s.ExpiresAt != "2026-10-25T18:00:00Z" || s.ExternalID != "98765" || !s.HasKey {
		t.Errorf("strava = %+v", s)
	}
	h := byProvider["hevy"]
	if !h.Enabled || h.ConnectedAt != "" || h.ExpiresAt != "" || !h.HasKey {
		t.Errorf("hevy = %+v", h)
	}
	fb := byProvider["fitbit"]
	if fb.Enabled || fb.ExternalID != "ABC123" || fb.HasKey {
		t.Errorf("fitbit = %+v", fb)
	}
	if iv := byProvider["intervals"]; iv.ExternalID != "i1" || !iv.HasKey {
		t.Errorf("intervals = %+v", iv)
	}
	if len(listConnections(nil)) != 0 || len(listConnections(&pbuser.UserIntegrations{})) != 0 {
		t.Error("empty integrations should list nothing")
	}
}

func TestConnectionsCommand(t *testing.T) {
	t.Setenv(ownerUIDEnv, "owner")
	store := &fakeStore{integrations: sampleIntegrations()}

	var stdout, stderr bytes.Buffer
	if err := connectionsCommand(context.Background(), nil, &stdout, &stderr, openFake(store)); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"PROVIDER", "strava", "2026-10-25T18:00:00Z", "98765", "hevy", "present", "missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"secret-token", "hevy-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("table leaks %q", secret)
		}
	}

	stdout.Reset()
	if err := connectionsCommand(context.Background(), []string{"--json"}, &stdout, &stderr, openFake(store)); err != nil {
		t.Fatal(err)
	}
	var rows []connection
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout.String())
	}
	if len(rows) != 4 || rows[3].Provider != "strava" || rows[3].ExpiresAt != "2026-10-25T18:00:00Z" {
		t.Errorf("rows = %+v", rows)
	}
	if strings.Contains(stdout.String(), "secret") {
		t.Error("json leaks a secret")
	}

	stdout.Reset()
	if err := connectionsCommand(context.Background(), nil, &stdout, &stderr, openFake(&fakeStore{integrations: &pbuser.UserIntegrations{}})); err != nil || !strings.Contains(stdout.String(), "no integrations") {
		t.Errorf("empty: err=%v out=%q", err, stdout.String())
	}

	err := connectionsCommand(context.Background(), nil, &stdout, &stderr, openFake(&fakeStore{getErr: errors.New("boom")}))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("store error: %v", err)
	}

	t.Setenv(ownerUIDEnv, "")
	if err := connectionsCommand(context.Background(), nil, &stdout, &stderr, openFake(store)); err == nil || !strings.Contains(err.Error(), "--uid") {
		t.Errorf("uid required: %v", err)
	}
	if err := connectionsCommand(context.Background(), []string{"--uid", "u", "--nope"}, &stdout, &stderr, openFake(store)); err == nil {
		t.Error("unknown flag should error")
	}
}
