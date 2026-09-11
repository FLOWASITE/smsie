package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pccr10001/smsie/internal/model"
	"gorm.io/gorm"
)

func TestUpsertIgnoresRuntimeOnlyPortName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Modem{}); err != nil {
		t.Fatal(err)
	}

	repo := NewModemRepository(db)
	if err := repo.Upsert(&model.Modem{ICCID: "89840000000000000000", IMEI: "123456789012345", PortName: "COM19"}); err != nil {
		t.Fatalf("upsert modem: %v", err)
	}
}
