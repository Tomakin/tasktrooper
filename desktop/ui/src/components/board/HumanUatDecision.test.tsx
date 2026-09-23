import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BoardTask } from "@/api";
import { HumanUatDecision } from "@/components/board/HumanUatDecision";
import { BranchFlowCard } from "@/components/projects/BranchFlowCard";
import { I18nProvider } from "@/hooks/useI18n";

const { updateRepositoryTask, getBranchFlow, setBranchFlow } = vi.hoisted(() => ({
  updateRepositoryTask: vi.fn(),
  getBranchFlow: vi.fn(),
  setBranchFlow: vi.fn(),
}));

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return {
    ...actual,
    api: { ...actual.api, updateRepositoryTask, getBranchFlow, setBranchFlow },
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

describe("HumanUatDecision", () => {
  beforeEach(() => {
    updateRepositoryTask.mockReset();
  });

  it("approves straight into done; the flow takes it from there", async () => {
    const onUpdated = vi.fn();
    render(
      <I18nProvider>
        <HumanUatDecision task={task} repositoryId="repo-1" onUpdated={onUpdated} />
      </I18nProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() => expect(updateRepositoryTask).toHaveBeenCalledWith("repo-1", "task-1", { column: "done" }));
    expect(onUpdated).toHaveBeenCalled();
  });
});

describe("BranchFlowCard", () => {
  it("turns the flow on with the typed branch and off again", async () => {
    getBranchFlow.mockResolvedValue({ enabled: false, integration_branch: "", release_branch: "" });
    setBranchFlow.mockResolvedValueOnce({ enabled: true, integration_branch: "development", release_branch: "master" });
    setBranchFlow.mockResolvedValueOnce({ enabled: false, integration_branch: "", release_branch: "" });
    render(
      <I18nProvider>
        <BranchFlowCard repositoryId="repo-1" />
      </I18nProvider>,
    );
    fireEvent.change(await screen.findByLabelText("Integration branch"), { target: { value: " development " } });
    fireEvent.change(screen.getByLabelText("Release branch"), { target: { value: " master " } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("On: development → master")).toBeInTheDocument();
    expect(setBranchFlow).toHaveBeenCalledWith("repo-1", "development", "master");

    fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
    expect(await screen.findByText("Off")).toBeInTheDocument();
    expect(setBranchFlow).toHaveBeenLastCalledWith("repo-1", "", "");
  });
});
