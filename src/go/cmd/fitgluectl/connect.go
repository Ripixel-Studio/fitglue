package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/fitglue/server/src/go/internal/oauthflow"
	"github.com/fitglue/server/src/go/pkg/domain/apikey"
	"github.com/fitglue/server/src/go/pkg/integrations/intervals"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

const (
	ownerUIDEnv = "FITGLUE_OWNER_UID"
	projectEnv  = "PROJECT_ID"
)

// integrationStore is the slice of internal/user.FirestoreStore the connect
// and connections commands need. It is satisfied by *user.FirestoreStore and
// by the fake in the tests.
type integrationStore interface {
	GetIntegrations(ctx context.Context, userID string) (*pbuser.UserIntegrations, error)
	SetIntegration(ctx context.Context, userID, provider string, data interface{}) error
	CreateIngressKey(ctx context.Context, keyHash, userID, label string, scopes []string, createdAt time.Time) error
}

// storeOpener opens the owner's Firestore-backed store for a project.
type storeOpener func(ctx context.Context, project string) (integrationStore, func(), error)

// connectDeps are the side-effecting collaborators of connect, injectable for tests.
type connectDeps struct {
	store           integrationStore
	verifyIntervals func(ctx context.Context, apiKey, athleteID string) error
	newIngressKey   func() (raw, hash string, err error)
	now             func() time.Time
}

type connectFlags struct {
	provider    string
	project     string
	uid         string
	apiKey      string
	apiKeyStdin bool
	athleteID   string
}

// apiKeyProviders are connected with a credential, not an authorise URL.
var apiKeyProviders = []string{"hevy", "intervals"}

func connectUsage(fs *flag.FlagSet, stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: fitgluectl connect <provider> [flags]")
	fmt.Fprintf(stderr, "  OAuth providers (prints the authorise URL): %s\n", strings.Join(oauthflow.Providers(), ", "))
	fmt.Fprintln(stderr, "  hevy       --api-key <key> | --api-key-stdin")
	fmt.Fprintln(stderr, "  intervals  --api-key <key> | --api-key-stdin, plus --athlete-id <id>")
	fmt.Fprintln(stderr, "flags:")
	fs.PrintDefaults()
}

func parseConnectFlags(args []string, stderr io.Writer) (*connectFlags, error) {
	f := &connectFlags{}
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding Firestore (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.StringVar(&f.apiKey, "api-key", "", "API key for hevy / intervals (prefer --api-key-stdin: flags end up in shell history)")
	fs.BoolVar(&f.apiKeyStdin, "api-key-stdin", false, "read the API key from the first line of stdin")
	fs.StringVar(&f.athleteID, "athlete-id", "", "Intervals.icu athlete id (e.g. i12345)")
	fs.Usage = func() { connectUsage(fs, stderr) }
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		connectUsage(fs, stderr)
		return nil, errors.New("connect: provider is required")
	}
	f.provider = args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	if f.uid == "" {
		return nil, errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	if f.apiKey != "" && f.apiKeyStdin {
		return nil, errors.New("--api-key and --api-key-stdin are mutually exclusive")
	}
	isKeyProvider := contains(apiKeyProviders, f.provider)
	isOAuth := oauthflow.Provider(f.provider) != nil
	switch {
	case f.provider == "trainingpeaks":
		return nil, errors.New("trainingpeaks: no authorise configuration exists (the web app never had one; tokens refresh via pkg/infrastructure/oauth but a first connection has to be set up by hand — see docs/guides/connect-sources.md)")
	case !isKeyProvider && !isOAuth:
		return nil, fmt.Errorf("unsupported provider %q (oauth: %s; api key: %s)", f.provider, strings.Join(oauthflow.Providers(), ", "), strings.Join(apiKeyProviders, ", "))
	case isOAuth && (f.apiKey != "" || f.apiKeyStdin || f.athleteID != ""):
		return nil, fmt.Errorf("%s is an OAuth provider; --api-key/--athlete-id do not apply", f.provider)
	case f.provider == "intervals" && f.athleteID == "":
		return nil, errors.New("intervals: --athlete-id is required")
	case f.provider == "hevy" && f.athleteID != "":
		return nil, errors.New("hevy: --athlete-id does not apply")
	}
	return f, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// connectCommand is the `fitgluectl connect` entry point. OAuth providers
// never touch Firestore (printing a URL is side-effect free); API-key providers
// open the store through openStore.
func connectCommand(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, openStore storeOpener) error {
	f, err := parseConnectFlags(args, stderr)
	if err != nil {
		return err
	}
	deps := connectDeps{
		verifyIntervals: intervals.VerifyCredentials,
		newIngressKey:   apikey.GenerateRandomIngressKey,
		now:             time.Now,
	}
	if contains(apiKeyProviders, f.provider) {
		store, cleanup, err := openStore(ctx, f.project)
		if err != nil {
			return err
		}
		defer cleanup()
		deps.store = store
	}
	return runConnect(ctx, f, deps, stdin, stdout)
}

func runConnect(ctx context.Context, f *connectFlags, deps connectDeps, stdin io.Reader, stdout io.Writer) error {
	if deps.now == nil {
		deps.now = time.Now
	}
	switch f.provider {
	case "hevy":
		return connectHevy(ctx, f, deps, stdin, stdout)
	case "intervals":
		return connectIntervals(ctx, f, deps, stdin, stdout)
	}
	return connectOAuth(f, deps, stdout)
}

// connectOAuth prints the authorise URL exactly as api-client's
// POST /users/me/integrations/{provider}/connect would have returned it.
func connectOAuth(f *connectFlags, deps connectDeps, stdout io.Writer) error {
	secret := os.Getenv(oauthflow.StateSecretEnv)
	if secret == "" {
		return errors.New(oauthflow.StateSecretEnv + " must be set to the value api-client runs with, or the callback will reject the state")
	}
	cfg := oauthflow.Provider(f.provider)
	if cfg.ClientID == "" {
		return fmt.Errorf("%s: client id env var is not set (see docs/guides/connect-sources.md)", f.provider)
	}
	authURL, err := oauthflow.Connect(f.provider, f.uid, []byte(secret), deps.now())
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, authURL)
	return nil
}

func readAPIKey(f *connectFlags, stdin io.Reader) (string, error) {
	key := f.apiKey
	if f.apiKeyStdin {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading api key from stdin: %w", err)
		}
		key = line
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("an API key is required: --api-key <key> or --api-key-stdin")
	}
	return key, nil
}

