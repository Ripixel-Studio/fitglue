package googlehealth

// Sleep, body measurements and the food log (L0.1b).
//
// API facts this relies on (developers.google.com/health/reference/rest/v4,
// checked against the live API on 2026-10-29):
//   - Sleep is read with GET .../dataTypes/sleep/dataPoints:reconcile (one
//     stream from every source; sleep rejects an explicit dataSourceFamily on
//     list), filter sleep.interval.civil_end_time >= "YYYY-MM-DD", pageSize
//     default and max 25. A point is {sleep: {interval{startTime,
//     startUtcOffset, endTime, endUtcOffset}, type STAGES|CLASSIC, metadata
//     {mainSleep, nap, processed}, summary{minutesAsleep, minutesAwake,
//     minutesInSleepPeriod, stagesSummary[{type AWAKE|LIGHT|DEEP|REM, minutes}]}}};
//     minutes are int64 and so arrive as strings. There is no efficiency and
//     no sleep score field.
//   - Weight and body fat roll up per civil day with POST
//     .../dataPoints:dailyRollUp {range{start{date}, end{date}} (closed-open,
//     at most 90 days), windowSizeDays, pageSize (max 90), dataSourceFamily};
//     values are weight.weightGramsAvg and bodyFat.bodyFatPercentageAvg. A
//     day with no data has no rollup point.
//   - Daily resting HR and HRV are not roll-up types: GET .../dataPoints with
//     filter daily_resting_heart_rate.date >= "YYYY-MM-DD" (snake_case — a
//     hyphenated name in a filter is a 400); values
//     dailyRestingHeartRate.beatsPerMinute (int64 string) and
//     dailyHeartRateVariability.averageHeartRateVariabilityMilliseconds.
//   - The food log is nutrition-log: dailyRollUp gives nutritionLog.energy.kcalSum,
//     totalCarbohydrate.gramsSum, totalFat.gramsSum, nutrients[{nutrient
//     PROTEIN|CARBOHYDRATES|…, quantity.gramsSum}]; the entries themselves
//     are GET .../nutrition-log/dataPoints with filter
//     nutrition_log.interval.civil_start_time >= "YYYY-MM-DD", each
//     {interval, energy.kcal, totalCarbohydrate.grams, totalFat.grams,
//     nutrients[], mealType (optional), foodDisplayName}.
//   - dataSourceFamily all-sources (the default) is needed for anything a
//     third-party app writes through Health Connect: MacroFactor's food
//     (com.sbs.diet) and the Renpho scale (com.renpho.health) arrive with
//     platform HEALTH_CONNECT; google-wearables would drop them.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DataSourceAll is every source the user has, Health Connect apps included.
const DataSourceAll = "users/me/dataSourceFamilies/all-sources"

// MaxRollUpDays is the longest dailyRollUp range for the types read here.
const MaxRollUpDays = 90

// Num is an API number. int64 fields arrive as JSON strings, doubles as
// JSON numbers; Num reads either.
type Num float64

// UnmarshalJSON accepts 12, 12.5, "12" and "12.5"; null and "" are 0.
func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("googlehealth: not a number: %s", b)
	}
	*n = Num(f)
	return nil
}

// Date is a civil date.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf is the civil date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// String is YYYY-MM-DD.
func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// AddDays is d moved by n days.
func (d Date) AddDays(n int) Date {
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 12, 0, 0, 0, time.UTC))
}

// IsZero reports an unset date.
func (d Date) IsZero() bool { return d.Year == 0 }

type apiDate struct {
	Year  Num `json:"year"`
	Month Num `json:"month"`
	Day   Num `json:"day"`
}

func (a apiDate) date() Date { return Date{int(a.Year), time.Month(int(a.Month)), int(a.Day)} }

type apiCivil struct {
	Date apiDate `json:"date"`
}

// SleepSession is one sleep log.
type SleepSession struct {
	Start, End time.Time // in the offset Google recorded
	// Type is STAGES or CLASSIC.
	Type      string
	MainSleep bool
	Nap       bool
	// Minutes asleep and awake, and the whole sleep period.
	MinutesAsleep, MinutesAwake, MinutesInPeriod int
	// Stages maps AWAKE/LIGHT/DEEP/REM (or CLASSIC's ASLEEP/RESTLESS/AWAKE) to minutes.
	Stages map[string]int
}

// WakeDate is the civil date the session ended on.
func (s SleepSession) WakeDate() Date { return DateOf(s.End) }

