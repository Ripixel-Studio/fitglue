// Package googlehealth is a small hand-written client for the Google Health
// API (https://health.googleapis.com/v4), the replacement for the Fitbit Web
// API, which Google turns off on 2 Nov 2026.
//
// Only what FitGlue reads is here: the caller's identity (proof the Google
// account is linked to the Google Health app) and heart-rate samples for a
// time range. Authentication is the caller's business: pass an *http.Client
// whose transport adds the OAuth bearer (pkg/infrastructure/oauth with
// provider "googlehealth").
//
// API facts this relies on (developers.google.com/health/reference/rest/v4,
// checked 2026-10-28):
//   - GET /v4/users/me/identity → {name, legacyUserId, healthUserId}.
//   - GET /v4/users/me/dataTypes/heart-rate/dataPoints lists samples; the
//     time range is an AIP-160 filter on heart_rate.sample_time.physical_time
//     (RFC 3339, >= and <; hyphenated type names in a filter are a 400);
//     pageSize defaults to 1440 and is capped at 10000; results are ordered
//     newest first; nextPageToken is empty on the last page.
//   - A heart-rate point is {heartRate: {sampleTime: {physicalTime, utcOffset,
//     civilTime}, beatsPerMinute}}; beatsPerMinute is an int64 and so arrives
//     as a JSON string.
//   - dataSourceFamily users/me/dataSourceFamilies/google-wearables is "data
//     from Google and Fitbit tracker devices", excluding manual entries —
//     what the Fitbit intraday endpoint used to return.
//   - Errors are the standard Google JSON error; a Google account not linked
//     to the Google Health app answers 400 with reason ACCOUNT_NOT_LINKED, a
//     grant without the scope 403 MISSING_OAUTH_SCOPE.
package googlehealth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BaseURL is the Google Health API origin.
const BaseURL = "https://health.googleapis.com"

// DataSourceWearables restricts reads to Google and Fitbit tracker devices.
const DataSourceWearables = "users/me/dataSourceFamilies/google-wearables"

// maxPageSize is the API's cap for dataPoints.list on sample types.
const maxPageSize = 10000

// maxPages bounds pagination: a 24 h activity at 1 s resolution is 86 400
// samples, nine pages. Anything beyond this is a bug, not data.
const maxPages = 50

// Client reads the Google Health API as the user whose token the HTTP
// client carries.
type Client struct {
	base string
	http *http.Client
}

// New returns a client against BaseURL. httpClient must authenticate.
func New(httpClient *http.Client) *Client {
	return NewWithBaseURL(BaseURL, httpClient)
}

// NewWithBaseURL is New against another origin (tests).
func NewWithBaseURL(base string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{base: strings.TrimRight(base, "/"), http: httpClient}
}

// APIError is a non-2xx answer from the API.
type APIError struct {
	StatusCode int
	// Status is the google.rpc code name, e.g. FAILED_PRECONDITION.
	Status string
	// Reason is the ErrorInfo reason when present, e.g. ACCOUNT_NOT_LINKED.
	Reason  string
	Message string
	// RetryAfter is the Retry-After header in seconds form, 0 when absent.
	RetryAfter time.Duration
	Body       string
}

func (e *APIError) Error() string {
	label := e.Reason
	if label == "" {
		label = e.Status
	}
	if label == "" {
		label = http.StatusText(e.StatusCode)
	}
	msg := e.Message
	if msg == "" {
		msg = e.Body
	}
	return fmt.Sprintf("google health api %d %s: %s", e.StatusCode, label, msg)
}

// Transient reports whether the failure is worth retrying later: rate
// limiting and server errors.
func (e *APIError) Transient() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// Identity is GET /v4/users/me/identity.
type Identity struct {
	Name         string `json:"name"`
	LegacyUserID string `json:"legacyUserId"`
	HealthUserID string `json:"healthUserId"`
}

