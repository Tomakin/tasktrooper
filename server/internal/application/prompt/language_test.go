package prompt

import (
	"strings"
	"testing"
)

// The board's own scaffolding is English, so an agent told only to answer in
// Turkish still filed criteria as "Given … when … then" with Turkish inside
// them. The instruction has to name what a person reads — and what stays
// English regardless.
func TestLanguageInstructionCoversEverythingAPersonReads(t *testing.T) {
	got := LanguageInstruction("tr")
	name := LocaleDisplayName("tr")
	if !strings.Contains(got, "Respond in "+name) {
		t.Fatalf("no respond-in clause: %s", got)
	}
	for _, want := range []string{"card comments", "task titles", "acceptance criteria", "never mixed with English"} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction does not name %q:\n%s", want, got)
		}
	}
	for _, want := range []string{"commit messages", "branch names", "pull-request titles", "stay in English"} {
		if !strings.Contains(got, want) {
			t.Errorf("the English-history exception lost %q:\n%s", want, got)
		}
	}
	if strings.Count(got, name) < 3 {
		t.Errorf("the language is not named in every clause: %s", got)
	}
}
