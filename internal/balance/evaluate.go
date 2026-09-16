package balance

const (
	LevelUnknown  = "unknown"
	LevelOK       = "ok"
	LevelLow      = "low"
	LevelForecast = "forecast"
)

type Config struct {
	LowThresholdVND int64
	ForecastDays    int
}

type Input struct {
	HasBalance   bool
	VND          int64
	SimThreshold *int64 // ngưỡng riêng của SIM, nil = dùng cfg
	Points       []Point
}

type Result struct {
	Level           string
	ThresholdVND    int64
	ThresholdSource string // "sim" | "config"
	DaysLeft        *float64
}

// Evaluate xếp mức cảnh báo: low thắng forecast; DaysLeft vẫn điền khi dự báo được.
func Evaluate(in Input, cfg Config) Result {
	r := Result{Level: LevelUnknown, ThresholdVND: cfg.LowThresholdVND, ThresholdSource: "config"}
	if in.SimThreshold != nil {
		r.ThresholdVND, r.ThresholdSource = *in.SimThreshold, "sim"
	}
	if !in.HasBalance {
		return r
	}
	r.Level = LevelOK
	if d, ok := Forecast(in.Points); ok {
		r.DaysLeft = &d
		if d <= float64(cfg.ForecastDays) {
			r.Level = LevelForecast
		}
	}
	if in.VND < r.ThresholdVND {
		r.Level = LevelLow
	}
	return r
}
