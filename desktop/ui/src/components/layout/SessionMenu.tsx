import { LogOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/hooks/useI18n";
import { useWebSession } from "@/hooks/useWebSession";

/** The signed-in user and a sign-out button; renders nothing in the desktop shell. */
export function SessionMenu() {
  const { t } = useI18n();
  const session = useWebSession();
  if (!session) return null;

  return (
    <div className="flex items-center gap-1">
      <span
        className="hidden max-w-[12rem] truncate text-caption text-muted-foreground sm:inline"
        title={t("auth.session.signedInAs", { name: session.username })}
      >
        {session.username}
      </span>
      <Button
        variant="ghost"
        size="icon"
        onClick={() => void session.signOut()}
        title={t("auth.session.signOut")}
        aria-label={t("auth.session.signOut")}
      >
        <LogOut className="h-4 w-4" />
      </Button>
    </div>
  );
}
