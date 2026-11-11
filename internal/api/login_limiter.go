package api

import (
	"sync"
	"time"
)

// attemptLimiter đếm lần đăng nhập SAI theo khoá (IP, tên đăng nhập) trong một cửa sổ trượt.
// Chỉ lần sai mới bị đếm; đăng nhập đúng xoá bộ đếm của khoá đó. Giữ trong bộ nhớ — khởi động lại là về 0,
// đủ cho mục đích chặn dò mật khẩu.
type attemptLimiter struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

const limiterMaxKeys = 20000

func newAttemptLimiter(window time.Duration) *attemptLimiter {
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &attemptLimiter{window: window, now: time.Now, hits: map[string][]time.Time{}}
}

func (l *attemptLimiter) recent(key string, now time.Time) []time.Time {
	list := l.hits[key]
	cut := now.Add(-l.window)
	i := 0
	for i < len(list) && !list[i].After(cut) {
		i++
	}
	if i > 0 {
		list = list[i:]
		if len(list) == 0 {
			delete(l.hits, key)
		} else {
			l.hits[key] = list
		}
	}
	return list
}

// blocked: khoá đã có ≥ max lần sai trong cửa sổ? Trả thêm thời gian phải chờ tới khi lần sai cũ nhất hết hạn.
func (l *attemptLimiter) blocked(key string, max int) (bool, time.Duration) {
	if max <= 0 {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	list := l.recent(key, now)
	if len(list) < max {
		return false, 0
	}
	wait := list[len(list)-max].Add(l.window).Sub(now)
	if wait < time.Second {
		wait = time.Second
	}
	return true, wait
}

func (l *attemptLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.hits) >= limiterMaxKeys {
		for k := range l.hits {
			l.recent(k, now)
		}
		if len(l.hits) >= limiterMaxKeys { // vẫn đầy: bỏ hết còn hơn phình bộ nhớ vô hạn
			l.hits = map[string][]time.Time{}
		}
	}
	l.hits[key] = append(l.recent(key, now), now)
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
