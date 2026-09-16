// Package keepalive nuôi SIM: định kỳ gửi một SMS nội mạng sang SIM khác trong khay.
package keepalive

import (
	"sort"
	"time"
)

// Candidate là một SIM trong khay đang xét làm đích.
type Candidate struct {
	ICCID       string
	Phone       string
	Operator    string
	Online      bool
	TargetCount int // số lần đã làm đích trong cửa sổ xoay vòng
}

// pickTarget chọn SIM đích cho src: loại chính nó và SIM không có số; nếu có ít nhất một SIM
// cùng nhà mạng thì chỉ xét nhóm đó; rồi ưu tiên online, TargetCount thấp, ICCID nhỏ (ổn định).
func pickTarget(src Candidate, all []Candidate) (Candidate, bool) {
	var pool, same []Candidate
	for _, c := range all {
		if c.ICCID == src.ICCID || c.Phone == "" {
			continue
		}
		pool = append(pool, c)
		if c.Operator != "" && c.Operator == src.Operator {
			same = append(same, c)
		}
	}
	if len(same) > 0 {
		pool = same
	}
	if len(pool) == 0 {
		return Candidate{}, false
	}
	sort.Slice(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.Online != b.Online {
			return a.Online
		}
		if a.TargetCount != b.TargetCount {
			return a.TargetCount < b.TargetCount
		}
		return a.ICCID < b.ICCID
	})
	return pool[0], true
}

// isDue: mốc tham chiếu = lastActivity, thiếu thì firstSeen; cả hai nil → false.
func isDue(now time.Time, lastActivity, firstSeen *time.Time, intervalDays int) bool {
	ref := lastActivity
	if ref == nil {
		ref = firstSeen
	}
	if ref == nil {
		return false
	}
	return now.Sub(*ref) >= time.Duration(intervalDays)*24*time.Hour
}
