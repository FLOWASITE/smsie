package slotlog

import (
	"testing"

	"github.com/pccr10001/smsie/internal/model"
)

func slot(n int) *int { return &n }

func bays(list ...model.ModemBay) []model.ModemBay { return list }

func TestDecideUnknownModemInsertsWithoutSlot(t *testing.T) {
	d := DecideSlotEvents(bays(), "IMEI-A", "ICCID-1")
	if d.Bay.IMEI != "IMEI-A" || d.Bay.SlotNumber != nil || d.Bay.CurrentICCID != "ICCID-1" {
		t.Fatalf("bay = %+v", d.Bay)
	}
	if len(d.Events) != 1 || d.Events[0].Event != model.SlotEventInserted || d.Events[0].ToSlot != nil {
		t.Fatalf("events = %+v", d.Events)
	}
	if d.VacatedBay != nil {
		t.Fatalf("no bay should be vacated")
	}
}

func TestDecideEmptyInputsNoop(t *testing.T) {
	for _, tc := range []struct{ imei, iccid string }{{"", "X"}, {"IMEI-A", ""}} {
		d := DecideSlotEvents(bays(), tc.imei, tc.iccid)
		if len(d.Events) != 0 || d.Bay.IMEI != "" {
			t.Fatalf("imei=%q iccid=%q: d = %+v", tc.imei, tc.iccid, d)
		}
	}
}

func TestDecideSameSimNoEvent(t *testing.T) {
	d := DecideSlotEvents(bays(model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"}), "IMEI-A", "ICCID-1")
	if len(d.Events) != 0 {
		t.Fatalf("expected no events, got %+v", d.Events)
	}
}

func TestDecideInsertedIntoEmptyBay(t *testing.T) {
	d := DecideSlotEvents(bays(model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15)}), "IMEI-A", "ICCID-1")
	if len(d.Events) != 1 || d.Events[0].Event != model.SlotEventInserted || *d.Events[0].ToSlot != 15 {
		t.Fatalf("events = %+v", d.Events)
	}
}

func TestDecideMovedBetweenBays(t *testing.T) {
	d := DecideSlotEvents(bays(
		model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"},
		model.ModemBay{IMEI: "IMEI-B", SlotNumber: slot(16)},
	), "IMEI-B", "ICCID-1")
	if len(d.Events) != 1 {
		t.Fatalf("events = %+v", d.Events)
	}
	e := d.Events[0]
	if e.Event != model.SlotEventMoved || *e.FromSlot != 15 || *e.ToSlot != 16 || e.ICCID != "ICCID-1" {
		t.Fatalf("event = %+v", e)
	}
	if d.VacatedBay == nil || d.VacatedBay.IMEI != "IMEI-A" || d.VacatedBay.CurrentICCID != "" {
		t.Fatalf("vacated = %+v", d.VacatedBay)
	}
}

func TestDecideSwapEmitsRemovedForPreviousOccupant(t *testing.T) {
	// Khe 16 đang có ICCID-2, nay thấy ICCID-1 (đang ghi ở khe 15) → moved 15→16 + removed ICCID-2 khỏi 16.
	d := DecideSlotEvents(bays(
		model.ModemBay{IMEI: "IMEI-A", SlotNumber: slot(15), CurrentICCID: "ICCID-1"},
		model.ModemBay{IMEI: "IMEI-B", SlotNumber: slot(16), CurrentICCID: "ICCID-2"},
	), "IMEI-B", "ICCID-1")
	if len(d.Events) != 2 {
		t.Fatalf("events = %+v", d.Events)
	}
	if d.Events[0].Event != model.SlotEventRemoved || d.Events[0].ICCID != "ICCID-2" || *d.Events[0].FromSlot != 16 || d.Events[0].ToSlot != nil {
		t.Fatalf("removed = %+v", d.Events[0])
	}
	if d.Events[1].Event != model.SlotEventMoved || d.Events[1].ICCID != "ICCID-1" {
		t.Fatalf("moved = %+v", d.Events[1])
	}
	if d.Bay.CurrentICCID != "ICCID-1" {
		t.Fatalf("bay = %+v", d.Bay)
	}
}
