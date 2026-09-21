package slotlog

import (
	"strconv"
	"strings"
)

// InferSlot đoán số khe cho một modem mới thấy từ các khe đã hiệu chuẩn: trên một khay
// cắm USB cố định, số khe và số COM lệch nhau một hằng số (offset = khe + COM). Lấy offset
// được nhiều khe đồng ý nhất (≥ 2 khe), suy khe = offset − COM; trả nil nếu ra ngoài 1..32
// hoặc khe đó đã có người giữ.
// ponytail: giả định ánh xạ tuyến tính, đủ cho một khay 32 cổng cắm cố định; đổi hub/USB
// thì hiệu chuẩn tay như cũ.
func InferSlot(known map[int]string, port string) *int {
	com, ok := comNumber(port)
	if !ok || len(known) < 2 {
		return nil
	}
	votes := map[int]int{}
	for slot, p := range known {
		if c, ok := comNumber(p); ok {
			votes[slot+c]++
		}
	}
	bestOff, best := 0, 0
	for off, n := range votes {
		if n > best || (n == best && off < bestOff) {
			bestOff, best = off, n
		}
	}
	if best < 2 {
		return nil
	}
	slot := bestOff - com
	if slot < 1 || slot > 32 {
		return nil
	}
	if _, taken := known[slot]; taken {
		return nil
	}
	return &slot
}

func comNumber(port string) (int, bool) {
	p := strings.ToUpper(strings.TrimSpace(port))
	p = strings.TrimPrefix(p, `\.\`)
	if !strings.HasPrefix(p, "COM") {
		return 0, false
	}
	n, err := strconv.Atoi(p[3:])
	return n, err == nil
}