type sleepList struct {
	DataPoints []struct {
		Sleep *struct {
			Interval struct {
				StartTime      string `json:"startTime"`
				StartUTCOffset string `json:"startUtcOffset"`
				EndTime        string `json:"endTime"`
				EndUTCOffset   string `json:"endUtcOffset"`
			} `json:"interval"`
			Type     string `json:"type"`
			Metadata struct {
				MainSleep bool `json:"mainSleep"`
				Nap       bool `json:"nap"`
			} `json:"metadata"`
			Summary struct {
				MinutesAsleep        Num `json:"minutesAsleep"`
				MinutesAwake         Num `json:"minutesAwake"`
				MinutesInSleepPeriod Num `json:"minutesInSleepPeriod"`
				StagesSummary        []struct {
					Type    string `json:"type"`
					Minutes Num    `json:"minutes"`
				} `json:"stagesSummary"`
			} `json:"summary"`
		} `json:"sleep"`
	} `json:"dataPoints"`
	NextPageToken string `json:"nextPageToken"`
}

// Sleep returns every sleep session (naps included) that ended on or after
// the civil date from, newest first as the API orders them.
func (c *Client) Sleep(ctx context.Context, from Date) ([]SleepSession, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`sleep.interval.civil_end_time >= "%s"`, from))
	q.Set("pageSize", "25")
	var out []SleepSession
	err := c.pages(ctx, "/v4/users/me/dataTypes/sleep/dataPoints:reconcile", q, func(body []byte) (string, error) {
		var resp sleepList
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		for _, dp := range resp.DataPoints {
			s := dp.Sleep
			if s == nil {
				continue
			}
			start, err1 := parseOffsetTime(s.Interval.StartTime, s.Interval.StartUTCOffset)
			end, err2 := parseOffsetTime(s.Interval.EndTime, s.Interval.EndUTCOffset)
			if err1 != nil || err2 != nil {
				continue
			}
			sess := SleepSession{
				Start: start, End: end, Type: s.Type,
				MainSleep: s.Metadata.MainSleep, Nap: s.Metadata.Nap,
				MinutesAsleep:   int(s.Summary.MinutesAsleep),
				MinutesAwake:    int(s.Summary.MinutesAwake),
				MinutesInPeriod: int(s.Summary.MinutesInSleepPeriod),
				Stages:          map[string]int{},
			}
			for _, st := range s.Summary.StagesSummary {
				sess.Stages[st.Type] += int(st.Minutes)
			}
			out = append(out, sess)
		}
		return resp.NextPageToken, nil
	})
	return out, err
}

// DailyValue is one value for one civil day.
type DailyValue struct {
	Date  Date
	Value float64
}

type rollUpRequest struct {
	Range struct {
		Start apiCivilOut `json:"start"`
		End   apiCivilOut `json:"end"`
	} `json:"range"`
	WindowSizeDays   int    `json:"windowSizeDays"`
	PageSize         int    `json:"pageSize"`
	PageToken        string `json:"pageToken,omitempty"`
	DataSourceFamily string `json:"dataSourceFamily"`
}

