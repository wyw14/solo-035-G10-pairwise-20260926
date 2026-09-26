package emergency

import (
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/journal"
	"github.com/wyw14/solo-035/internal/model"
)

func newTestCoordinator(t *testing.T, clock func() time.Time, ready bool, handed *bool) (*Coordinator, model.LiveEvent) {
	t.Helper()
	store, err := journal.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	integration := journal.NewIntegration(store, journal.NewState(), clock)
	state := NewState()
	coordinator := NewCoordinator(state, integration, clock,
		func(model.EventID) (TakeoverOutput, bool) {
			if !ready {
				return TakeoverOutput{}, false
			}
			return TakeoverOutput{Source: model.NewSourceID(), Revision: model.NewRevisionID(), Fence: 7}, true
		},
		func(model.LiveEvent, Takeover) error {
			if handed != nil {
				*handed = true
			}
			return nil
		},
	)
	event := model.LiveEvent{ID: model.NewEventID(), Revision: model.NewRevisionID(), Phase: model.PhaseOnAir}
	return coordinator, event
}

func dailyWindows() []TimeWindow {
	windows := make([]TimeWindow, 0, 7)
	for day := 0; day <= 6; day++ {
		windows = append(windows, TimeWindow{Weekday: day, StartMin: 0, EndMin: 24 * 60})
	}
	return windows
}

func TestRegisterAssetRequiresWindowAndControl(t *testing.T) {
	base := time.Unix(1000, 0).UTC()
	coordinator, _ := newTestCoordinator(t, func() time.Time { return base }, true, nil)
	if _, err := coordinator.RegisterAsset(RegisterInput{Name: "无时段", ContentURI: "slate://x"}); err == nil {
		t.Fatal("缺少适用时段应当录入失败")
	}
	if _, err := coordinator.RegisterAsset(RegisterInput{Name: "无责任人", ContentURI: "slate://x", ApplicableWindows: dailyWindows()}); err == nil {
		t.Fatal("缺少责任人应当录入失败")
	}
}

func TestCandidatesRespectApplicableWindow(t *testing.T) {
	mondayNoon := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) // 周一
	clock := func() time.Time { return mondayNoon }
	coordinator, _ := newTestCoordinator(t, clock, true, nil)

	mondayOnly, err := coordinator.RegisterAsset(RegisterInput{
		Name: "周一午间", ContentURI: "slate://monday", Control: "导播",
		ApplicableWindows: []TimeWindow{{Weekday: 1, StartMin: 11 * 60, EndMin: 13 * 60}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := coordinator.RegisterAsset(RegisterInput{
		Name: "全天候兜底", ContentURI: "slate://always", Control: "导播",
		ApplicableWindows: dailyWindows(),
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates := coordinator.Candidates(mondayNoon)
	if len(candidates) != 2 {
		t.Fatalf("候选数量 = %d，期望 2", len(candidates))
	}
	// 限时素材优先，全天候兜底排在最后。
	if candidates[0].Asset.ID != mondayOnly.ID || candidates[1].Asset.ID != fallback.ID {
		t.Fatal("候选排序错误：限时素材应排在全天候兜底之前")
	}

	tuesday := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) // 周二
	candidates = coordinator.Candidates(tuesday)
	if len(candidates) != 1 || candidates[0].Asset.ID != fallback.ID {
		t.Fatalf("周二只应匹配全天候兜底，实际 %d 个", len(candidates))
	}
}

func TestProposeRejectsOutsideWindowAndDoubleTake(t *testing.T) {
	current := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) // 周一
	coordinator, event := newTestCoordinator(t, func() time.Time { return current }, true, nil)
	asset, err := coordinator.RegisterAsset(RegisterInput{
		Name: "周一", ContentURI: "slate://m", Control: "导播",
		ApplicableWindows: []TimeWindow{{Weekday: 1, StartMin: 0, EndMin: 24 * 60}},
	})
	if err != nil {
		t.Fatal(err)
	}
	current = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) // 周二
	if _, err := coordinator.Propose(event, asset.ID, ""); err == nil {
		t.Fatal("素材不在适用时段时不应能发起提案")
	}
	current = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) // 回到周一
	proposal, err := coordinator.Propose(event, asset.ID, "")
	if err != nil {
		t.Fatalf("周一应能提案：%v", err)
	}
	if !proposal.ExpiresAt.After(proposal.ProposedAt) {
		t.Fatal("提案必须带过期时间")
	}
	if _, err := coordinator.ConfirmTake(event, model.NewCommandID()); err == nil {
		t.Fatal("错误的确认令牌必须被拒绝")
	}
	if _, err := coordinator.ConfirmTake(event, proposal.Token); err != nil {
		t.Fatalf("正确令牌应当切入成功：%v", err)
	}
	if _, err := coordinator.Propose(event, asset.ID, ""); err == nil {
		t.Fatal("已在播静态片时不应允许再次提案切入")
	}
}

