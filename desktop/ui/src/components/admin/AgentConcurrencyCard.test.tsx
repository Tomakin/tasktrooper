import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConcurrencyCard } from "@/components/admin/AgentConcurrencyCard";
import { I18nProvider } from "@/hooks/useI18n";

const { getAgentConcurrency, setAgentConcurrency } = vi.hoisted(() => ({
  getAgentConcurrency: vi.fn(),
  setAgentConcurrency: vi.fn(),
}));

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return { ...actual, api: { ...actual.api, getAgentConcurrency, setAgentConcurrency } };
});

beforeAll(() => {
  // Radix Select measures and scrolls its listbox; jsdom implements neither.
  Element.prototype.scrollIntoView = () => {};
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.releasePointerCapture = () => {};
});

function renderCard() {
  return render(
    <I18nProvider>
      <AgentConcurrencyCard />
    </I18nProvider>,
  );
}

describe("AgentConcurrencyCard", () => {
  beforeEach(() => {
    getAgentConcurrency.mockReset();
    setAgentConcurrency.mockReset();
  });

  it("shows the occupancy and saves a new limit", async () => {
    getAgentConcurrency.mockResolvedValue({ limit: 1, active: 1, waiting: 2, min: 1, max: 10 });
    setAgentConcurrency.mockResolvedValue({ limit: 3, active: 3, waiting: 0, min: 1, max: 10 });
    renderCard();

    expect(await screen.findByText("1 / 1 running, 2 queued")).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();

    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "3" }));
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await waitFor(() => expect(setAgentConcurrency).toHaveBeenCalledWith(3));
    expect(await screen.findByText("3 / 3 running, 0 queued")).toBeInTheDocument();
  });

  it("says when there is no limit", async () => {
    getAgentConcurrency.mockResolvedValue({ limit: 0, active: 4, waiting: 0, min: 1, max: 10 });
    renderCard();
    expect(await screen.findByText("4 running, no limit")).toBeInTheDocument();
  });

  it("renders nothing on a server without the setting", async () => {
    getAgentConcurrency.mockRejectedValue(new Error("not found"));
    const { container } = renderCard();
    await waitFor(() => expect(container.textContent).toBe(""));
  });
});
