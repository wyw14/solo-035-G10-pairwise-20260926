package service

import (
	"testing"
	"time"

	"github.com/wyw14/solo-035/internal/model"
	"github.com/wyw14/solo-035/internal/slate"
)

func TestSlateEmergencyLifecycle(t *testing.T) {
	runtime, err := NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(runtime)
	event := runtime.Events.List()[0]
	output, ok := runtime.PlayoutState.Output(event.ID)
	if !ok {
		t.Fatal("bootstrap playout output missing")
	}

	material, err := runtime.Slate.RegisterMaterial(slate.Material{
		Name:         "信号中断通用静态片",
		ContentURI:   "slate://controlled/generic-interstitial.png",
		Checksum:     "sha256:7d865e959b2466918c9863afca942d0f",
		ControlledBy: "素材管理员-何岚",
	})
	if err != nil {
		t.Fatal(err)
	}

	if view := runtime.Slate.Status(event.ID); !view.Chain.Available {
		t.Fatalf("chain should be available after bootstrap: %s", view.Chain.Reason)
	}

	// 外部信号失锁：开始新一代切换且尚未重新锁定
	unlocked := runtime.SourceFlow.Switch(output.Source)
	if view := runtime.Slate.Status(event.ID); view.Chain.Available {
		t.Fatal("chain must be unavailable once the external signal loses lock")
	}

	// 链路仍可用时禁止提案的反向保证已在 api 测试覆盖；此处应急已成立
	if _, err := coordinator.ProposeSlate(event.ID, "external signal unlocked", ""); err == nil {
		t.Fatal("proposal without operator identity must be rejected")
	}
	proposal, err := coordinator.ProposeSlate(event.ID, "external signal unlocked", "值班导播-赵安")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != slate.StatusProposed || len(proposal.Candidates) != 1 || proposal.Candidates[0] != material.ID {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	if _, err := coordinator.ProposeSlate(event.ID, "again", "值班导播-赵安"); err == nil {
		t.Fatal("a second proposal while one is active must be rejected")
	}

	if _, err := coordinator.ConfirmSlateCutIn(event.ID, material.ID, model.NewCommandID(), "值班导播-赵安"); err == nil {
		t.Fatal("cut-in with a wrong confirmation token must be rejected")
	}
	if _, err := coordinator.ConfirmSlateCutIn(event.ID, material.ID, proposal.Command, "  "); err == nil {
		t.Fatal("cut-in without confirming operator must be rejected")
	}
	if _, err := coordinator.ConfirmSlateCutIn(event.ID, "missing-material", proposal.Command, "值班导播-赵安"); err == nil {
		t.Fatal("cut-in with a non-candidate material must be rejected")
	}

	onAir, err := coordinator.ConfirmSlateCutIn(event.ID, material.ID, proposal.Command, "值班导播-赵安")
	if err != nil {
		t.Fatal(err)
	}
	if onAir.Status != slate.StatusOnAir || onAir.CutInAt == nil || onAir.ConfirmedBy != "值班导播-赵安" {
		t.Fatalf("slate should be on air after confirmation: %+v", onAir)
	}

	// 信号重新锁定：主路成为已准备好的接管输出
	if err := runtime.SourceFlow.Confirm(output.Source, unlocked.CurrentCommand, true, true); err != nil {
		t.Fatal(err)
	}
	view := runtime.Slate.Status(event.ID)
	var primary *slate.TakeoverOption
	for i := range view.Takeovers {
		if view.Takeovers[i].ID == slate.TakeoverPrimary {
			primary = &view.Takeovers[i]
		}
	}
	if primary == nil || !primary.Ready {
		t.Fatalf("primary takeover output should be ready after relock: %+v", view.Takeovers)
	}

	if _, err := coordinator.CompleteSlateHandover(event.ID, slate.TakeoverPrimary, " "); err == nil {
		t.Fatal("handover without director identity must be rejected")
	}
	if _, err := coordinator.CompleteSlateHandover(event.ID, slate.TakeoverBackup, "总导播-周明"); err == nil {
		t.Fatal("handover to a not-ready backup output must be rejected")
	}

	handed, err := coordinator.CompleteSlateHandover(event.ID, slate.TakeoverPrimary, "总导播-周明")
	if err != nil {
		t.Fatal(err)
	}
	if handed.Status != slate.StatusHandedOver || handed.HandedOverAt == nil || handed.Director != "总导播-周明" {
		t.Fatalf("slate should be handed over: %+v", handed)
	}

	evidence := runtime.SlateEvidence(event.ID)
	wantKinds := []string{slate.KindCutIn, slate.KindHandover, slate.KindWithdrawn}
	if len(evidence) != len(wantKinds) {
		t.Fatalf("evidence count = %d, want %d", len(evidence), len(wantKinds))
	}
	for i, kind := range wantKinds {
		if evidence[i].Kind != kind {
			t.Fatalf("evidence[%d] = %s, want %s", i, evidence[i].Kind, kind)
		}
	}

	report := runtime.Diagnostics()
	for _, check := range report.Checks {
		if check.Name == "slate-evidence" && !check.Passed {
			t.Fatalf("slate evidence diagnostic failed: %s", check.Detail)
		}
	}
}

func TestSlateMaterialWindowAndRecovery(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	runtime, err := NewRuntime(t.TempDir(), func() time.Time { return base })
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	event := runtime.Events.List()[0]
	output, _ := runtime.PlayoutState.Output(event.ID)
	runtime.SourceFlow.Switch(output.Source)

	if _, err := runtime.Slate.RegisterMaterial(slate.Material{
		Name: "未来时段静态片", ContentURI: "slate://future.png", Checksum: "c1", ControlledBy: "admin",
		AvailableFrom: base.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinatorPropose(runtime, event.ID); err == nil {
		t.Fatal("no candidate should be available inside an empty window")
	}

	current, err := runtime.Slate.RegisterMaterial(slate.Material{
		Name: "当前时段静态片", ContentURI: "slate://current.png", Checksum: "c2", ControlledBy: "admin",
		AvailableFrom: base.Add(-time.Minute), AvailableTo: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := coordinatorPropose(runtime, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Candidates) != 1 || proposal.Candidates[0] != current.ID {
		t.Fatalf("only the in-window material should be a candidate: %+v", proposal.Candidates)
	}
	if _, err := NewCoordinator(runtime).ConfirmSlateCutIn(event.ID, current.ID, proposal.Command, "op"); err != nil {
		t.Fatal(err)
	}

	checkpoint := runtime.ExportCheckpoint()
	if len(checkpoint.SlateMaterials) != 2 || len(checkpoint.Slate) != 1 {
		t.Fatalf("checkpoint slate export = %d materials, %d emergencies", len(checkpoint.SlateMaterials), len(checkpoint.Slate))
	}

	restored, err := NewRuntime(t.TempDir(), func() time.Time { return base })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(restored.SlateMaterials()) != 2 {
		t.Fatalf("restored materials = %d", len(restored.SlateMaterials()))
	}
	emergencies := restored.SlateEmergencies()
	if len(emergencies) != 1 || emergencies[0].Status != slate.StatusOnAir {
		t.Fatalf("restored emergency = %+v", emergencies)
	}
}

func coordinatorPropose(runtime *Runtime, eventID model.EventID) (slate.Emergency, error) {
	return NewCoordinator(runtime).ProposeSlate(eventID, "signal lost", "值班导播-赵安")
}