func TestExpiredProposalCannotBeConfirmed(t *testing.T) {
	current := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	coordinator, event := newTestCoordinator(t, func() time.Time { return current }, true, nil)
	asset, err := coordinator.RegisterAsset(RegisterInput{
		Name: "周一", ContentURI: "slate://m", Control: "导播",
		ApplicableWindows: []TimeWindow{{Weekday: 1, StartMin: 0, EndMin: 24 * 60}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := coordinator.Propose(event, asset.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	current = current.Add(proposalTTL + time.Second)
	if _, err := coordinator.ConfirmTake(event, proposal.Token); err == nil {
		t.Fatal("过期提案不应能被确认切入")
	}
}

func TestFullSlateLifecycleLeavesEvidence(t *testing.T) {
	monday := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	handed := false
	coordinator, event := newTestCoordinator(t, func() time.Time { return monday }, true, &handed)
	asset, err := coordinator.RegisterAsset(RegisterInput{
		Name: "通告", ContentURI: "slate://n", Control: "导播", ApplicableWindows: dailyWindows(),
	})
	if err != nil {
		t.Fatal(err)
	}

	proposal, err := coordinator.Propose(event, asset.ID, "链路中断")
	if err != nil {
		t.Fatal(err)
	}
	takeEvidence, err := coordinator.ConfirmTake(event, proposal.Token)
	if err != nil {
		t.Fatalf("切入失败：%v", err)
	}
	if takeEvidence.Kind != EvidenceKindTake || takeEvidence.AssetID != asset.ID {
		t.Fatal("切入证据内容不正确")
	}

	// 接管输出未撤下前不能准备；先撤下。
	if _, err := coordinator.PrepareTakeover(event, ""); err == nil {
		t.Fatal("未撤下静态片时不应能准备接管输出")
	}
	withdrawEvidence, err := coordinator.Withdraw(event, "")
	if err != nil {
		t.Fatalf("撤下失败：%v", err)
	}
	if withdrawEvidence.Kind != EvidenceKindWithdraw {
		t.Fatal("撤下证据类型不正确")
	}

	takeover, err := coordinator.PrepareTakeover(event, "")
	if err != nil {
		t.Fatalf("准备接管失败：%v", err)
	}
	if _, err := coordinator.ConfirmHandover(event, string(model.NewCommandID())); err == nil {
		t.Fatal("未准备的接管 ID 必须被拒绝")
	}
	handoverEvidence, err := coordinator.ConfirmHandover(event, takeover.ID)
	if err != nil {
		t.Fatalf("交接失败：%v", err)
	}
	if !handed {
		t.Fatal("交接时必须实际执行接管上播动作")
	}
	if handoverEvidence.Kind != EvidenceKindHandover || handoverEvidence.TakeoverID != takeover.ID {
		t.Fatal("交接证据内容不正确")
	}

	evidence := coordinator.state.Evidence(event.ID)
	if len(evidence) != 3 {
		t.Fatalf("应留下切入/撤下/交接三条证据，实际 %d", len(evidence))
	}
	session, _ := coordinator.state.Session(event.ID)
	if session.Status != StatusHandedOver || session.ActiveAsset != nil {
		t.Fatal("交接后会话应处于 handed-over 且无在播静态片")
	}
}

func TestPrepareTakeoverRejectsUnreadyOutput(t *testing.T) {
	monday := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	coordinator, event := newTestCoordinator(t, func() time.Time { return monday }, false, nil)
	asset, err := coordinator.RegisterAsset(RegisterInput{
		Name: "通告", ContentURI: "slate://n", Control: "导播", ApplicableWindows: dailyWindows(),
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := coordinator.Propose(event, asset.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ConfirmTake(event, proposal.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Withdraw(event, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.PrepareTakeover(event, ""); err == nil {
		t.Fatal("接管输出未准备好时必须拒绝准备")
	}
}