// connectHevy stores the Hevy personal API key the way the web app's
// PUT /users/me/integrations/hevy did (camelCase apiKey, enabled, consent_given,
// connected_at) and mints the webhook ingress key Hevy must send back, which
// is how api-webhook resolves the user. created_at is added so `connections`
// can show when it happened.
func connectHevy(ctx context.Context, f *connectFlags, deps connectDeps, stdin io.Reader, stdout io.Writer) error {
	key, err := readAPIKey(f, stdin)
	if err != nil {
		return err
	}
	now := deps.now().UTC()
	data := map[string]interface{}{
		"apiKey":        key,
		"enabled":       true,
		"consent_given": true,
		"connected_at":  now.Format(time.RFC3339),
		"created_at":    now.Format(time.RFC3339),
	}
	if err := deps.store.SetIntegration(ctx, f.uid, "hevy", data); err != nil {
		return fmt.Errorf("storing hevy integration: %w", err)
	}
	rawKey, keyHash, err := deps.newIngressKey()
	if err != nil {
		return fmt.Errorf("generating ingress key: %w", err)
	}
	if err := deps.store.CreateIngressKey(ctx, keyHash, f.uid, "hevy-webhook", []string{"webhook:hevy"}, now); err != nil {
		return fmt.Errorf("storing ingress key: %w", err)
	}
	fmt.Fprintln(stdout, "hevy: API key stored.")
	fmt.Fprintln(stdout, "Register a webhook in Hevy (Settings -> Developer -> Webhooks) with:")
	fmt.Fprintf(stdout, "  URL:        %s\n", webhookURL("hevy"))
	fmt.Fprintf(stdout, "  Auth token: %s\n", rawKey)
	fmt.Fprintln(stdout, "The token is shown once; a lost token means running connect hevy again.")
	return nil
}

// connectIntervals verifies the key against Intervals.icu (as the web flow did)
// and stores it under the snake_case names the Firestore converter expects.
func connectIntervals(ctx context.Context, f *connectFlags, deps connectDeps, stdin io.Reader, stdout io.Writer) error {
	key, err := readAPIKey(f, stdin)
	if err != nil {
		return err
	}
	if err := deps.verifyIntervals(ctx, key, f.athleteID); err != nil {
		return fmt.Errorf("could not verify Intervals.icu credentials: %w", err)
	}
	now := deps.now().UTC()
	data := map[string]interface{}{
		"api_key":       key,
		"athlete_id":    f.athleteID,
		"enabled":       true,
		"consent_given": true,
		"connected_at":  now.Format(time.RFC3339),
		"created_at":    now.Format(time.RFC3339),
	}
	if err := deps.store.SetIntegration(ctx, f.uid, "intervals", data); err != nil {
		return fmt.Errorf("storing intervals integration: %w", err)
	}
	fmt.Fprintf(stdout, "intervals: API key stored for athlete %s.\n", f.athleteID)
	fmt.Fprintln(stdout, "Note: api-webhook routes only strava, fitbit and hevy; Intervals.icu is a destination (upload) and a history source, not a webhook source.")
	return nil
}

