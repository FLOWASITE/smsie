package worker

import (
	"errors"
	"testing"
)

func TestParseIMEI(t *testing.T) {
	cases := map[string]string{
		"123456789012345\r\n\r\nOK":                                "123456789012345",
		"+CPIN: READY\r\n+QIND: SMS DONE\r\n123456789012345\r\nOK": "123456789012345",
		"+CPIN: READY\r\nOK":                                       "",
	}
	for in, want := range cases {
		if got := parseIMEI(in); got != want {
			t.Errorf("parseIMEI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSIMNotInserted(t *testing.T) {
	cases := map[string]bool{
		"modem error: +CME ERROR: 10":               true,
		"modem error: +CME ERROR: SIM not inserted": true,
		"modem error: +CME ERROR: 100":              false,
		"modem error: +CME ERROR: 14":               false,
		"timeout":                                   false,
	}
	for in, want := range cases {
		if got := isSIMNotInserted(errors.New(in)); got != want {
			t.Errorf("isSIMNotInserted(%q) = %v, want %v", in, got, want)
		}
	}
	if isSIMNotInserted(nil) {
		t.Error("nil error must not be SIM not inserted")
	}
}
