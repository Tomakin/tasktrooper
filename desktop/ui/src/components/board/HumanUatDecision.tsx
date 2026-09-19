import { Eye } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type BoardTask, type TaskIntegrationResponse } from "@/api";
import { IntegrationStatus, integrationReadyForRelease } from "@/components/board/IntegrationStatus";
import { LocalPreviewPanel } from "@/components/board/LocalPreviewPanel";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { useI18n } from "@/hooks/useI18n";

const INTEGRATION_POLL_MS = 15000;

interface HumanUatDecisionProps {
  task: BoardTask;
  repositoryId: string;
  onUpdated: () => void;
}

// The stakeholder's decision on a card outranks every other field, but it only
// exists at all while the card is actually waiting on them — anywhere else the
// generic column select (TaskDetailDrawer) remains the only override.
//
// Decline is rendered inline, not as a nested Radix Dialog: this component
// already lives inside TaskDetailDrawer's own modal Dialog, and a second
// modal Dialog.Root mounted inside an already-open one fights it over the
// document-level aria-hide/inert bookkeeping Radix uses to make everything
// but the top layer non-interactive, leaving both overlays marked inert and
// swallowing pointer input meant for the inner form. A non-modal expansion of
// the same section has no overlay to fight over.
export function HumanUatDecision({ task, repositoryId, onUpdated }: HumanUatDecisionProps) {
  const { t } = useI18n();
  const [saving, setSaving] = useState(false);
  const [declining, setDeclining] = useState(false);
  const [reason, setReason] = useState("");

  // The drawer swaps `task` in place without unmounting this component (it
  // never closes the drawer between cards), so a decline draft typed for one
  // task would otherwise still be here — and still submittable — once the
  // drawer shows a different one.
  useEffect(() => {
    setDeclining(false);
    setReason("");
  }, [task.id]);

  // With the two-stage delivery the server merges the task into the
  // integration branch on its own and watches that deploy; this polls its
  // record so the panel and the release button follow without a reload.
  const [flow, setFlow] = useState<TaskIntegrationResponse | null>(null);
  const inUat = task.column === "human_uat";
  useEffect(() => {
    setFlow(null);
    if (!inUat) return;
    let cancelled = false;
    const load = () =>
      api
        .getTaskIntegration(repositoryId, task.id)
        .then((r) => !cancelled && setFlow(r))
        .catch(() => undefined);
    void load();
    const timer = window.setInterval(() => void load(), INTEGRATION_POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [repositoryId, task.id, inUat]);

  if (!inUat) return null;

  const branchFlow = flow?.enabled ? flow : null;
  const branch = branchFlow?.integration_branch ?? "";
  const releaseReady = integrationReadyForRelease(branchFlow?.integration);

  const release = async () => {
    setSaving(true);
    try {
      const res = await api.releaseTask(repositoryId, task.id);
      onUpdated();
      if (res.merge_error) {
        toast.warning(t("boardArea.components.taskDetail.integration.releaseMergeRefused", { reason: res.merge_error }));
      } else {
        toast.success(t("boardArea.components.taskDetail.integration.released"));
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("boardArea.components.taskDetail.updateFailed"));
    } finally {
      setSaving(false);
    }
  };

  const approve = async () => {
    setSaving(true);
    try {
      await api.updateRepositoryTask(repositoryId, task.id, { column: "done" });
      onUpdated();
      toast.success(t("boardArea.components.taskDetail.humanUatApproved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("boardArea.components.taskDetail.updateFailed"));
    } finally {
      setSaving(false);
    }
  };

  const cancelDecline = () => {
    if (saving) return;
    setDeclining(false);
    setReason("");
  };

  // A decline is two independently-idempotent calls, not one transaction: the
  // reason is only worth posting if it can also move the card, but a failed
  // move must never lose the reason the stakeholder already typed.
  const submitDecline = async () => {
    const trimmed = reason.trim();
    if (!trimmed) return;
    setSaving(true);
    try {
      await api.createTaskComment(repositoryId, task.id, trimmed);
      await api.updateRepositoryTask(repositoryId, task.id, { column: "need_revision" });
      setDeclining(false);
      setReason("");
      onUpdated();
      toast.success(t("boardArea.components.taskDetail.humanUatDeclined"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("boardArea.components.taskDetail.updateFailed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="space-y-3 rounded-lg border-2 border-primary bg-primary/10 p-4 shadow-sm">
      <div className="flex items-center gap-2 text-primary">
        <Eye className="h-5 w-5 shrink-0" />
        <h3 className="text-base font-semibold text-foreground">
          {t("boardArea.components.taskDetail.humanUatHeading")}
        </h3>
      </div>
      {!declining ? (
        <>
          {branchFlow && <IntegrationStatus branch={branch} integration={branchFlow.integration} />}
          <div className="flex flex-wrap items-center gap-2">
            {branchFlow ? (
              <Button onClick={release} disabled={saving || !releaseReady}>
                {saving
                  ? t("boardArea.components.taskDetail.integration.releasing")
                  : t("boardArea.components.taskDetail.integration.release")}
              </Button>
            ) : (
              <Button onClick={approve} disabled={saving}>
                {t("boardArea.components.taskDetail.humanUatApprove")}
              </Button>
            )}
            <Button variant="destructive" onClick={() => setDeclining(true)} disabled={saving}>
              {t("boardArea.components.taskDetail.humanUatDecline")}
            </Button>
          </div>
          {branchFlow && !releaseReady && (
            <p className="text-sm text-muted-foreground">
              {t("boardArea.components.taskDetail.integration.notReady", { branch })}
            </p>
          )}
          <LocalPreviewPanel task={task} repositoryId={repositoryId} />
        </>
      ) : (
        <div className="space-y-2">
          <Label htmlFor="human-uat-decline-reason">
            {t("boardArea.components.taskDetail.humanUatDeclineReasonLabel")}
          </Label>
          <p className="text-sm text-muted-foreground">
            {t("boardArea.components.taskDetail.humanUatDeclineDescription")}
          </p>
          <Textarea
            id="human-uat-decline-reason"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t("boardArea.components.taskDetail.humanUatDeclinePlaceholder")}
            rows={4}
            disabled={saving}
            autoFocus
          />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={cancelDecline} disabled={saving}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={submitDecline}
              disabled={saving || reason.trim().length === 0}
            >
              {t("boardArea.components.taskDetail.humanUatDeclineSubmit")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}
