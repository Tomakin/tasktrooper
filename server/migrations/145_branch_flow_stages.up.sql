-- The two-stage flow moved: the integration merge now happens on the way out of
-- code_review (promote_to records where the held card goes once its deploy is
-- green), and the release merge happens in done, watched to released.
ALTER TABLE repository_branch_flow ADD COLUMN IF NOT EXISTS release_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE task_integration ADD COLUMN IF NOT EXISTS promote_to TEXT NOT NULL DEFAULT '';
ALTER TABLE task_integration ADD COLUMN IF NOT EXISTS release_deploy_status TEXT NOT NULL DEFAULT '';
ALTER TABLE task_integration ADD COLUMN IF NOT EXISTS release_deploy_url TEXT NOT NULL DEFAULT '';
