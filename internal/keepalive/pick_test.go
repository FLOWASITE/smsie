package keepalive

import (
	"testing"
	"time"
)

func TestPickTargetSameOperatorWins(t *testing.T) {
	src := Candidate{ICCID: "S", Phone: "1", Operator: "Viettel"}
	all := []Candidate{
		src,
		{ICCID: "A", Phone: "2", Operator: "Mobifone", Online: true},
		{ICCID: "B", Phone: "3", Operator: "Viettel", Online: false},
	}
	got, ok := pickTarget(src, all)
	if !ok || got.ICCID != "B" {
		t.Fatalf("got %+v %v, want B (cùng nhà mạng dù offline)", got, ok)
	}
}

func TestPickTargetFallsBackToOtherOperatorAndPrefersOnline(t *testing.T) {
	src := Candidate{ICCID: "S", Phone: "1", Operator: "Viettel"}
	all := []Candidate{
		src,
		{ICCID: "A", Phone: "2", Operator: "Mobifone", Online: false},
		{ICCID: "B", Phone: "3", Operator: "Vinaphone", Online: true},
	}
	got, ok := pickTarget(src, all)
	if !ok || got.ICCID != "B" {
		t.Fatalf("got %+v %v, want B (online)", got, ok)
	}
}

func TestPickTargetSkipsNoPhoneAndSelf(t *testing.T) {
	src := Candidate{ICCID: "S", Phone: "1", Operator: "Viettel"}
	all := []Candidate{
		src,
		{ICCID: "A", Phone: "", Operator: "Viettel", Online: true},
	}
	if got, ok := pickTarget(src, all); ok {
		t.Fatalf("expected no candidate, got %+v", got)
	}
	if _, ok := pickTarget(src, nil); ok {
		t.Fatal("expected no candidate for empty list")
	}
}

func TestPickTargetRotatesByTargetCountThenICCID(t *testing.T) {
	src := Candidate{ICCID: "S", Phone: "1", Operator: "Viettel"}
	all := []Candidate{
		{ICCID: "C", Phone: "4", Operator: "Viettel", Online: true, TargetCount: 0},
		{ICCID: "A", Phone: "2", Operator: "Viettel", Online: true, TargetCount: 2},
		{ICCID: "B", Phone: "3", Operator: "Viettel", Online: true, TargetCount: 0},
		src,
	}
	got, ok := pickTarget(src, all)
	if !ok || got.ICCID != "B" {
		t.Fatalf("got %+v %v, want B (TargetCount 0, ICCID nhỏ nhất)", got, ok)
	}
}

func TestIsDue(t *testing.T) {
	now := time.Date(2026, 9, 16, 7, 0, 0, 0, time.Local)
	at := func(days int) *time.Time { v := now.AddDate(0, 0, -days); return &v }
	if isDue(now, nil, nil, 25) {
		t.Fatal("cả hai nil → không tới hạn")
	}
	if !isDue(now, nil, at(25), 25) {
		t.Fatal("chưa có SMS, first_seen 25 ngày → tới hạn")
	}
	if isDue(now, at(24), at(100), 25) {
		t.Fatal("last_activity 24 ngày → chưa tới hạn (ưu tiên last_activity hơn first_seen)")
	}
	if !isDue(now, at(25), nil, 25) {
		t.Fatal("đúng 25 ngày → tới hạn (>=)")
	}
	if isDue(now, at(25), nil, 26) {
		t.Fatal("interval 26 → chưa")
	}
}
