package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

type BranchFlowStore struct {
	pool *DB
}

func NewBranchFlowStore(pool *DB) *BranchFlowStore {
	return &BranchFlowStore{pool: pool}
}

func (s *BranchFlowStore) GetFlow(ctx context.Context, repositoryID uuid.UUID) (domain.BranchFlow, error) {
	var f domain.BranchFlow
	err := s.pool.QueryRow(ctx, `
		SELECT repository_id, integration_branch, updated_at
		FROM repository_branch_flow WHERE repository_id = $1
	`, repositoryID).Scan(&f.RepositoryID, &f.IntegrationBranch, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BranchFlow{}, domain.ErrBranchFlowNotFound
	}
	if err != nil {
		return domain.BranchFlow{}, fmt.Errorf("get branch flow: %w", err)
	}
	return f, nil
}

func (s *BranchFlowStore) ListFlows(ctx context.Context) ([]domain.BranchFlow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT repository_id, integration_branch, updated_at FROM repository_branch_flow
	`)
	if err != nil {
		return nil, fmt.Errorf("list branch flows: %w", err)
	}
	defer rows.Close()
	var out []domain.BranchFlow
	for rows.Next() {
		var f domain.BranchFlow
		if err := rows.Scan(&f.RepositoryID, &f.IntegrationBranch, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *BranchFlowStore) SetFlow(ctx context.Context, f domain.BranchFlow) (domain.BranchFlow, error) {
	err := s.pool.QueryRow(ctx, `
		INSERT INTO repository_branch_flow (repository_id, integration_branch, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (repository_id) DO UPDATE
		SET integration_branch = EXCLUDED.integration_branch, updated_at = now()
		RETURNING repository_id, integration_branch, updated_at
	`, f.RepositoryID, f.IntegrationBranch).Scan(&f.RepositoryID, &f.IntegrationBranch, &f.UpdatedAt)
	if err != nil {
		return domain.BranchFlow{}, fmt.Errorf("set branch flow: %w", err)
	}
	return f, nil
}

func (s *BranchFlowStore) DeleteFlow(ctx context.Context, repositoryID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM repository_branch_flow WHERE repository_id = $1`, repositoryID); err != nil {
		return fmt.Errorf("delete branch flow: %w", err)
	}
	return nil
}

const taskIntegrationColumns = `task_id, repository_id, branch, pr_number, pr_url, head_sha, merge_sha,
	status, reason, detail, deploy_status, deploy_url, merged_at, updated_at`

func scanTaskIntegration(row pgx.Row) (domain.TaskIntegration, error) {
	var t domain.TaskIntegration
	var status, reason, deployStatus string
	err := row.Scan(&t.TaskID, &t.RepositoryID, &t.Branch, &t.PRNumber, &t.PRURL, &t.HeadSHA, &t.MergeSHA,
		&status, &reason, &t.Detail, &deployStatus, &t.DeployURL, &t.MergedAt, &t.UpdatedAt)
	t.Status = domain.IntegrationStatus(status)
	t.Reason = domain.IntegrationReason(reason)
	t.DeployStatus = domain.IntegrationDeployStatus(deployStatus)
	return t, err
}

func (s *BranchFlowStore) GetTaskIntegration(ctx context.Context, taskID uuid.UUID) (domain.TaskIntegration, error) {
	t, err := scanTaskIntegration(s.pool.QueryRow(ctx,
		`SELECT `+taskIntegrationColumns+` FROM task_integration WHERE task_id = $1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TaskIntegration{}, domain.ErrTaskIntegrationNotFound
	}
	if err != nil {
		return domain.TaskIntegration{}, fmt.Errorf("get task integration: %w", err)
	}
	return t, nil
}

func (s *BranchFlowStore) SaveTaskIntegration(ctx context.Context, in domain.TaskIntegration) (domain.TaskIntegration, error) {
	out, err := scanTaskIntegration(s.pool.QueryRow(ctx, `
		INSERT INTO task_integration (task_id, repository_id, branch, pr_number, pr_url, head_sha, merge_sha,
			status, reason, detail, deploy_status, deploy_url, merged_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		ON CONFLICT (task_id) DO UPDATE SET
			repository_id = EXCLUDED.repository_id, branch = EXCLUDED.branch,
			pr_number = EXCLUDED.pr_number, pr_url = EXCLUDED.pr_url,
			head_sha = EXCLUDED.head_sha, merge_sha = EXCLUDED.merge_sha,
			status = EXCLUDED.status, reason = EXCLUDED.reason, detail = EXCLUDED.detail,
			deploy_status = EXCLUDED.deploy_status, deploy_url = EXCLUDED.deploy_url,
			merged_at = EXCLUDED.merged_at, updated_at = now()
		RETURNING `+taskIntegrationColumns,
		in.TaskID, in.RepositoryID, in.Branch, in.PRNumber, in.PRURL, in.HeadSHA, in.MergeSHA,
		string(in.Status), string(in.Reason), in.Detail, string(in.DeployStatus), in.DeployURL, in.MergedAt))
	if err != nil {
		return domain.TaskIntegration{}, fmt.Errorf("save task integration: %w", err)
	}
	return out, nil
}
