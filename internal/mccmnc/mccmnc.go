package mccmnc

import (
	"encoding/json"
	"os"
	"sync"
)

// NetworkOperator represents an entry in mcc_mnc.json
type NetworkOperator struct {
	MCC         string `json:"mcc"`
	MNC         string `json:"mnc"`
	ISO         string `json:"iso"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	Name        string `json:"name"`
}

var (
	operators []NetworkOperator
	once      sync.Once
)

// LoadOperators loads the mcc_mnc.json file
func LoadOperators(path string) error {
	var err error
	once.Do(func() {
		file, e := os.ReadFile(path)
		if e != nil {
			err = e
			return
		}
		if e := json.Unmarshal(file, &operators); e != nil {
			err = e
			return
		}
	})
	return err
}

// builtinVN: nhà mạng Việt Nam dùng khi không có mcc_mnc.json (khay toàn SIM VN).
// ponytail: chỉ VN; nước khác thì cần file mcc_mnc.json như README.
var builtinVN = map[string]string{
	"01": "Mobifone",
	"02": "Vinaphone",
	"04": "Viettel",
	"05": "Vietnamobile",
	"07": "Gmobile",
	"08": "Viettel",
}

// GetOperatorName finds the operator name for a given MCC and MNC
func GetOperatorName(mcc, mnc string) string {
	for _, op := range operators {
		if op.MCC == mcc && op.MNC == mnc {
			return op.Name
		}
	}
	if mcc == "452" {
		return builtinVN[mnc]
	}
	return ""
}
