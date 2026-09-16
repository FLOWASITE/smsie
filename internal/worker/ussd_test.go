package worker

import (
	"encoding/hex"
	"testing"

	"github.com/warthog618/sms/encoding/gsm7"
	"github.com/warthog618/sms/encoding/ucs2"
)

func TestDecodeCUSD(t *testing.T) {
	plain := "So thue bao cua quy khach la 84912345678"
	u := hex.EncodeToString(ucs2.Encode([]rune("Số thuê bao: 0912345678")))
	packed, _ := gsm7.Encode([]byte(plain))
	g := hex.EncodeToString(gsm7.Pack7BitUSSD(packed, 0))
	cases := map[string]string{
		`+CUSD: 0,"` + plain + `",15`: plain,
		`+CUSD: 1,"` + u + `",72`:     "Số thuê bao: 0912345678",
		`+CUSD: 0,"` + u + `",15`:     "Số thuê bao: 0912345678", // modem ở CSCS=UCS2 vẫn báo dcs 15
		`+CUSD: 0,"` + g + `",15`:     plain,
		`+CUSD: 0,"0901234567",15`:    "0901234567", // toàn chữ số, không phải hex
		`+CUSD: 2`:                    `+CUSD: 2`,
		`TK chinh: 50000d`:            `TK chinh: 50000d`,
	}
	for in, want := range cases {
		if got := decodeCUSD(in); got != want {
			t.Errorf("decodeCUSD(%q) = %q, want %q", in, got, want)
		}
	}
}
