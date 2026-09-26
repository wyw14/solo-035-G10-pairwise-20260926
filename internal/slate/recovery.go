package slate

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/model"
)

type RecoveryRecord struct {
	EventID   model.EventID `json:"event_id"`
	Emergency Emergency     `json:"emergency"`
}

func (state *State) ExportMaterials() []Material {
	return state.ListMaterials()
}

func (state *State) ExportEmergencies() []RecoveryRecord {
	emergencies := state.ListEmergencies()
	records := make([]RecoveryRecord, 0, len(emergencies))
	for _, emergency := range emergencies {
		records = append(records, RecoveryRecord{EventID: emergency.EventID, Emergency: emergency})
	}
	return records
}

func (state *State) RestoreMaterials(materials []Material) error {
	for _, material := range materials {
		if material.ID == "" || material.Name == "" || material.ContentURI == "" {
			return fmt.Errorf("slate material recovery record is incomplete")
		}
		if !material.Controlled {
			return fmt.Errorf("slate material %s was never controlled", material.ID)
		}
	}
	state.ReplaceMaterials(materials)
	return nil
}

func (state *State) RestoreEmergencies(records []RecoveryRecord) error {
	emergencies := make([]Emergency, 0, len(records))
	for _, record := range records {
		if !record.EventID.Valid() || !record.Emergency.Command.Valid() {
			return fmt.Errorf("slate recovery identity is invalid")
		}
		switch record.Emergency.Status {
		case StatusProposed, StatusOnAir, StatusHandedOver:
		default:
			return fmt.Errorf("slate emergency for event %s has an unknown status %q", record.EventID, record.Emergency.Status)
		}
		record.Emergency.EventID = record.EventID
		emergencies = append(emergencies, record.Emergency)
	}
	state.ReplaceEmergencies(emergencies)
	return nil
}
