import { GitMerge, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type BranchFlowSettings } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/hooks/useI18n";

interface BranchFlowCardProps {
  repositoryId: string;
  className?: string;
}

/**
 * The repository's two-stage delivery: the integration branch a task that
 * passed QA is merged into before a human approves it for the default branch.
 * Empty turns the flow off.
 */
export function BranchFlowCard({ repositoryId, className }: BranchFlowCardProps) {
  const { t } = useI18n();
  const [settings, setSettings] = useState<BranchFlowSettings | null>(null);
  const [branch, setBranch] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setSettings(null);
    api
      .getBranchFlow(repositoryId)
      .then((s) => {
        if (cancelled) return;
        setSettings(s);
        setBranch(s.integration_branch);
      })
      .catch(() => !cancelled && setSettings({ enabled: false, integration_branch: "" }));
    return () => {
      cancelled = true;
    };
  }, [repositoryId]);

  const save = async (next: string) => {
    setSaving(true);
    try {
      const s = await api.setBranchFlow(repositoryId, next.trim());
      setSettings(s);
      setBranch(s.integration_branch);
      toast.success(t("projectAdmin.projectSettings.branchFlow.saved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("common.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className={`w-full space-y-4 p-6 ${className ?? ""}`}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <GitMerge className="h-4 w-4 text-muted-foreground" />
          <h3 className="font-semibold">{t("projectAdmin.projectSettings.branchFlow.title")}</h3>
        </div>
        {settings && (
          <Badge variant={settings.enabled ? "success" : "outline"}>
            {settings.enabled
              ? t("projectAdmin.projectSettings.branchFlow.enabled", { branch: settings.integration_branch })
              : t("projectAdmin.projectSettings.branchFlow.disabled")}
          </Badge>
        )}
      </div>
      <p className="text-sm text-muted-foreground">{t("projectAdmin.projectSettings.branchFlow.description")}</p>
      {!settings ? (
        <Skeleton className="h-9 w-full" />
      ) : (
        <div className="space-y-2">
          <Label htmlFor="branch-flow-integration">{t("projectAdmin.projectSettings.branchFlow.branchLabel")}</Label>
          <div className="flex flex-wrap gap-2">
            <Input
              id="branch-flow-integration"
              className="max-w-xs font-mono"
              value={branch}
              placeholder={t("projectAdmin.projectSettings.branchFlow.branchPlaceholder")}
              onChange={(e) => setBranch(e.target.value)}
              disabled={saving}
            />
            <Button
              size="sm"
              onClick={() => void save(branch)}
              disabled={saving || branch.trim() === settings.integration_branch}
            >
              <Save className="mr-2 h-4 w-4" />
              {t("common.save")}
            </Button>
            {settings.enabled && (
              <Button size="sm" variant="outline" onClick={() => void save("")} disabled={saving}>
                {t("projectAdmin.projectSettings.branchFlow.disableAction")}
              </Button>
            )}
          </div>
        </div>
      )}
    </Card>
  );
}
