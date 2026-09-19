import { Cpu, Save } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type AgentConcurrency } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/hooks/useI18n";

const REFRESH_MS = 10000;

/** How many agent sessions run at once on this machine, and how full it is now. */
export function AgentConcurrencyCard() {
  const { t } = useI18n();
  const [status, setStatus] = useState<AgentConcurrency | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [choice, setChoice] = useState<string>("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await api.getAgentConcurrency();
      setStatus(s);
      setChoice((prev) => (prev === "" ? String(s.limit) : prev));
    } catch {
      setUnavailable(true);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [load]);

  if (unavailable) return null;

  const save = async () => {
    setSaving(true);
    try {
      const s = await api.setAgentConcurrency(Number(choice));
      setStatus(s);
      setChoice(String(s.limit));
      toast.success(t("settings.agentConcurrency.savedToast"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("common.saveFailed"));
    } finally {
      setSaving(false);
    }
  };

  const options = status ? Array.from({ length: status.max - status.min + 1 }, (_, i) => status.min + i) : [];

  return (
    <Card className="mt-4 w-full space-y-4 p-6">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <Cpu className="h-4 w-4" />
          <h3 className="font-semibold">{t("settings.agentConcurrency.title")}</h3>
        </div>
        {status && (
          <Badge variant={status.waiting > 0 ? "warning" : "outline"}>
            {status.limit > 0
              ? t("settings.agentConcurrency.occupancy", {
                  active: status.active,
                  limit: status.limit,
                  waiting: status.waiting,
                })
              : t("settings.agentConcurrency.occupancyUnlimited", { active: status.active })}
          </Badge>
        )}
      </div>
      <p className="text-sm text-muted-foreground">{t("settings.agentConcurrency.description")}</p>
      {!status ? (
        <Skeleton className="h-9 w-40" />
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-2">
            <Label htmlFor="agent-concurrency">{t("settings.agentConcurrency.label")}</Label>
            <Select value={choice} onValueChange={setChoice}>
              <SelectTrigger id="agent-concurrency" className="w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {options.map((n) => (
                  <SelectItem key={n} value={String(n)}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button onClick={() => void save()} disabled={saving || choice === "" || Number(choice) === status.limit}>
            <Save className="mr-2 h-4 w-4" />
            {saving ? t("common.saving") : t("common.save")}
          </Button>
        </div>
      )}
    </Card>
  );
}
