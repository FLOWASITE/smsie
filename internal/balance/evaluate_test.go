package balance

import "testing"

func ptr(v int64) *int64 { return &v }

var cfg = Config{LowThresholdVND: 20000, ForecastDays: 7}

func TestEvaluateUnknownWithoutBalance(t *testing.T) {
	if r := Evaluate(Input{HasBalance: false}, cfg); r.Level != LevelUnknown {
		t.Fatalf("got %+v", r)
	}
}

func TestEvaluateSimThresholdOverridesConfig(t *testing.T) {
	r := Evaluate(Input{HasBalance: true, VND: 25000, SimThreshold: ptr(30000)}, cfg)
	if r.Level != LevelLow || r.ThresholdSource != "sim" || r.ThresholdVND != 30000 {
		t.Fatalf("got %+v", r)
	}
}

func TestEvaluateForecastWithinDays(t *testing.T) {
	r := Evaluate(Input{HasBalance: true, VND: 100000, Points: pts(140000, 120000, 100000)}, cfg)
	if r.Level != LevelForecast || r.DaysLeft == nil || !near(*r.DaysLeft, 5) || r.ThresholdSource != "config" || r.ThresholdVND != 20000 {
		t.Fatalf("got %+v", r)
	}
}

func TestEvaluateOK(t *testing.T) {
	r := Evaluate(Input{HasBalance: true, VND: 100000, Points: pts(80000, 90000, 100000)}, cfg)
	if r.Level != LevelOK || r.DaysLeft != nil {
		t.Fatalf("got %+v", r)
	}
}

func TestEvaluateLowBeatsForecast(t *testing.T) {
	r := Evaluate(Input{HasBalance: true, VND: 10000, Points: pts(30000, 20000, 10000)}, cfg)
	if r.Level != LevelLow || r.DaysLeft == nil || !near(*r.DaysLeft, 1) {
		t.Fatalf("got %+v", r)
	}
}
