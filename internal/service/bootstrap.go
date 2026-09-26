package service

import (
	"fmt"
	"time"

	"github.com/wyw14/solo-035/internal/emergency"
	"github.com/wyw14/solo-035/internal/model"
)

func Bootstrap(runtime *Runtime) error {
	coordinator := NewCoordinator(runtime)
	if err := bootstrapEmergencySlates(coordinator); err != nil {
		return err
	}
	event, err := coordinator.CreateEvent("Evening News")
	if err != nil {
		return err
	}
	sourceID := model.NewSourceID()
	status, err := coordinator.PrepareSignal(event.ID, sourceID)
	if err != nil {
		return err
	}
	if err := coordinator.ConfirmSignal(sourceID, status.CurrentCommand); err != nil {
		return err
	}
	base := runtime.Clock().UTC()
	for sequence := int64(1); sequence <= 3; sequence++ {
		frame := model.Frame{
			Source:           sourceID,
			SourceGeneration: status.Generation,
			Sequence:         sequence,
			Timecode:         1000 + sequence,
			CapturedAt:       base.Add(time.Duration(sequence) * 20 * time.Millisecond),
			VideoLocked:      true,
			AudioLocked:      true,
		}
		if err := coordinator.PushFrame(event.ID, frame); err != nil {
			return err
		}
	}
	if err := coordinator.ConfigureProgram(event.ID, sourceID); err != nil {
		return err
	}
	if err := coordinator.EstablishInterlocks(event.ID); err != nil {
		return err
	}
	output, ok := runtime.Playout.Output(event.ID)
	if !ok {
		return fmt.Errorf("bootstrap output was not staged")
	}
	if err := coordinator.CommitOnAir(event.ID, output.Fence); err != nil {
		return err
	}
	return nil
}

// bootstrapEmergencySlates 预置受控静态片素材：一张 7×24 小时全天候兜底片，
// 保证外部信号失锁且备路无效时，任何时段都至少有一个合规候选。
func bootstrapEmergencySlates(coordinator *Coordinator) error {
	windows := make([]emergency.TimeWindow, 0, 7)
	for weekday := 0; weekday <= 6; weekday++ {
		windows = append(windows, emergency.TimeWindow{Weekday: weekday, StartMin: 0, EndMin: 24 * 60})
	}
	if _, err := coordinator.RegisterEmergencyAsset(emergency.RegisterInput{
		Name:              "应急静态片-信号故障通告",
		ContentURI:        "slate://emergency/signal-unavailable-1080p50.png",
		Reason:            "外部信号失锁且编码与音画备路均无法提供有效节目时使用",
		ApplicableWindows: windows,
		Control:           "值班导播",
	}); err != nil {
		return err
	}
	return nil
}
