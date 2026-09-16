package balance

import (
	"sort"
	"time"
)

// Point là một mốc số dư đã đọc.
type Point struct {
	At  time.Time
	VND int64
}

// Forecast hồi quy tuyến tính VND ~ ngày trên "đoạn giảm gần nhất" của ≤7 mốc cuối:
// nếu trong 7 mốc cuối có mốc tăng (nạp tiền), chỉ dùng từ mốc tăng cuối cùng trở đi.
// ok=false khi <2 mốc, trải <24h, hoặc slope ≥ 0. daysLeft = VND_cuối / (-slope_theo_ngày).
func Forecast(points []Point) (daysLeft float64, ok bool) {
	p := append([]Point(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].At.Before(p[j].At) })
	if len(p) > 7 {
		p = p[len(p)-7:]
	}
	for i := len(p) - 1; i > 0; i-- {
		if p[i].VND > p[i-1].VND {
			p = p[i:]
			break
		}
	}
	if len(p) < 2 || p[len(p)-1].At.Sub(p[0].At) < 24*time.Hour {
		return 0, false
	}
	n := float64(len(p))
	var st, sv float64
	t := make([]float64, len(p))
	for i, pt := range p {
		t[i] = pt.At.Sub(p[0].At).Hours() / 24
		st += t[i]
		sv += float64(pt.VND)
	}
	mt, mv := st/n, sv/n
	var cov, vart float64
	for i, pt := range p {
		cov += (t[i] - mt) * (float64(pt.VND) - mv)
		vart += (t[i] - mt) * (t[i] - mt)
	}
	if vart == 0 {
		return 0, false
	}
	slope := cov / vart
	if slope >= 0 {
		return 0, false
	}
	daysLeft = float64(p[len(p)-1].VND) / (-slope)
	if daysLeft < 0 {
		daysLeft = 0
	}
	return daysLeft, true
}
