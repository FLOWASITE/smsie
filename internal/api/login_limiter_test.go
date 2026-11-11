package api

import (
	"testing"
	"time"
)

func TestAttemptLimiterBlocksAfterMaxAndRecovers(t *testing.T) {
	l := newAttemptLimiter(15 * time.Minute)
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if b, _ := l.blocked("ip:1.2.3.4", 3); b {
			t.Fatalf("bị chặn sớm ở lần %d", i)
		}
		l.fail("ip:1.2.3.4")
		now = now.Add(time.Minute)
	}
	b, wait := l.blocked("ip:1.2.3.4", 3)
	if !b || wait != 12*time.Minute {
		t.Fatalf("blocked = %v, wait = %v (lần sai đầu lúc 09:00 hết hạn 09:15, giờ là 09:03)", b, wait)
	}
	if b, _ := l.blocked("ip:5.6.7.8", 3); b {
		t.Fatal("IP khác không được bị chặn theo")
	}
	now = now.Add(12*time.Minute + time.Second)
	if b, _ := l.blocked("ip:1.2.3.4", 3); b {
		t.Fatal("hết cửa sổ phải được thử lại")
	}
}

func TestAttemptLimiterResetAndDisabled(t *testing.T) {
	l := newAttemptLimiter(time.Minute)
	l.fail("user:admin")
	l.fail("user:admin")
	if b, _ := l.blocked("user:admin", 2); !b {
		t.Fatal("phải chặn")
	}
	l.reset("user:admin")
	if b, _ := l.blocked("user:admin", 2); b {
		t.Fatal("reset xong phải hết chặn")
	}
	l.fail("user:admin")
	if b, _ := l.blocked("user:admin", 0); b {
		t.Fatal("max = 0 nghĩa là tắt giới hạn")
	}
}
