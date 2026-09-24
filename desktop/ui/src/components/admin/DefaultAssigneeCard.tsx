import { Save, UserRound } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type Agent } from "@/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/hooks/useI18n";

// The empty choice, spelled as a value because a Radix SelectItem cannot carry
// an empty one.
const NOBODY = "-";

/**
 * Who a task created without an assignee goes to.
 *
 * The board dispatches a card either to its assignee or to an agent subscribed
 * to its column, and the columns agents own are the review ones — so a task
 * dropped in Todo with neither waits there silently. This is the setting that
 * makes "drop it in Todo" enough.
 */
export function DefaultAssigneeCard() {
  const { t } = useI18n();
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [saved, setSaved] = useState(NOBODY);
  const [selected, setSelected] = useState(NOBODY);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const [settings, roster] = await Promise.all([api.getSettings(), api.listAgents()]);
      setAgents(roster.agents ?? []);
      const current = settings.default_assignee || NOBODY;
      setSaved(current);
      setSelected(current);
    } catch {
      setAgents([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    try {
      const settings = await api.updateSettings({ default_assignee: selected });
      const current = settings.default_assignee || NOBODY;
      setSaved(current);
      setSelected(current);
      toast.success(t("settingsPages.board.defaultAssignee.saved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("common.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <UserRound className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-sm font-semibold">{t("settingsPages.board.defaultAssignee.title")}</h3>
      </div>
      <p className="text-sm text-muted-foreground">{t("settingsPages.board.defaultAssignee.description")}</p>
      {!agents ? (
        <Skeleton className="h-9 w-64" />
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-2">
            <Label htmlFor="default-assignee">{t("settingsPages.board.defaultAssignee.label")}</Label>
            <Select value={selected} onValueChange={setSelected}>
              <SelectTrigger id="default-assignee" className="w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NOBODY}>{t("settingsPages.board.defaultAssignee.nobody")}</SelectItem>
                {agents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.name}>
                    {agent.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button size="sm" onClick={() => void save()} disabled={saving || selected === saved}>
            <Save className="mr-2 h-4 w-4" />
            {t("common.save")}
          </Button>
        </div>
      )}
    </Card>
  );
}
