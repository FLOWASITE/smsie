package slotlog

import "testing"

func TestInferSlot(t *testing.T) {
	known := map[int]string{16: "COM19", 15: "COM20", 14: "COM21", 13: "COM22"} // offset 35
	if s := InferSlot(known, "COM25"); s == nil || *s != 10 {
		t.Fatalf("COM25 phải ra khe 10, got %v", s)
	}
	if s := InferSlot(known, "COM40"); s != nil { // 35-40 < 1
		t.Fatalf("ngoài 1..32 phải nil, got %d", *s)
	}
	if s := InferSlot(known, "COM19"); s != nil { // khe 16 đã có
		t.Fatalf("khe đã giữ phải nil, got %d", *s)
	}
	if s := InferSlot(map[int]string{16: "COM19"}, "COM20"); s != nil {
		t.Fatalf("1 khe không đủ bỏ phiếu, got %d", *s)
	}
	if s := InferSlot(map[int]string{16: "COM19", 3: "COM7"}, "COM20"); s != nil { // 35 vs 10, hoà 1-1
		t.Fatalf("không có đa số phải nil, got %d", *s)
	}
	if s := InferSlot(known, "ttyUSB0"); s != nil {
		t.Fatalf("cổng không phải COM phải nil, got %d", *s)
	}
}
