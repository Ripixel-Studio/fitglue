// Package health serves the owner's sleep, body measurements and food log
// from the Google Health API (L0.1b) in the shapes Jarvis reads:
//
//	GET /api/v2/users/me/sleep?days=7       {nights[...]}          (J9.4, docs/home-screen.md in jarvis)
//	GET /api/v2/users/me/body?days=30       {weights, body_fat, resting_hr, hrv}
//	GET /api/v2/users/me/nutrition?days=7   {days[{date, kcal, protein_g, carbs_g, fat_g, meals[]}]}
//
// FitGlue holds the one googlehealth grant; Jarvis never calls Google
// itself. A grant that is missing, disabled, without the data type's scope
// or not linked to the Google Health app is ErrNotConnected, which the
// api-client answers 404 ("not connected" to Jarvis).
package health

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fitglue/server/src/go/pkg/integrations/googlehealth"
)

// Kind names one route and the scope it needs.
type Kind string

const (
	KindSleep     Kind = "sleep"
	KindBody      Kind = "body"
	KindNutrition Kind = "nutrition"
)

// Scope is the read-only Google scope each kind needs.
func (k Kind) Scope() string {
	switch k {
	case KindSleep:
		return "https://www.googleapis.com/auth/googlehealth.sleep.readonly"
	case KindBody:
		return "https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly"
	case KindNutrition:
		return "https://www.googleapis.com/auth/googlehealth.nutrition.readonly"
	}
	return ""
}

// DefaultDays is the window a route uses when ?days is absent.
func (k Kind) DefaultDays() int {
	if k == KindBody {
		return 30
	}
	return 7
}

// MaxDays bounds ?days: the dailyRollUp range limit.
const MaxDays = googlehealth.MaxRollUpDays

// ErrNotConnected is a grant that cannot read the kind: absent, disabled,
// missing the scope, or a Google account not linked to the Google Health app.
var ErrNotConnected = errors.New("google health not connected")

// Grant is the owner's googlehealth integration as the API sees it.
type Grant struct {
	// Scope is the space-separated scopes Google granted ("" when unknown:
	// the API is then left to refuse).
	Scope string
	// HTTP authenticates as the owner (OAuth bearer, refreshing).
	HTTP *http.Client
}

// Has reports whether the grant carries scope (or is unknown).
func (g *Grant) Has(scope string) bool {
	if strings.TrimSpace(g.Scope) == "" {
		return true
	}
	for _, s := range strings.Fields(g.Scope) {
		if s == scope || s == strings.TrimSuffix(scope, ".readonly") {
			return true
		}
	}
	return false
}

// GrantSource returns the current grant, or an error wrapping
// ErrNotConnected when there is none.
type GrantSource func(ctx context.Context) (*Grant, error)

// Reader builds the route responses. It is safe for concurrent use.
type Reader struct {
	grant GrantSource
	// BaseURL is the Google Health origin (tests).
	BaseURL string
	// Now and Location fix "today": the civil day is the box's.
	Now      func() time.Time
	Location *time.Location
	// TTL is how long a successful answer is reused.
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at time.Time
	v  interface{}
}

// DefaultTTL keeps an answer for five minutes: Jarvis polls every 30, and a
// reload of the page or a second card should not cost Google calls.
const DefaultTTL = 5 * time.Minute

// NewReader returns a Reader over grant with the defaults.
func NewReader(grant GrantSource) *Reader {
	return &Reader{grant: grant, BaseURL: googlehealth.BaseURL, Now: time.Now, Location: time.Local, TTL: DefaultTTL, cache: map[string]cached{}}
}

// Read returns kind's response for the last days civil days (today
// included), from the cache when fresh.
func (r *Reader) Read(ctx context.Context, kind Kind, days int) (interface{}, error) {
	if days < 1 || days > MaxDays {
		return nil, fmt.Errorf("days must be 1..%d", MaxDays)
	}
	key := fmt.Sprintf("%s/%d", kind, days)
	now := r.Now()
	r.mu.Lock()
	if c, ok := r.cache[key]; ok && now.Sub(c.at) < r.TTL {
		r.mu.Unlock()
		return c.v, nil
	}
	r.mu.Unlock()

	g, err := r.grant(ctx)
	if err != nil {
		return nil, err
	}
	if !g.Has(kind.Scope()) {
		return nil, fmt.Errorf("%w: the grant lacks %s", ErrNotConnected, kind.Scope())
	}
	client := googlehealth.NewWithBaseURL(r.BaseURL, g.HTTP)
	today := googlehealth.DateOf(now.In(r.Location))
	from := today.AddDays(-(days - 1))

	var v interface{}
	switch kind {
	case KindSleep:
		v, err = readSleep(ctx, client, from)
	case KindBody:
		v, err = readBody(ctx, client, from, today)
	case KindNutrition:
		v, err = readNutrition(ctx, client, from, today)
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	if err != nil {
		return nil, classify(err)
	}
	r.mu.Lock()
	r.cache[key] = cached{at: now, v: v}
	r.mu.Unlock()
	return v, nil
}

// classify turns the API's "this grant cannot read that" answers into
// ErrNotConnected; everything else is an upstream failure.
func classify(err error) error {
	var apiErr *googlehealth.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusForbidden || apiErr.Reason == "ACCOUNT_NOT_LINKED" ||
			apiErr.Reason == "MISSING_OAUTH_SCOPE" || apiErr.Status == "PERMISSION_DENIED" {
			return fmt.Errorf("%w: %v", ErrNotConnected, err)
		}
	}
	return err
}

