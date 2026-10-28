package calories

import (
	"testing"

	pbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"
)

func TestPlatform(t *testing.T) {
	a, b := 600.0, 532.0
	act := &pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{TotalCalories: &a}, {}, {TotalCalories: &b}}}
	if kcal, ok := Platform(act); !ok || kcal != 1132 {
		t.Errorf("Platform = %v %v, want 1132 true", kcal, ok)
	}
	zero := 0.0
	if _, ok := Platform(&pbactivity.StandardizedActivity{Sessions: []*pbactivity.Session{{TotalCalories: &zero}}}); ok {
		t.Error("a zero total is not a platform figure")
	}
	if _, ok := Platform(nil); ok {
		t.Error("nil activity")
	}
}

func TestComparison(t *testing.T) {
	cases := map[float64]string{
		1346: "≈ 4.7 slice of pizza 🍕",
		40:   "",
		300:  "≈ 1.1 slice of pizza 🍕",
	}
	for kcal, want := range cases {
		if got := Comparison(kcal); got != want {
			t.Errorf("Comparison(%v) = %q, want %q", kcal, got, want)
		}
	}
	if FoodFor(100000).Name != "slice of pizza" {
		t.Error("default is pizza")
	}
}
