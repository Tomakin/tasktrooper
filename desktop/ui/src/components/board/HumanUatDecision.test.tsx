import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BoardTask, TaskIntegrationResponse } from "@/api";
import { HumanUatDecision } from "@/components/board/HumanUatDecision";
import { BranchFlowCard } from "@/components/projects/BranchFlowCard";
import { I18nProvider } from "@/hooks/useI18n";

const { getTaskIntegration, releaseTask, updateRepositoryTask, getBranchFlow, setBranchFlow } = vi.hoisted(() => ({
  getTaskIntegration: vi.fn(),
  releaseTask: vi.fn(),
  updateRepositoryTask: vi.fn(),
  getBranchFlow: vi.fn(),
  setBranchFlow: vi.fn(),
}));

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return {
    ...actual,
    api: { ...actual.api, getTaskIntegration, releaseTask, updateRepositoryTask, getBranchFlow, setBranchFlow },
  };
});

const task: BoardTask = {
  id: "task-1",
  repository_id: "repo-1",
  key: "T-7",
  task_number: 7,
  title: "A change",
  task_type: "task",
  description: "",
  technical_description: "",
  column: "human_uat",
  position: 0,
  priority: "medium",
  created_by: "human",
  created_at: "2026-09-19T00:00:00Z",
  updated_at: "2026-09-19T00:00:00Z",
};

function flow(overrides: Partial<NonNullable<TaskIntegrationResponse["integration"]>> | null): TaskIntegrationResponse {
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
            deploy_status: "success",
            deploy_url: "https://github.com/acme/app/actions/runs/1",
            updated_at: "2026-09-19T00:00:00Z",
            ...overrides,
          },
  };
}

function renderDecision() {
  const onUpdated = vi.fn();
  render(
    <I18nProvider>
      <HumanUatDecision task={task} repositoryId="repo-1" onUpdated={onUpdated} />
    </I18nProvider>,
  );
  return onUpdated;
}

describe("HumanUatDecision with the two-stage delivery", () => {
  beforeEach(() => {
    getTaskIntegration.mockReset();
    releaseTask.mockReset();
    updateRepositoryTask.mockReset();
  });

  it("keeps the plain approve when the repository has no flow", async () => {
    getTaskIntegration.mockResolvedValue({ enabled: false });
    renderDecision();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() => expect(updateRepositoryTask).toHaveBeenCalledWith("repo-1", "task-1", { column: "done" }));
    expect(releaseTask).not.toHaveBeenCalled();
  });

  it("releases through the server once the change is merged and deployed", async () => {
    getTaskIntegration.mockResolvedValue(flow({}));
    releaseTask.mockResolvedValue({ task: { ...task, column: "done" }, merge: { merged: true } });
    const onUpdated = renderDecision();

    expect(await screen.findByText("Deploy succeeded")).toBeInTheDocument();
    expect(screen.getByText("Test environment (development)")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Passed, release to production" });
    expect(button).toBeEnabled();
    fireEvent.click(button);
    await waitFor(() => expect(releaseTask).toHaveBeenCalledWith("repo-1", "task-1"));
    await waitFor(() => expect(onUpdated).toHaveBeenCalled());
    expect(updateRepositoryTask).not.toHaveBeenCalled();
  });

  it("explains a stuck merge in the UI's language, not the server's", async () => {
    getTaskIntegration.mockResolvedValue(
      flow({ status: "failed", reason: "no_token", detail: "GitHub is not connected (server text)", deploy_status: "" }),
    );
    renderDecision();
    expect(await screen.findByText(/GitHub is not connected, so the change cannot be merged into development/)).toBeInTheDocument();
    expect(screen.queryByText("GitHub is not connected (server text)")).toBeNull();
  });

  it("holds the release while the deploy runs or after it failed", async () => {
    getTaskIntegration.mockResolvedValue(flow({ deploy_status: "failure" }));
    renderDecision();
    expect(await screen.findByText("Deploy failed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Passed, release to production" })).toBeDisabled();
    expect(screen.getByText(/Release is available once the change is merged into development/)).toBeInTheDocument();
  });

  it("says the merge is on its way before the server has a record", async () => {
    getTaskIntegration.mockResolvedValue(flow(null));
    renderDecision();
    expect(await screen.findByText(/Being merged into development/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Passed, release to production" })).toBeDisabled();
  });
});

describe("BranchFlowCard", () => {
  it("turns the flow on with the typed branch and off again", async () => {
    getBranchFlow.mockResolvedValue({ enabled: false, integration_branch: "" });
    setBranchFlow.mockResolvedValueOnce({ enabled: true, integration_branch: "development" });
    setBranchFlow.mockResolvedValueOnce({ enabled: false, integration_branch: "" });
    render(
      <I18nProvider>
        <BranchFlowCard repositoryId="repo-1" />
      </I18nProvider>,
    );
    fireEvent.change(await screen.findByLabelText("Integration branch"), { target: { value: " development " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("On: development → default branch")).toBeInTheDocument();
    expect(setBranchFlow).toHaveBeenCalledWith("repo-1", "development");

    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(await screen.findByText("Off")).toBeInTheDocument();
    expect(setBranchFlow).toHaveBeenLastCalledWith("repo-1", "");
  });
});
