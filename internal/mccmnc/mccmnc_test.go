package mccmnc

import "testing"

func TestBuiltinVNFallback(t *testing.T) {
	if got := GetOperatorName("452", "05"); got != "Vietnamobile" {
		t.Fatalf("452/05 = %q", got)
	}
	if got := GetOperatorName("452", "99"); got != "" {
		t.Fatalf("unknown mnc = %q", got)
	}
	if got := GetOperatorName("310", "260"); got != "" {
		t.Fatalf("non-VN without file = %q", got)
	}
}
