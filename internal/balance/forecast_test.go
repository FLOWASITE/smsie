package balance

import (
	"math"
	"testing"
	"time"
)

// pts: mỗi mốc cách nhau 1 ngày, bắt đầu 2026-09-01 06:00.
func pts(vals ...int64) []Point {
	base := time.Date(2026, 9, 1, 6, 0, 0, 0, time.Local)
	out := make([]Point, len(vals))
	for i, v := range vals {
		out[i] = Point{At: base.AddDate(0, 0, i), VND: v}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestForecastNeedsTwoPointsSpanningOneDay(t *testing.T) {
	if _, ok := Forecast(pts(50000)); ok {
		t.Fatal("1 mốc phải ok=false")
	}
	base := time.Date(2026, 9, 1, 6, 0, 0, 0, time.Local)
	if _, ok := Forecast([]Point{{base, 50000}, {base.Add(time.Hour), 40000}}); ok {
		t.Fatal("2 mốc cách 1 giờ phải ok=false")
	}
}

func TestForecastIgnoresRisingBalance(t *testing.T) {
	if _, ok := Forecast(pts(10000, 20000, 30000)); ok {
		t.Fatal("chuỗi tăng phải ok=false")
	}
}

func TestForecastLinearDecline(t *testing.T) {
	d, ok := Forecast(pts(70000, 60000, 50000))
	if !ok || !near(d, 5) {
		t.Fatalf("got %v %v, want 5 true", d, ok)
	}
}

func TestForecastUsesLastSevenPoints(t *testing.T) {
	// 3 đầu tăng mạnh, 7 cuối giảm 10k/ngày từ 100k → 40k
	d, ok := Forecast(pts(1000, 2000, 3000, 100000, 90000, 80000, 70000, 60000, 50000, 40000))
	if !ok || !near(d, 4) {
		t.Fatalf("got %v %v, want 4 true", d, ok)
	}
}

func TestForecastAfterTopUpStillDeclining(t *testing.T) {
	// đoạn giảm gần nhất: [80000, 70000, 60000] → 6 ngày
	d, ok := Forecast(pts(30000, 20000, 80000, 70000, 60000))
	if !ok || !near(d, 6) {
		t.Fatalf("got %v %v, want 6 true", d, ok)
	}
}
