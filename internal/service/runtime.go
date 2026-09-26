package service

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/wyw14/solo-035/internal/audio"
	"github.com/wyw14/solo-035/internal/backup"
	"github.com/wyw14/solo-035/internal/caption"
	"github.com/wyw14/solo-035/internal/emergency"
	"github.com/wyw14/solo-035/internal/encoder"
	"github.com/wyw14/solo-035/internal/event"
	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/mux"
	"github.com/wyw14/solo-035/internal/playout"
	"github.com/wyw14/solo-035/internal/quality"
	"github.com/wyw14/solo-035/internal/source"
	"github.com/wyw14/solo-035/internal/syncer"
)

type Runtime struct {
	Clock           func() time.Time
	Events          *event.State
	EventFlow       *event.Integration
	Sources         *source.State
	SourceFlow      *source.Coordinator
	Receiver        *source.Receiver
	SyncState       *syncer.State
	Sync            *syncer.Coordinator
	Encoders        *encoder.State
	EncoderFlow     *encoder.Coordinator
	MuxState        *mux.State
	Mux             *mux.Coordinator
	AudioState      *audio.State
	Audio           *audio.Switch
	AudioMonitor    *audio.Monitor
	Captions        *caption.State
	CaptionFlow     *caption.Coordinator
	Continuity      *caption.Continuity
	Evidence        *quality.EvidenceSet
	Quality         *quality.Coordinator
	Evaluator       *quality.Evaluator
	JournalState    *journal.State
	JournalStore    *journal.Store
	Journal         *journal.Integration
	PlayoutState    *playout.State
	Playout         *playout.Coordinator
	PlayoutFlow     *playout.Integration
	PlayoutRecovery *playout.Recovery
	BackupState     *backup.State
	Backup          *backup.Coordinator
	EmergencyState  *emergency.State
	Emergency       *emergency.Coordinator
}

func NewRuntime(dataDirectory string, clock func() time.Time) (*Runtime, error) {
	if clock == nil {
		clock = time.Now
	}
	store, err := journal.NewStore(filepath.Join(dataDirectory, "journal"))
	if err != nil {
		return nil, err
	}
	monitor, err := audio.NewMonitor(-60, 6)
	if err != nil {
		return nil, err
	}
	evaluator, err := quality.NewEvaluator(quality.Thresholds{MinimumLoudness: -60, MaximumLoudness: 6, MaximumAgeMillis: 5000})
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{Clock: clock}
	runtime.Events = event.NewState()
	runtime.EventFlow = event.NewIntegration(runtime.Events, clock)
	runtime.Sources = source.NewState()
	runtime.SourceFlow = source.NewCoordinator(runtime.Sources, clock)
	runtime.Receiver = source.NewReceiver(runtime.SourceFlow)
	runtime.SyncState = syncer.NewState()
	runtime.Sync = syncer.NewCoordinator(runtime.SyncState)
	runtime.Encoders = encoder.NewState("encoder-primary-1", "encoder-backup-1")
	runtime.EncoderFlow = encoder.NewCoordinator(runtime.Encoders, clock)
	runtime.MuxState = mux.NewState()
	runtime.Mux = mux.NewCoordinator(runtime.MuxState, clock)
	runtime.AudioState = audio.NewState()
	runtime.Audio = audio.NewSwitch(runtime.AudioState, clock)
	runtime.AudioMonitor = monitor
	runtime.Captions = caption.NewState()
	runtime.CaptionFlow = caption.NewCoordinator(runtime.Captions, clock)
	runtime.Continuity = caption.NewContinuity()
	runtime.Evidence = quality.NewEvidenceSet()
	runtime.Quality = quality.NewCoordinator(runtime.Evidence, clock)
	runtime.Evaluator = evaluator
	runtime.JournalState = journal.NewState()
	runtime.JournalStore = store
	runtime.Journal = journal.NewIntegration(store, runtime.JournalState, clock)
	runtime.PlayoutState = playout.NewState()
	runtime.Playout = playout.NewCoordinator(runtime.PlayoutState, runtime.Journal, clock)
	runtime.PlayoutFlow = playout.NewIntegration(runtime.PlayoutState)
	runtime.PlayoutRecovery = playout.NewRecovery(runtime.PlayoutState, clock)
	runtime.BackupState = backup.NewState()
	runtime.Backup = backup.NewCoordinator(runtime.BackupState, runtime.Audio, runtime.EncoderFlow, runtime.PlayoutFlow, clock)
	if runtime.Backup == nil {
		return nil, fmt.Errorf("backup coordinator was not constructed")
	}
	runtime.EmergencyState = emergency.NewState()
	runtime.Emergency = emergency.NewCoordinator(runtime.EmergencyState, runtime.Journal, clock, runtime.takeoverReadiness, runtime.applySlateHandover)
	if runtime.Emergency == nil {
		return nil, fmt.Errorf("emergency slate coordinator was not constructed")
	}
	return runtime, nil
}

// takeoverReadiness 向应急通路提供"接管输出是否已准备好"的判定：
// 事件在播出链路上必须存在一路有效的已恢复输出（在播、来源与版本齐全）。
func (runtime *Runtime) takeoverReadiness(eventID model.EventID) (emergency.TakeoverOutput, bool) {
	output, ok := runtime.Playout.Output(eventID)
	if !ok || !output.Source.Valid() || !output.Revision.Valid() {
		return emergency.TakeoverOutput{}, false
	}
	return emergency.TakeoverOutput{Source: output.Source, Revision: output.Revision, Fence: output.Fence}, true
}

// applySlateHandover 是应急结束时的实际交接动作：把已准备好的接管输出重新发布
// 到播出链路（恢复在播），并落一条"节目恢复"日志；成功后调用方再写交接证据。
func (runtime *Runtime) applySlateHandover(event model.LiveEvent, takeover emergency.Takeover) error {
	if err := runtime.PlayoutState.Publish(event.ID, takeover.Revision, takeover.Fence, runtime.Clock()); err != nil {
		return err
	}
	if _, err := runtime.Journal.Persist(event.ID, "program-resumed", takeover.Revision, takeover.Fence, map[string]any{
		"takeover_id": takeover.ID,
		"source":      takeover.Source,
		"label":       takeover.Label,
	}); err != nil {
		return err
	}
	return nil
}