// ---- sleep (J9.4 shape) ----

// SleepResponse is GET /users/me/sleep.
type SleepResponse struct {
	Nights []Night `json:"nights"`
}

// Night is one sleep log (naps included; Jarvis keeps the longest per date).
type Night struct {
	// Date is the wake date (the civil end date).
	Date          string `json:"date"`
	Start         string `json:"start"`
	End           string `json:"end"`
	MinutesAsleep int    `json:"minutes_asleep"`
	// Efficiency is minutes asleep over the sleep period, 0–100.
	Efficiency int `json:"efficiency"`
	// Stages in minutes, for stage-tracked sleep only.
	Stages *Stages `json:"stages,omitempty"`
	// Nap and MainSleep are Google's flags (extra keys; Jarvis ignores them).
	Nap       bool `json:"nap,omitempty"`
	MainSleep bool `json:"main_sleep,omitempty"`
}

// Stages are minutes per sleep stage.
type Stages struct {
	Deep  int `json:"deep"`
	Light int `json:"light"`
	REM   int `json:"rem"`
	Wake  int `json:"wake"`
}

func readSleep(ctx context.Context, c *googlehealth.Client, from googlehealth.Date) (*SleepResponse, error) {
	sessions, err := c.Sleep(ctx, from)
	if err != nil {
		return nil, err
	}
	out := &SleepResponse{Nights: []Night{}}
	for _, s := range sessions {
		n := Night{
			Date:          s.WakeDate().String(),
			Start:         s.Start.Format(time.RFC3339),
			End:           s.End.Format(time.RFC3339),
			MinutesAsleep: s.MinutesAsleep,
			Nap:           s.Nap,
			MainSleep:     s.MainSleep,
		}
		period := s.MinutesInPeriod
		if period <= 0 {
			period = s.MinutesAsleep + s.MinutesAwake
		}
		if period > 0 {
			n.Efficiency = int(math.Round(100 * float64(s.MinutesAsleep) / float64(period)))
		}
		if s.Type == "STAGES" {
			n.Stages = &Stages{Deep: s.Stages["DEEP"], Light: s.Stages["LIGHT"], REM: s.Stages["REM"], Wake: s.Stages["AWAKE"]}
		}
		out.Nights = append(out.Nights, n)
	}
	sort.SliceStable(out.Nights, func(i, j int) bool { return out.Nights[i].End < out.Nights[j].End })
	return out, nil
}

// ---- body ----

// BodyResponse is GET /users/me/body. Each list is oldest first, one entry
// per civil day that has a value.
type BodyResponse struct {
	Weights   []Weight  `json:"weights"`
	BodyFat   []BodyFat `json:"body_fat"`
	RestingHR []Resting `json:"resting_hr"`
	HRV       []HRV     `json:"hrv"`
}

type Weight struct {
	Date string  `json:"date"`
	Kg   float64 `json:"kg"`
}

type BodyFat struct {
	Date string  `json:"date"`
	Pct  float64 `json:"pct"`
}

type Resting struct {
	Date string `json:"date"`
	BPM  int    `json:"bpm"`
}

type HRV struct {
	Date string  `json:"date"`
	Ms   float64 `json:"ms"`
}

