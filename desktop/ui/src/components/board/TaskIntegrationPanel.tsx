import { useEffect, useState } from "react";
import { api, type BoardTask, type TaskIntegrationResponse } from "@/api";
import { IntegrationStatus } from "@/components/board/IntegrationStatus";

const POLL_MS = 15000;

// Columns where the flow is doing something: holding a passing review until the
// integration deploy is green, and landing a signed-off task on the release
// branch. Elsewhere the last known state is still worth showing, but there is
// nothing to poll for.
const LIVE_COLUMNS = new Set(["code_review", "done"]);

interface TaskIntegrationPanelProps {
  task: BoardTask;
  repositoryId: string;
}

/**
 * Where this task stands on the integration and release branches. Renders
 * nothing for a repository that does not run the two-stage flow.
 */
export function TaskIntegrationPanel({ task, repositoryId }: TaskIntegrationPanelProps) {
  const [flow, setFlow] = useState<TaskIntegrationResponse | null>(null);

  useEffect(() => {
    setFlow(null);
    let cancelled = false;
    const load = () =>
      api
        .getTaskIntegration(repositoryId, task.id)
        .then((r) => !cancelled && setFlow(r))
        .catch(() => undefined);
    void load();
    if (!LIVE_COLUMNS.has(task.column)) return () => { cancelled = true; };
    const timer = window.setInterval(() => void load(), POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [repositoryId, task.id, task.column]);

  if (!flow?.enabled || (!flow.integration && !LIVE_COLUMNS.has(task.column))) return null;

  return (
    <IntegrationStatus
      branch={flow.integration_branch ?? ""}
      integration={flow.integration}
      column={task.column}
    />
  );
}
