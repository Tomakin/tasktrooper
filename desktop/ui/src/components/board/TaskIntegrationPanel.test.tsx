import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BoardTask, TaskIntegrationResponse } from "@/api";
import { TaskIntegrationPanel } from "@/components/board/TaskIntegrationPanel";
import { I18nProvider } from "@/hooks/useI18n";

const { getTaskIntegration } = vi.hoisted(() => ({ getTaskIntegration: vi.fn() }));

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return { ...actual, api: { ...actual.api, getTaskIntegration } };
});

function task(column: BoardTask["column"]): BoardTask {
  return {
    id: "task-1",
    repository_id: "repo-1",
    key: "T-7",
    task_number: 7,
    title: "A change",
    task_type: "task",
    description: "",
    technical_description: "",
    column,
    position: 0,
    priority: "medium",
    created_by: "human",
    created_at: "2026-09-22T00:00:00Z",
    updated_at: "2026-09-22T00:00:00Z",
  };
}

function answer(overrides: Partial<NonNullable<TaskIntegrationResponse["integration"]>> | null): TaskIntegrationResponse {
  return {
    enabled: true,
    integration_branch: "development",
    integration:
      overrides === null
        ? undefined
        : {
            task_id: "task-1",
            branch: "development",
            pr_number: 12,
            pr_url: "https://github.com/acme/app/pull/12",
            merge_sha: "abcdef1234567890",
            status: "merged",
            deploy_status: "pending",
            updated_at: "2026-09-22T00:00:00Z",
            ...overrides,
          },
  };
}

function renderPanel(column: BoardTask["column"]) {
  render(
    <I18nProvider>
      <TaskIntegrationPanel task={task(column)} repositoryId="repo-1" />
    </I18nProvider>,
  );
}

describe("TaskIntegrationPanel", () => {
  beforeEach(() => getTaskIntegration.mockReset());

  it("shows the integration deploy while the review is held", async () => {
    getTaskIntegration.mockResolvedValue(answer({}));
    renderPanel("code_review");
    expect(await screen.findByText("Test environment (development)")).toBeInTheDocument();
    expect(screen.getByText("Deploy running")).toBeInTheDocument();
  });

  it("says the merge has not started yet", async () => {
    getTaskIntegration.mockResolvedValue(answer(null));
    renderPanel("code_review");
    expect(await screen.findByText(/Waiting for the merge into development/)).toBeInTheDocument();
  });

  it("switches to the release view in done", async () => {
    getTaskIntegration.mockResolvedValue(answer({ release_deploy_status: "success" }));
    renderPanel("done");
    expect(await screen.findByText("Release")).toBeInTheDocument();
    expect(screen.getByText("Deploy succeeded")).toBeInTheDocument();
  });

  it("renders nothing for a repository without the flow", async () => {
    getTaskIntegration.mockResolvedValue({ enabled: false });
    const { container } = render(
      <I18nProvider>
        <TaskIntegrationPanel task={task("in_qa")} repositoryId="repo-1" />
      </I18nProvider>,
    );
    await waitFor(() => expect(container.textContent).toBe(""));
  });
});