// Identity returns the caller's Google Health identity. It is the cheapest
// call that proves the token works and the Google account is linked to the
// Google Health app (else an APIError with Reason ACCOUNT_NOT_LINKED).
func (c *Client) Identity(ctx context.Context) (*Identity, error) {
	var out Identity
	if err := c.get(ctx, "/v4/users/me/identity", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HeartRateSample is one heart-rate observation.
type HeartRateSample struct {
	// Time is the physical (UTC) instant of the observation.
	Time time.Time
	BPM  int
	// UTCOffset is the user's local offset at the time, as Google reports it.
	UTCOffset time.Duration
}

type heartRateList struct {
	DataPoints []struct {
		HeartRate *struct {
			SampleTime struct {
				PhysicalTime string `json:"physicalTime"`
				UTCOffset    string `json:"utcOffset"`
			} `json:"sampleTime"`
			BeatsPerMinute json.Number `json:"beatsPerMinute"`
		} `json:"heartRate"`
	} `json:"dataPoints"`
	NextPageToken string `json:"nextPageToken"`
}

// HeartRate returns the wearable heart-rate samples in [start, end), oldest
// first, one per second (the API's storage resolution; a duplicate second
// keeps the first value seen).
func (c *Client) HeartRate(ctx context.Context, start, end time.Time) ([]HeartRateSample, error) {
	filter := fmt.Sprintf(`heart_rate.sample_time.physical_time >= "%s" AND heart_rate.sample_time.physical_time < "%s"`,
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	q := url.Values{}
	q.Set("filter", filter)
	q.Set("pageSize", strconv.Itoa(maxPageSize))
	q.Set("dataSourceFamily", DataSourceWearables)

	seen := map[int64]bool{}
	var out []HeartRateSample
	for page := 0; ; page++ {
		if page == maxPages {
			return nil, fmt.Errorf("google health heart-rate: more than %d pages for %s..%s", maxPages, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
		}
		var resp heartRateList
		if err := c.get(ctx, "/v4/users/me/dataTypes/heart-rate/dataPoints", q, &resp); err != nil {
			return nil, err
		}
		for _, dp := range resp.DataPoints {
			if dp.HeartRate == nil {
				continue
			}
			t, err := time.Parse(time.RFC3339Nano, dp.HeartRate.SampleTime.PhysicalTime)
			if err != nil {
				continue
			}
			bpm, err := strconv.Atoi(dp.HeartRate.BeatsPerMinute.String())
			if err != nil || bpm <= 0 {
				continue
			}
			sec := t.Unix()
			if seen[sec] {
				continue
			}
			seen[sec] = true
			out = append(out, HeartRateSample{
				Time:      t.UTC(),
				BPM:       bpm,
				UTCOffset: parseDurationSeconds(dp.HeartRate.SampleTime.UTCOffset),
			})
		}
		if resp.NextPageToken == "" {
			break
		}
		q.Set("pageToken", resp.NextPageToken)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out, nil
}

// parseDurationSeconds reads a protobuf Duration in JSON form ("3600s",
// "-18000s", "0s"); anything else is 0.
func parseDurationSeconds(s string) time.Duration {
	if !strings.HasSuffix(s, "s") {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
	if err != nil {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out interface{}) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseAPIError(resp, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("google health %s: decoding response: %w", path, err)
	}
	return nil
}

func parseAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	if len(e.Body) > 2000 {
		e.Body = e.Body[:2000]
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			e.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	var g struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &g) == nil {
		e.Message = g.Error.Message
		e.Status = g.Error.Status
		for _, d := range g.Error.Details {
			if d.Reason != "" {
				e.Reason = d.Reason
				break
			}
		}
	}
	// Some errors carry the reason only in the message text.
	if e.Reason == "" && strings.Contains(e.Body, "ACCOUNT_NOT_LINKED") {
		e.Reason = "ACCOUNT_NOT_LINKED"
	}
	return e
}
