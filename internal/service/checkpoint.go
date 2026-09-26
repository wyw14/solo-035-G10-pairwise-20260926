package service

import (
	"fmt"

	"github.com/wyw14/solo-035/internal/backup"
	"github.com/wyw14/solo-035/internal/emergency"
	"github.com/wyw14/solo-035/internal/event"
	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/quality"
	"github.com/wyw14/solo-035/internal/syncer"
)

type ControlCheckpoint struct {
	Events        []event.RecoveryRecord   `json:"events"`
	Sync          []syncer.Recovery        `json:"sync"`
	Backup        []backup.RecoveryRecord  `json:"backup"`
	Quality       []quality.RecoveryRecord `json:"quality"`
	Journal       []journal.Record         `json:"journal"`
	SlateAssets   []emergency.Asset        `json:"slate_assets"`
	SlateSessions []emergency.Session      `json:"slate_sessions"`
}

func (runtime *Runtime) ExportCheckpoint() ControlCheckpoint {
	events := runtime.Events.ExportRecovery()
	ids := make([]model.EventID, 0, len(events))
	for _, record := range events {
		ids = append(ids, record.Event.ID)
	}
	return ControlCheckpoint{
		Events:        events,
		Sync:          runtime.SyncState.ExportRecovery(),
		Backup:        runtime.BackupState.ExportRecovery(),
		Quality:       runtime.Evidence.Export(ids),
		Journal:       runtime.Journal.Records(),
		SlateAssets:   runtime.EmergencyState.AssetsList(),
		SlateSessions: runtime.EmergencyState.Sessions(),
	}
}

func (runtime *Runtime) RestoreCheckpoint(checkpoint ControlCheckpoint) (RecoveryReport, error) {
	if err := runtime.Events.Restore(checkpoint.Events, runtime.Clock()); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore events: %w", err)
	}
	if err := runtime.SyncState.RestoreRecovery(checkpoint.Sync); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore synchronization: %w", err)
	}
	if err := runtime.BackupState.RestoreRecovery(checkpoint.Backup); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore failovers: %w", err)
	}
	if err := runtime.Evidence.Restore(checkpoint.Quality); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore quality evidence: %w", err)
	}
	if err := runtime.EmergencyState.StateRestoreAssets(checkpoint.SlateAssets); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore slate assets: %w", err)
	}
	if err := runtime.EmergencyState.RestoreSessions(checkpoint.SlateSessions); err != nil {
		return RecoveryReport{}, fmt.Errorf("restore slate sessions: %w", err)
	}
	sources := make(map[model.EventID]model.SourceID)
	for _, item := range checkpoint.Sync {
		sources[item.EventID] = item.Source
	}
	return runtime.RestoreFromRecords(checkpoint.Journal, func(eventID model.EventID) model.SourceID {
		sourceID := sources[eventID]
		if sourceID == "" {
			sourceID = model.NewSourceID()
		}
		return sourceID
	})
}
