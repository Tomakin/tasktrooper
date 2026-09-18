package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/makifbaysal/tasktrooper/server/internal/application/workspace"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func TestApplyLocalOverridesWidensAllowedRoots(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	repo := filepath.Join(home, "project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &domain.Config{}
	cfg.Indexer.AllowedRoots = []string{"/from/config"}
	applyLocalOverrides(cfg, Options{DataDir: dataDir, AllowedRoots: []string{home}})

	if len(cfg.Indexer.AllowedRoots) != 2 || cfg.Indexer.AllowedRoots[0] != "/from/config" || cfg.Indexer.AllowedRoots[1] != home {
		t.Fatalf("allowed roots = %q", cfg.Indexer.AllowedRoots)
	}
	if _, err := workspace.ValidateProjectRoot(repo, cfg.Storage.Sessions.WorkspaceRoot, cfg.Indexer.AllowedRoots); err != nil {
		t.Fatalf("a folder under the env-supplied root was refused: %v", err)
	}
}

// Without the env var nothing outside the managed workspace is reachable:
// the override must widen, never replace, and never default to "anywhere".
func TestApplyLocalOverridesWithoutAllowedRootsKeepsWorkspaceOnly(t *testing.T) {
	cfg := &domain.Config{}
	applyLocalOverrides(cfg, Options{DataDir: t.TempDir()})

	if _, err := workspace.ValidateProjectRoot(t.TempDir(), cfg.Storage.Sessions.WorkspaceRoot, cfg.Indexer.AllowedRoots); err == nil {
		t.Fatal("a folder outside the workspace was accepted with no allowed roots")
	}
}
