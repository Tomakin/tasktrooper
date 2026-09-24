import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { DefaultAssigneeCard } from "@/components/admin/DefaultAssigneeCard";
import { I18nProvider } from "@/hooks/useI18n";

const { getSettings, listAgents, updateSettings } = vi.hoisted(() => ({
  getSettings: vi.fn(),
  listAgents: vi.fn(),
  updateSettings: vi.fn(),
}));

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return { ...actual, api: { ...actual.api, getSettings, listAgents, updateSettings } };
});

beforeAll(() => {
  Element.prototype.scrollIntoView = () => {};
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
});

function renderCard() {
  render(
    <I18nProvider>
      <DefaultAssigneeCard />
    </I18nProvider>,
  );
}

describe("DefaultAssigneeCard", () => {
  beforeEach(() => {
    getSettings.mockReset();
    listAgents.mockReset();
    updateSettings.mockReset();
    listAgents.mockResolvedValue({
      agents: [
        { id: "1", name: "frontend-developer" },
        { id: "2", name: "system-architect" },
      ],
    });
  });

  it("picks an agent and saves it", async () => {
    getSettings.mockResolvedValue({ workspace_root: "", default_language: "en" });
    updateSettings.mockResolvedValue({ workspace_root: "", default_language: "en", default_assignee: "frontend-developer" });
    renderCard();

    const save = await screen.findByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "frontend-developer" }));
    fireEvent.click(save);

    await waitFor(() => expect(updateSettings).toHaveBeenCalledWith({ default_assignee: "frontend-developer" }));
  });

  it("shows the configured agent and offers clearing it", async () => {
    getSettings.mockResolvedValue({ workspace_root: "", default_language: "en", default_assignee: "system-architect" });
    renderCard();
    expect(await screen.findByText("system-architect")).toBeInTheDocument();

    const trigger = screen.getByRole("combobox");
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    expect(await screen.findByRole("option", { name: /Nobody/ })).toBeInTheDocument();
  });
});