type apiCivilOut struct {
	Date struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"date"`
}

func civilOut(d Date) apiCivilOut {
	var c apiCivilOut
	c.Date.Year, c.Date.Month, c.Date.Day = d.Year, int(d.Month), d.Day
	return c
}

type rollUpResponse struct {
	RollupDataPoints []struct {
		CivilStartTime apiCivil `json:"civilStartTime"`
		Weight         *struct {
			WeightGramsAvg *Num `json:"weightGramsAvg"`
		} `json:"weight"`
		BodyFat *struct {
			BodyFatPercentageAvg *Num `json:"bodyFatPercentageAvg"`
		} `json:"bodyFat"`
		NutritionLog *struct {
			Energy *struct {
				KcalSum Num `json:"kcalSum"`
			} `json:"energy"`
			TotalCarbohydrate *struct {
				GramsSum Num `json:"gramsSum"`
			} `json:"totalCarbohydrate"`
			TotalFat *struct {
				GramsSum Num `json:"gramsSum"`
			} `json:"totalFat"`
			Nutrients []struct {
				Nutrient string `json:"nutrient"`
				Quantity struct {
					GramsSum Num `json:"gramsSum"`
				} `json:"quantity"`
			} `json:"nutrients"`
		} `json:"nutritionLog"`
	} `json:"rollupDataPoints"`
	NextPageToken string `json:"nextPageToken"`
}

// rollUp runs dailyRollUp for dataType over the civil days [from, to] (to
// inclusive), all sources, one-day windows, following nextPageToken.
func (c *Client) rollUp(ctx context.Context, dataType string, from, to Date, each func(rollUpResponse)) error {
	if days := daysBetween(from, to) + 1; days > MaxRollUpDays || days < 1 {
		return fmt.Errorf("googlehealth %s rollup: %d days (1..%d)", dataType, days, MaxRollUpDays)
	}
	req := rollUpRequest{WindowSizeDays: 1, PageSize: MaxRollUpDays, DataSourceFamily: DataSourceAll}
	req.Range.Start = civilOut(from)
	req.Range.End = civilOut(to.AddDays(1))
	for page := 0; ; page++ {
		if page == maxPages {
			return fmt.Errorf("googlehealth %s rollup: more than %d pages", dataType, maxPages)
		}
		var resp rollUpResponse
		if err := c.post(ctx, "/v4/users/me/dataTypes/"+dataType+"/dataPoints:dailyRollUp", req, &resp); err != nil {
			return err
		}
		each(resp)
		if resp.NextPageToken == "" {
			return nil
		}
		req.PageToken = resp.NextPageToken
	}
}

// DailyWeightKg is the day's average weight in kg for each civil day in
// [from, to] that has one, all sources.
func (c *Client) DailyWeightKg(ctx context.Context, from, to Date) ([]DailyValue, error) {
	var out []DailyValue
	err := c.rollUp(ctx, "weight", from, to, func(r rollUpResponse) {
		for _, p := range r.RollupDataPoints {
			if p.Weight != nil && p.Weight.WeightGramsAvg != nil && *p.Weight.WeightGramsAvg > 0 {
				out = append(out, DailyValue{p.CivilStartTime.Date.date(), float64(*p.Weight.WeightGramsAvg) / 1000})
			}
		}
	})
	return out, err
}

// DailyBodyFatPct is the day's average body-fat percentage for each civil day
// in [from, to] that has one, all sources.
func (c *Client) DailyBodyFatPct(ctx context.Context, from, to Date) ([]DailyValue, error) {
	var out []DailyValue
	err := c.rollUp(ctx, "body-fat", from, to, func(r rollUpResponse) {
		for _, p := range r.RollupDataPoints {
			if p.BodyFat != nil && p.BodyFat.BodyFatPercentageAvg != nil && *p.BodyFat.BodyFatPercentageAvg > 0 {
				out = append(out, DailyValue{p.CivilStartTime.Date.date(), float64(*p.BodyFat.BodyFatPercentageAvg)})
			}
		}
	})
	return out, err
}

// NutritionDay is one civil day's food-log totals.
type NutritionDay struct {
	Date                    Date
	Kcal                    float64
	ProteinG, CarbsG, FatG  float64
	HasProtein, HasCarbsFat bool
}

// DailyNutrition is the food log rolled up per civil day in [from, to], all
// sources; days with nothing logged are absent.
func (c *Client) DailyNutrition(ctx context.Context, from, to Date) ([]NutritionDay, error) {
	var out []NutritionDay
	err := c.rollUp(ctx, "nutrition-log", from, to, func(r rollUpResponse) {
		for _, p := range r.RollupDataPoints {
			n := p.NutritionLog
			if n == nil {
				continue
			}
			day := NutritionDay{Date: p.CivilStartTime.Date.date()}
			if n.Energy != nil {
				day.Kcal = float64(n.Energy.KcalSum)
			}
			if n.TotalCarbohydrate != nil {
				day.CarbsG, day.HasCarbsFat = float64(n.TotalCarbohydrate.GramsSum), true
			}
			if n.TotalFat != nil {
				day.FatG, day.HasCarbsFat = float64(n.TotalFat.GramsSum), true
			}
			for _, nu := range n.Nutrients {
				switch nu.Nutrient {
				case "PROTEIN":
					day.ProteinG, day.HasProtein = float64(nu.Quantity.GramsSum), true
				case "CARBOHYDRATES":
					if n.TotalCarbohydrate == nil {
						day.CarbsG = float64(nu.Quantity.GramsSum)
					}
				}
			}
			out = append(out, day)
		}
	})
	return out, err
}

// FoodEntry is one nutrition-log entry.
type FoodEntry struct {
	Start time.Time // in the offset Google recorded
	// Date is the entry's civil start date.
	Date Date
	// MealType is BREAKFAST, LUNCH, DINNER, SNACK, … or "" when the app did not say.
	MealType string
	Name     string
	Kcal     float64
	ProteinG float64
	CarbsG   float64
	FatG     float64
	// Source is the writing app's package name (com.sbs.diet is MacroFactor),
	// else the platform.
	Source string
}

type dataSource struct {
	Platform    string `json:"platform"`
	Application struct {
		PackageName string `json:"packageName"`
	} `json:"application"`
}

func (d dataSource) label() string {
	if d.Application.PackageName != "" {
		return d.Application.PackageName
	}
	return d.Platform
}

type grams struct {
	Grams Num `json:"grams"`
}

type foodList struct {
	DataPoints []struct {
		DataSource   dataSource `json:"dataSource"`
		NutritionLog *struct {
			Interval struct {
				StartTime      string   `json:"startTime"`
				StartUTCOffset string   `json:"startUtcOffset"`
				CivilStartTime apiCivil `json:"civilStartTime"`
			} `json:"interval"`
			Energy *struct {
				Kcal Num `json:"kcal"`
			} `json:"energy"`
			TotalCarbohydrate *grams `json:"totalCarbohydrate"`
			TotalFat          *grams `json:"totalFat"`
			Nutrients         []struct {
				Nutrient string `json:"nutrient"`
				Quantity grams  `json:"quantity"`
			} `json:"nutrients"`
			MealType        string `json:"mealType"`
			FoodDisplayName string `json:"foodDisplayName"`
		} `json:"nutritionLog"`
	} `json:"dataPoints"`
	NextPageToken string `json:"nextPageToken"`
}

// FoodLog returns the nutrition-log entries whose civil start date is on or
// after from, all sources, newest first as the API orders them.
func (c *Client) FoodLog(ctx context.Context, from Date) ([]FoodEntry, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`nutrition_log.interval.civil_start_time >= "%s"`, from))
	q.Set("pageSize", "1000")
	q.Set("dataSourceFamily", DataSourceAll)
	var out []FoodEntry
	err := c.pages(ctx, "/v4/users/me/dataTypes/nutrition-log/dataPoints", q, func(body []byte) (string, error) {
		var resp foodList
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		for _, dp := range resp.DataPoints {
			n := dp.NutritionLog
			if n == nil {
				continue
			}
			start, err := parseOffsetTime(n.Interval.StartTime, n.Interval.StartUTCOffset)
			if err != nil {
				continue
			}
			e := FoodEntry{
				Start: start, Date: n.Interval.CivilStartTime.Date.date(),
				MealType: n.MealType, Name: n.FoodDisplayName, Source: dp.DataSource.label(),
			}
			if e.Date.IsZero() {
				e.Date = DateOf(start)
			}
			if n.Energy != nil {
				e.Kcal = float64(n.Energy.Kcal)
			}
			if n.TotalCarbohydrate != nil {
				e.CarbsG = float64(n.TotalCarbohydrate.Grams)
			}
			if n.TotalFat != nil {
				e.FatG = float64(n.TotalFat.Grams)
			}
			for _, nu := range n.Nutrients {
				switch nu.Nutrient {
				case "PROTEIN":
					e.ProteinG = float64(nu.Quantity.Grams)
				case "CARBOHYDRATES":
					if n.TotalCarbohydrate == nil {
						e.CarbsG = float64(nu.Quantity.Grams)
					}
				}
			}
			out = append(out, e)
		}
		return resp.NextPageToken, nil
	})
	return out, err
}

type dailyList struct {
	DataPoints []struct {
		DailyRestingHeartRate *struct {
			Date           apiDate `json:"date"`
			BeatsPerMinute Num     `json:"beatsPerMinute"`
		} `json:"dailyRestingHeartRate"`
		DailyHeartRateVariability *struct {
			Date                                    apiDate `json:"date"`
			AverageHeartRateVariabilityMilliseconds Num     `json:"averageHeartRateVariabilityMilliseconds"`
		} `json:"dailyHeartRateVariability"`
	} `json:"dataPoints"`
	NextPageToken string `json:"nextPageToken"`
}

// DailyRestingHR is the daily resting heart rate (bpm) for each civil day on
// or after from, all sources; one value per day (the first the API returns).
func (c *Client) DailyRestingHR(ctx context.Context, from Date) ([]DailyValue, error) {
	return c.daily(ctx, "daily-resting-heart-rate", "daily_resting_heart_rate", from)
}

// DailyHRV is the daily average heart-rate variability (ms) for each civil
// day on or after from, all sources; one value per day.
func (c *Client) DailyHRV(ctx context.Context, from Date) ([]DailyValue, error) {
	return c.daily(ctx, "daily-heart-rate-variability", "daily_heart_rate_variability", from)
}

func (c *Client) daily(ctx context.Context, dataType, filterName string, from Date) ([]DailyValue, error) {
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`%s.date >= "%s"`, filterName, from))
	q.Set("pageSize", "1000")
	q.Set("dataSourceFamily", DataSourceAll)
	seen := map[Date]bool{}
	var out []DailyValue
	err := c.pages(ctx, "/v4/users/me/dataTypes/"+dataType+"/dataPoints", q, func(body []byte) (string, error) {
		var resp dailyList
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		for _, dp := range resp.DataPoints {
			var v DailyValue
			switch {
			case dp.DailyRestingHeartRate != nil:
				v = DailyValue{dp.DailyRestingHeartRate.Date.date(), float64(dp.DailyRestingHeartRate.BeatsPerMinute)}
			case dp.DailyHeartRateVariability != nil:
				v = DailyValue{dp.DailyHeartRateVariability.Date.date(), float64(dp.DailyHeartRateVariability.AverageHeartRateVariabilityMilliseconds)}
			default:
				continue
			}
			if v.Value <= 0 || v.Date.IsZero() || seen[v.Date] {
				continue
			}
			seen[v.Date] = true
			out = append(out, v)
		}
		return resp.NextPageToken, nil
	})
	return out, err
}

// pages GETs path with q, hands each body to page, and follows the token it
// returns until it is empty.
func (c *Client) pages(ctx context.Context, path string, q url.Values, page func([]byte) (string, error)) error {
	for n := 0; ; n++ {
		if n == maxPages {
			return fmt.Errorf("googlehealth %s: more than %d pages", path, maxPages)
		}
		var raw json.RawMessage
		if err := c.get(ctx, path, q, &raw); err != nil {
			return err
		}
		next, err := page(raw)
		if err != nil {
			return fmt.Errorf("googlehealth %s: decoding response: %w", path, err)
		}
		if next == "" {
			return nil
		}
		q.Set("pageToken", next)
	}
}

func (c *Client) post(ctx context.Context, path string, in, out interface{}) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
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

// parseOffsetTime reads an RFC 3339 instant and places it in the fixed zone
// of a protobuf Duration offset ("3600s"), so it formats with the user's
// local offset.
func parseOffsetTime(ts, offset string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, err
	}
	off := parseDurationSeconds(offset)
	return t.In(time.FixedZone("", int(off/time.Second))), nil
}

func daysBetween(a, b Date) int {
	ta := time.Date(a.Year, a.Month, a.Day, 12, 0, 0, 0, time.UTC)
	tb := time.Date(b.Year, b.Month, b.Day, 12, 0, 0, 0, time.UTC)
	return int(tb.Sub(ta).Hours() / 24)
}

// SourceFilters is the civil-time filter field per data type that Sources
// can census.
var SourceFilters = map[string]string{
	"weight":        "weight.sample_time.civil_time",
	"body-fat":      "body_fat.sample_time.civil_time",
	"nutrition-log": "nutrition_log.interval.civil_start_time",
}

// Sources counts the raw data points of dataType on or after from by the
// app that wrote them (package name, else platform), all sources — which
// apps feed Google Health, without reading a single value.
func (c *Client) Sources(ctx context.Context, dataType string, from Date) (map[string]int, error) {
	field, ok := SourceFilters[dataType]
	if !ok {
		return nil, fmt.Errorf("googlehealth: no source census for %s", dataType)
	}
	q := url.Values{}
	q.Set("filter", fmt.Sprintf(`%s >= "%s"`, field, from))
	q.Set("pageSize", "1000")
	q.Set("dataSourceFamily", DataSourceAll)
	out := map[string]int{}
	err := c.pages(ctx, "/v4/users/me/dataTypes/"+dataType+"/dataPoints", q, func(body []byte) (string, error) {
		var resp struct {
			DataPoints []struct {
				DataSource dataSource `json:"dataSource"`
			} `json:"dataPoints"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		for _, dp := range resp.DataPoints {
			out[dp.DataSource.label()]++
		}
		return resp.NextPageToken, nil
	})
	return out, err
}
