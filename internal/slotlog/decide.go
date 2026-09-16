// Package slotlog quyết định sự kiện đổi khe của SIM từ trạng thái các khe.
// Thuần: không DB, không thời gian — caller điền DetectedAt/PhoneNumber/Operator/PortName.
package slotlog

import "github.com/pccr10001/smsie/internal/model"

type Decision struct {
	Bay        model.ModemBay  // trạng thái mới của khe có IMEI này (caller upsert)
	VacatedBay *model.ModemBay // khe cũ của ICCID (đã xoá CurrentICCID), nil nếu không có
	Events     []model.SimSlotEvent
}

// DecideSlotEvents nhận toàn bộ bays hiện có, IMEI và ICCID vừa probe được.
func DecideSlotEvents(bays []model.ModemBay, imei, iccid string) Decision {
	if imei == "" || iccid == "" {
		return Decision{}
	}
	var bay *model.ModemBay
	var old *model.ModemBay
	for i := range bays {
		if bays[i].IMEI == imei {
			bay = &bays[i]
		} else if bays[i].CurrentICCID == iccid {
			old = &bays[i]
		}
	}

	if bay == nil {
		b := model.ModemBay{IMEI: imei, CurrentICCID: iccid}
		return Decision{Bay: b, Events: []model.SimSlotEvent{{ICCID: iccid, IMEI: imei, Event: model.SlotEventInserted}}}
	}
	if bay.CurrentICCID == iccid {
		return Decision{Bay: *bay}
	}

	d := Decision{Bay: *bay}
	if bay.CurrentICCID != "" {
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: bay.CurrentICCID, IMEI: imei, Event: model.SlotEventRemoved, FromSlot: bay.SlotNumber})
	}
	if old != nil {
		vacated := *old
		vacated.CurrentICCID = ""
		d.VacatedBay = &vacated
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: iccid, IMEI: imei, Event: model.SlotEventMoved, FromSlot: old.SlotNumber, ToSlot: bay.SlotNumber})
	} else {
		d.Events = append(d.Events, model.SimSlotEvent{ICCID: iccid, IMEI: imei, Event: model.SlotEventInserted, ToSlot: bay.SlotNumber})
	}
	d.Bay.CurrentICCID = iccid
	return d
}
