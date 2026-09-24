package catalog

import (
	"strings"
	"testing"
)

// The board dispatches a card to its assignee; with the default-assignee
// setting pointed at the architect, unowned work lands there first. Without a
// rule that says what to do with it, the architect reads its own
// "no feature code" rule, writes a comment and stops — which is where the card
// stayed the first time this was tried.
func TestArchitectTriagesWorkItIsHandedInTodo(t *testing.T) {
	var triage string
	for _, r := range systemArchitectAgent().rules {
		if r.Name == "architect-triages-new-work" {
			triage = r.Content
		}
	}
	if triage == "" {
		t.Fatal("the architect has no triage rule")
	}
	for _, want := range []string{"todo", "update_board_task", "list_team", "never implement"} {
		if !strings.Contains(triage, want) {
			t.Errorf("the triage rule does not say %q: %s", want, triage)
		}
	}
	if !strings.Contains(triage, "never leave it assigned to yourself") {
		t.Error("the rule must forbid the card resting with the architect")
	}
}
