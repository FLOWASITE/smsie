package worker

import (
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/warthog618/sms/encoding/gsm7"
	"github.com/warthog618/sms/encoding/ucs2"
)

var cusdPattern = regexp.MustCompile(`^\+CUSD:\s*\d\s*,\s*"([^"]*)"(?:\s*,\s*(\d+))?`)

// decodeCUSD trả phần text đọc được của một dòng +CUSD. Modem hay trả payload ở dạng hex:
// UCS2 (dcs 72 / bit 0x08, hoặc modem đang ở CSCS=UCS2) hay GSM7 đóng gói (Huawei, dcs 15).
// Không phải +CUSD hoặc không giải mã được → trả nguyên dòng.
func decodeCUSD(line string) string {
	m := cusdPattern.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return line
	}
	text := m[1]
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) < 2 || (!strings.ContainsAny(text, "ABCDEFabcdef") && !isUCS2DCS(m[2])) {
		// Không phải hex (hoặc chỉ toàn chữ số với dcs GSM7 — ví dụ "0901234567" là số thật).
		return text
	}
	if len(raw)%2 == 0 {
		if r, err := ucs2.Decode(raw); err == nil && printable(r) {
			return string(r)
		}
	}
	if isUCS2DCS(m[2]) {
		return text
	}
	if out, err := gsm7.Decode(gsm7.Unpack7BitUSSD(raw, 0)); err == nil && printable([]rune(string(out))) {
		return string(out)
	}
	return text
}

func isUCS2DCS(s string) bool {
	dcs, err := strconv.Atoi(s)
	return err == nil && dcs&0x0C == 0x08
}

func printable(r []rune) bool {
	for _, c := range r {
		if !unicode.IsPrint(c) && !unicode.IsSpace(c) {
			return false
		}
	}
	return len(r) > 0
}
