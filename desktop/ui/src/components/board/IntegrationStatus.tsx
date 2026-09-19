import { ExternalLink } from "lucide-react";
import type { TaskIntegration } from "@/api";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { useI18n } from "@/hooks/useI18n";

const STATUS_VARIANT: Record<TaskIntegration["status"], BadgeProps["variant"]> = {
  waiting: "info",
  merged: "success",
  conflict: "destructive",
  failed: "warning",
};

const DEPLOY_VARIANT: Record<string, BadgeProps["variant"]> = {
  pending: "info",
  success: "success",
  failure: "destructive",
  none: "outline",
};

interface IntegrationStatusProps {
  branch: string;
  integration?: TaskIntegration;
}

/** Where a human_uat task stands on the integration branch: merge and deploy. */
export function IntegrationStatus({ branch, integration }: IntegrationStatusProps) {
  const { t } = useI18n();
  const k = "boardArea.components.taskDetail.integration";

  return (
    <div className="space-y-2 rounded-md border border-border/60 bg-background/60 p-3 text-sm">
      <p className="font-medium">{t(`${k}.heading`, { branch })}</p>
      {!integration ? (
        <p className="text-muted-foreground">{t(`${k}.notStarted`, { branch })}</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={STATUS_VARIANT[integration.status]}>{t(`${k}.status.${integration.status}`)}</Badge>
            {integration.status === "merged" && integration.deploy_status && (
              <Badge variant={DEPLOY_VARIANT[integration.deploy_status]}>
                {t(`${k}.deploy.${integration.deploy_status}`)}
              </Badge>
            )}
            {integration.deploy_url && (
              <a
                className="inline-flex items-center gap-1 text-primary hover:underline"
                href={integration.deploy_url}
                target="_blank"
                rel="noreferrer"
              >
                {t(`${k}.viewRun`)}
                <ExternalLink className="h-3 w-3" />
              </a>
            )}
          </div>
          {integration.pr_url && (
            <a
              className="inline-flex items-center gap-1 text-primary hover:underline"
              href={integration.pr_url}
              target="_blank"
              rel="noreferrer"
            >
              {t(`${k}.pullRequest`, { branch })}
              {integration.pr_number ? ` #${integration.pr_number}` : ""}
              <ExternalLink className="h-3 w-3" />
            </a>
          )}
          {integration.merge_sha && (
            <p className="text-muted-foreground">
              {t(`${k}.mergeCommit`)}: <span className="font-mono">{integration.merge_sha.slice(0, 12)}</span>
            </p>
          )}
          {(integration.reason || integration.detail) && (
            <p className="text-muted-foreground">
              {integration.reason ? t(`${k}.reason.${integration.reason}`, { branch }) : integration.detail}
            </p>
          )}
        </>
      )}
    </div>
  );
}

export function integrationReadyForRelease(integration?: TaskIntegration): boolean {
  return (
    integration?.status === "merged" &&
    (integration.deploy_status === "success" || integration.deploy_status === "none")
  );
}
