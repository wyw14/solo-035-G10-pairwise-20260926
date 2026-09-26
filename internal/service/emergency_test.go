package service

import (
	"context"
	"testing"

	"github.com/wyw14/solo-035/internal/emergency"
	"github.com/wyw14/solo-035/internal/model"
)

func control(t *testing.T, runtime *Runtime, request ControlRequest) any {
	t.Helper()
	result, err := runtime.ExecuteControl(context.Background(), request)
	if err != nil {
		t.Fatalf("control %s failed: %v", request.Action, err)
	}
	return result.Value
}

func mustControlErr(t *testing.T, runtime *Runtime, request ControlRequest) error {
	t.Helper()
	_, err := runtime.ExecuteControl(context.Background(), request)
	if err == nil {
		t.Fatalf("control %s 应当失败但成功了", request.Action)
	}
	return err
}

func TestEmergencySlateEndToEnd(t *testing.T) {
	runtime, err := NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	events := runtime.Events.List()
	if len(events) != 1 {
		t.Fatalf("事件数 = %d", len(events))
	}
	eventID := events[0].ID
	coordinator := NewCoordinator(runtime)

	// bootstrap 应预置一张全天候兜底受控素材。
	assets := runtime.EmergencyAssets()
	if len(assets) != 1 {
		t.Fatalf("预置素材数 = %d，期望 1", len(assets))
	}
	fallback := assets[0]

	// 初始链路在播、信号锁定，应急提案必须被拒绝。
	overview := coordinator.EmergencyOverview(eventID)
	if !overview.Chain.Available {
		t.Fatal("初始播出链路应当可用")
	}
	mustControlErr(t, runtime, ControlRequest{Action: "propose-slate", EventID: string(eventID), SlateID: string(fallback.ID)})

	// 模拟切换操作仍无法保证屏幕有内容：播出输出停止（信号失锁且备路无效）。
	control(t, runtime, ControlRequest{Action: "stop-program", EventID: string(eventID)})
	overview = coordinator.EmergencyOverview(eventID)
	if overview.Chain.Available {
		t.Fatal("输出停止后链路应当判定为不可用")
	}
	if len(overview.Candidates) != 1 || overview.Candidates[0].Asset.ID != fallback.ID {
		t.Fatal("链路不可用时应给出全天候兜底候选")
	}

	// 提案 -> 明确确认 -> 切入。
	proposalValue := control(t, runtime, ControlRequest{Action: "propose-slate", EventID: string(eventID), SlateID: string(fallback.ID), Message: "输出停止"})
	proposal, ok := proposalValue.(emergency.Proposal)
	if !ok {
		t.Fatalf("提案类型错误: %T", proposalValue)
	}
	mustControlErr(t, runtime, ControlRequest{Action: "confirm-slate", EventID: string(eventID), Token: model.NewCommandID()})
	takeValue := control(t, runtime, ControlRequest{Action: "confirm-slate", EventID: string(eventID), Token: proposal.Token})
	takeEvidence := takeValue.(emergency.Evidence)
	if takeEvidence.Kind != emergency.EvidenceKindTake {
		t.Fatal("应当留下切入证据")
	}

	// 撤下。
	withdrawValue := control(t, runtime, ControlRequest{Action: "withdraw-slate", EventID: string(eventID)})
	if withdrawValue.(emergency.Evidence).Kind != emergency.EvidenceKindWithdraw {
		t.Fatal("应当留下撤下证据")
	}

	// 导播选择并准备接管输出，再明确确认交接。
	takeoverValue := control(t, runtime, ControlRequest{Action: "prepare-takeover", EventID: string(eventID), Label: "恢复在播节目"})
	takeover := takeoverValue.(emergency.Takeover)
	handoverValue := control(t, runtime, ControlRequest{Action: "confirm-handover", EventID: string(eventID), TakeoverID: takeover.ID})
	if handoverValue.(emergency.Evidence).Kind != emergency.EvidenceKindHandover {
		t.Fatal("应当留下交接证据")
	}

	// 交接后接管输出重新在播。
	output, ok := runtime.Playout.Output(eventID)
	if !ok || !output.OnAir {
		t.Fatal("交接后接管输出应当恢复在播")
	}

	// 三类实际播出证据齐全，且已持久化到 journal。
	evidence := runtime.EmergencyState.Evidence(eventID)
	if len(evidence) != 3 {
		t.Fatalf("播出证据 = %d，期望 3", len(evidence))
	}
	kinds := map[string]bool{}
	for _, record := range runtime.Journal.Records() {
		kinds[record.Kind] = true
	}
	for _, kind := range []string{emergency.EvidenceKindTake, emergency.EvidenceKindWithdraw, emergency.EvidenceKindHandover} {
		if !kinds[kind] {
			t.Fatalf("journal 缺少 %s 播出证据", kind)
		}
	}
}

func TestEmergencyCheckpointRestoresAssetsAndSessions(t *testing.T) {
	runtime, err := NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	checkpoint := runtime.ExportCheckpoint()
	if len(checkpoint.SlateAssets) != 1 {
		t.Fatalf("检查点素材数 = %d", len(checkpoint.SlateAssets))
	}

	fresh, err := NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatalf("恢复检查点失败: %v", err)
	}
	if len(fresh.EmergencyAssets()) != 1 {
		t.Fatalf("恢复后素材数 = %d，期望 1", len(fresh.EmergencyAssets()))
	}
}
