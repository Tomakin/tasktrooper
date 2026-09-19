package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/makifbaysal/tasktrooper/server/internal/adapter/store/postgres"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

func TestBranchFlowStore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded postgres integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := newTestDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := postgres.NewDB(pool)
	store := postgres.NewBranchFlowStore(db)

	repo, err := postgres.NewRepositoryStore(db).Create(ctx, "flow-test", "", "/tmp/flow-test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFlow(ctx, repo.ID); !errors.Is(err, domain.ErrBranchFlowNotFound) {
		t.Fatalf("no flow yet: %v", err)
	}
	if _, err := store.SetFlow(ctx, domain.BranchFlow{RepositoryID: repo.ID, IntegrationBranch: "development"}); err != nil {
		t.Fatal(err)
	}
	if f, err := store.SetFlow(ctx, domain.BranchFlow{RepositoryID: repo.ID, IntegrationBranch: "dev"}); err != nil || f.IntegrationBranch != "dev" {
		t.Fatalf("upsert: %+v %v", f, err)
	}
	flows, err := store.ListFlows(ctx)
	if err != nil || len(flows) != 1 || flows[0].IntegrationBranch != "dev" {
		t.Fatalf("list: %+v %v", flows, err)
	}
	if _, err := store.SetFlow(ctx, domain.BranchFlow{RepositoryID: uuid.New(), IntegrationBranch: "dev"}); err == nil {
		t.Fatal("a flow for an unknown repository must be refused")
	}

	task, err := postgres.NewBoardTaskStore(db).Create(ctx, domain.BoardTask{
		RepositoryID: repo.ID, Title: "t", Column: domain.TaskColumnHumanUAT, TaskType: domain.TaskTypeTask, Priority: domain.TaskPriorityMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTaskIntegration(ctx, task.ID); !errors.Is(err, domain.ErrTaskIntegrationNotFound) {
		t.Fatalf("no record yet: %v", err)
	}
	merged := time.Now().UTC().Truncate(time.Microsecond)
	in := domain.TaskIntegration{
		TaskID: task.ID, RepositoryID: repo.ID, Branch: "dev", PRNumber: 12, PRURL: "u", HeadSHA: "h",
		MergeSHA: "m", Status: domain.IntegrationMerged, Reason: domain.IntegrationReasonChecksPending, DeployStatus: domain.IntegrationDeployPending, MergedAt: &merged,
	}
	if _, err := store.SaveTaskIntegration(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.DeployStatus = domain.IntegrationDeploySuccess
	in.DeployURL = "run"
	if _, err := store.SaveTaskIntegration(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetTaskIntegration(ctx, task.ID)
	if err != nil || got.DeployStatus != domain.IntegrationDeploySuccess || got.DeployURL != "run" ||
		got.Status != domain.IntegrationMerged || got.Reason != domain.IntegrationReasonChecksPending || got.MergedAt == nil || !got.MergedAt.Equal(merged) || got.PRNumber != 12 {
		t.Fatalf("round trip: %+v %v", got, err)
	}

	if err := store.DeleteFlow(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFlow(ctx, repo.ID); !errors.Is(err, domain.ErrBranchFlowNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
