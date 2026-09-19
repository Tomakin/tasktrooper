package runtime

import (
	"testing"

	"github.com/makifbaysal/tasktrooper/server/internal/adapter/agentcli/claudecode"
)

func TestInitialSessionLimit(t *testing.T) {
	cases := map[int]int{0: claudecode.DefaultMaxConcurrentSessions, -1: 0, 1: 1, 7: 7}
	for in, want := range cases {
		if got := initialSessionLimit(in); got != want {
			t.Errorf("initialSessionLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
