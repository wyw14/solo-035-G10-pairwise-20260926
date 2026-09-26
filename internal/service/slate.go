package service

import (
	"fmt"
	"strings"

	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/slate"
)

func (coordinator *Coordinator) RegisterSlateMaterial(material slate.Material) (slate.Material, error) {
	return coordinator.runtime.Slate.RegisterMaterial(material)
}

func (runtime *Runtime) SlateMaterials() []slate.Material {
	return runtime.Slate.Materials()
}

func (runtime *Runtime) SlateEmergencies() []slate.Emergency {
	return runtime.Slate.Emergencies()
}

func (runtime *Runtime) SlateStatus(eventID model.EventID) (slate.StatusView, error) {
	if !eventID.Valid() {
		return slate.StatusView{}, fmt.Errorf("event identity is invalid")
	}
	return runtime.Slate.Status(eventID), nil
}

func (coordinator *Coordinator) ProposeSlate(eventID model.EventID, reason string, operator string) (slate.Emergency, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return slate.Emergency{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Slate.Propose(event, reason, operator)
}

func (coordinator *Coordinator) ConfirmSlateCutIn(eventID model.EventID, slateID string, command model.CommandID, operator string) (slate.Emergency, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return slate.Emergency{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Slate.ConfirmCutIn(event, slateID, command, operator)
}

func (coordinator *Coordinator) CompleteSlateHandover(eventID model.EventID, takeoverID string, director string) (slate.Emergency, error) {
	event, ok := coordinator.runtime.Events.Get(eventID)
	if !ok {
		return slate.Emergency{}, fmt.Errorf("event %s not found", eventID)
	}
	return coordinator.runtime.Slate.Handover(event, takeoverID, director)
}

func (runtime *Runtime) SlateEvidence(eventID model.EventID) []journal.Record {
	records := runtime.Journal.Records()
	evidence := make([]journal.Record, 0)
	for _, record := range records {
		if record.EventID == eventID && strings.HasPrefix(record.Kind, "slate-") {
			evidence = append(evidence, record)
		}
	}
	return evidence
}
