-- Two-stage delivery (development → main) per repository, and where each task
-- stands on the integration branch. Separate tables rather than columns on
-- repositories/board_tasks: no row means the ordinary one-stage flow.
CREATE TABLE IF NOT EXISTS repository_branch_flow (
    repository_id      UUID PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
    integration_branch TEXT NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS task_integration (
    task_id       UUID PRIMARY KEY REFERENCES board_tasks(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL,
    branch        TEXT NOT NULL DEFAULT '',
    pr_number     INTEGER NOT NULL DEFAULT 0,
    pr_url        TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    merge_sha     TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    reason        TEXT NOT NULL DEFAULT '',
    detail        TEXT NOT NULL DEFAULT '',
    deploy_status TEXT NOT NULL DEFAULT '',
    deploy_url    TEXT NOT NULL DEFAULT '',
    merged_at     TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