func readBody(ctx context.Context, c *googlehealth.Client, from, to googlehealth.Date) (*BodyResponse, error) {
	out := &BodyResponse{Weights: []Weight{}, BodyFat: []BodyFat{}, RestingHR: []Resting{}, HRV: []HRV{}}
	w, err := c.DailyWeightKg(ctx, from, to)
	if err != nil {
		return nil, err
	}
	for _, v := range sortDaily(w, from, to) {
		out.Weights = append(out.Weights, Weight{v.Date.String(), round(v.Value, 2)})
	}
	bf, err := c.DailyBodyFatPct(ctx, from, to)
	if err != nil {
		return nil, err
	}
	for _, v := range sortDaily(bf, from, to) {
		out.BodyFat = append(out.BodyFat, BodyFat{v.Date.String(), round(v.Value, 1)})
	}
	rhr, err := c.DailyRestingHR(ctx, from)
	if err != nil {
		return nil, err
	}
	for _, v := range sortDaily(rhr, from, to) {
		out.RestingHR = append(out.RestingHR, Resting{v.Date.String(), int(math.Round(v.Value))})
	}
	hrv, err := c.DailyHRV(ctx, from)
	if err != nil {
		return nil, err
	}
	for _, v := range sortDaily(hrv, from, to) {
		out.HRV = append(out.HRV, HRV{v.Date.String(), round(v.Value, 1)})
	}
	return out, nil
}

// sortDaily keeps the values inside [from, to], one per date, oldest first.
func sortDaily(vs []googlehealth.DailyValue, from, to googlehealth.Date) []googlehealth.DailyValue {
	lo, hi := from.String(), to.String()
	seen := map[string]bool{}
	var out []googlehealth.DailyValue
	for _, v := range vs {
		d := v.Date.String()
		if d < lo || d > hi || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.String() < out[j].Date.String() })
	return out
}

// ---- nutrition ----

// NutritionResponse is GET /users/me/nutrition: one entry per civil day with
// anything logged, oldest first.
type NutritionResponse struct {
	Days []NutritionDay `json:"days"`
}

type NutritionDay struct {
	Date     string  `json:"date"`
	Kcal     int     `json:"kcal"`
	ProteinG float64 `json:"protein_g"`
	CarbsG   float64 `json:"carbs_g"`
	FatG     float64 `json:"fat_g"`
	Meals    []Meal  `json:"meals"`
}

// Meal is one food-log entry.
type Meal struct {
	// Type is breakfast/lunch/dinner/snack…, or null when the app did not
	// say (MacroFactor does not).
	Type *string `json:"type"`
	Name string  `json:"name"`
	Kcal int     `json:"kcal"`
	// At is when it was logged as eaten, ISO 8601 with offset.
	At string `json:"at"`
}

func readNutrition(ctx context.Context, c *googlehealth.Client, from, to googlehealth.Date) (*NutritionResponse, error) {
	totals, err := c.DailyNutrition(ctx, from, to)
	if err != nil {
		return nil, err
	}
	entries, err := c.FoodLog(ctx, from)
	if err != nil {
		return nil, err
	}
	lo, hi := from.String(), to.String()
	days := map[string]*NutritionDay{}
	day := func(d string) *NutritionDay {
		if days[d] == nil {
			days[d] = &NutritionDay{Date: d, Meals: []Meal{}}
		}
		return days[d]
	}
	// Totals: Google's roll-up, else the entries summed (below).
	rolled := map[string]bool{}
	for _, t := range totals {
		d := t.Date.String()
		if d < lo || d > hi || (t.Kcal == 0 && !t.HasProtein && !t.HasCarbsFat) {
			continue
		}
		nd := day(d)
		nd.Kcal = int(math.Round(t.Kcal))
		nd.ProteinG, nd.CarbsG, nd.FatG = round(t.ProteinG, 1), round(t.CarbsG, 1), round(t.FatG, 1)
		rolled[d] = true
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Start.Before(entries[j].Start) })
	sums := map[string]*googlehealth.NutritionDay{}
	for _, e := range entries {
		d := e.Date.String()
		if d < lo || d > hi {
			continue
		}
		nd := day(d)
		m := Meal{Name: e.Name, Kcal: int(math.Round(e.Kcal)), At: e.Start.Format(time.RFC3339)}
		if t := mealType(e.MealType); t != "" {
			m.Type = &t
		}
		nd.Meals = append(nd.Meals, m)
		if sums[d] == nil {
			sums[d] = &googlehealth.NutritionDay{}
		}
		s := sums[d]
		s.Kcal += e.Kcal
		s.ProteinG += e.ProteinG
		s.CarbsG += e.CarbsG
		s.FatG += e.FatG
	}
	for d, s := range sums {
		if rolled[d] {
			continue
		}
		nd := days[d]
		nd.Kcal = int(math.Round(s.Kcal))
		nd.ProteinG, nd.CarbsG, nd.FatG = round(s.ProteinG, 1), round(s.CarbsG, 1), round(s.FatG, 1)
	}
	out := &NutritionResponse{Days: []NutritionDay{}}
	for _, nd := range days {
		out.Days = append(out.Days, *nd)
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date < out.Days[j].Date })
	return out, nil
}

func mealType(t string) string {
	switch t {
	case "", "MEAL_TYPE_UNSPECIFIED":
		return ""
	}
	return strings.ToLower(t)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
