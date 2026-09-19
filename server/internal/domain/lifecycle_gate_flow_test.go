package domain

import "testing"

func TestReviewChainForFlowSwapsPMForHumanUAT(t *testing.T) {
	plain := ReviewChainForFlow(TaskTypeTask, false)
	flow := ReviewChainForFlow(TaskTypeTask, true)
	if len(plain) != len(flow) {
		t.Fatalf("stage count changed: %d vs %d", len(plain), len(flow))
	}
	has := func(stages []ReviewStage, col TaskColumn) bool {
		for _, s := range stages {
			if s.Column == col {
				return true
			}
		}
		return false
	}
	if !has(plain, TaskColumnPMUAT) || has(plain, TaskColumnHumanUAT) {
		t.Errorf("the ordinary chain changed: %+v", plain)
	}
	if has(flow, TaskColumnPMUAT) || !has(flow, TaskColumnHumanUAT) || !has(flow, TaskColumnCodeReview) || !has(flow, TaskColumnInQA) {
		t.Errorf("flow chain: %+v", flow)
	}
	if again := ReviewChainForType(TaskTypeTask); !has(again, TaskColumnPMUAT) {
		t.Error("ReviewChainForFlow must not mutate the shared chain")
	}
	if a := ReviewChainForFlow(TaskTypeAnaliz, true); len(a) != 1 || a[0].Column != TaskColumnAnalizReview {
		t.Errorf("analiz chain: %+v", a)
	}
}