// webhookURL is where api-webhook receives a provider's events; it is derived
// from the same public base URL the OAuth callback uses.
func webhookURL(provider string) string {
	base, err := oauthflow.PublicBaseURL()
	if err != nil {
		return "<" + oauthflow.PublicBaseURLEnv + " unset>/api/webhooks/" + provider
	}
	return base + "/api/webhooks/" + provider
}

// ---- connections ----

type connectionsFlags struct {
	project string
	uid     string
	json    bool
}

func parseConnectionsFlags(args []string, stderr io.Writer) (*connectionsFlags, error) {
	f := &connectionsFlags{}
	fs := flag.NewFlagSet("connections", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.project, "project", envOr(projectEnv, defaultProject), "GCP project holding Firestore (default $"+projectEnv+", else fitglue-server-prod)")
	fs.StringVar(&f.uid, "uid", os.Getenv(ownerUIDEnv), "owner's user id (default $"+ownerUIDEnv+")")
	fs.BoolVar(&f.json, "json", false, "print JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if f.uid == "" {
		return nil, errors.New("--uid is required (or set " + ownerUIDEnv + ")")
	}
	return f, nil
}

func connectionsCommand(ctx context.Context, args []string, stdout, stderr io.Writer, openStore storeOpener) error {
	f, err := parseConnectionsFlags(args, stderr)
	if err != nil {
		return err
	}
	store, cleanup, err := openStore(ctx, f.project)
	if err != nil {
		return err
	}
	defer cleanup()
	return runConnections(ctx, f, store, stdout)
}

// connection is one row of `fitgluectl connections`.
type connection struct {
	Provider    string `json:"provider"`
	Enabled     bool   `json:"enabled"`
	ConnectedAt string `json:"connected_at,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	HasKey      bool   `json:"has_credentials"`
}

// listConnections walks UserIntegrations by proto reflection so every
// provider message is covered without a per-provider switch. Secrets are
// never copied out: only whether a token/key is present.
func listConnections(integrations *pbuser.UserIntegrations) []connection {
	var rows []connection
	if integrations == nil {
		return rows
	}
	m := integrations.ProtoReflect()
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		im := v.Message()
		row := connection{Provider: string(fd.Name())}
		im.Range(func(ifd protoreflect.FieldDescriptor, iv protoreflect.Value) bool {
			switch name := string(ifd.Name()); {
			case name == "enabled":
				row.Enabled = iv.Bool()
			case name == "created_at":
				row.ConnectedAt = formatTS(iv)
			case name == "expires_at":
				row.ExpiresAt = formatTS(iv)
			case name == "access_token" || name == "api_key":
				row.HasKey = row.HasKey || iv.String() != ""
			case name == "athlete_id" || strings.HasSuffix(name, "_user_id") || name == "user_id":
				row.ExternalID = fmt.Sprint(iv.Interface())
			}
			return true
		})
		rows = append(rows, row)
		return true
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	return rows
}

func formatTS(v protoreflect.Value) string {
	ts, ok := v.Message().Interface().(*timestamppb.Timestamp)
	if !ok || ts == nil || (ts.Seconds == 0 && ts.Nanos == 0) {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

func runConnections(ctx context.Context, f *connectionsFlags, store integrationStore, stdout io.Writer) error {
	integrations, err := store.GetIntegrations(ctx, f.uid)
	if err != nil {
		return fmt.Errorf("listing integrations for %s: %w", f.uid, err)
	}
	rows := listConnections(integrations)
	if f.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "no integrations on users/%s\n", f.uid)
		return nil
	}
	fmt.Fprintf(stdout, "%-14s %-8s %-20s %-20s %-12s %s\n", "PROVIDER", "ENABLED", "CONNECTED", "TOKEN EXPIRES", "CREDENTIALS", "EXTERNAL ID")
	for _, r := range rows {
		fmt.Fprintf(stdout, "%-14s %-8t %-20s %-20s %-12s %s\n", r.Provider, r.Enabled, dash(r.ConnectedAt), dash(r.ExpiresAt), credentialsWord(r), dash(r.ExternalID))
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func credentialsWord(r connection) string {
	if r.HasKey {
		return "present"
	}
	return "missing"
}
