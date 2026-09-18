import { useState } from "react";
import { LoginForm } from "@/components/auth/LoginForm";
import { SidebarBrand } from "@/components/layout/SidebarBrand";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ApiError, api, type WebSessionUser } from "@/api";
import { useI18n } from "@/hooks/useI18n";

interface LoginPageProps {
  onSignedIn: (user: WebSessionUser) => void;
  /** The previous session ended under the user; say so rather than just asking again. */
  sessionExpired?: boolean;
}

export function LoginPage({ onSignedIn, sessionExpired = false }: LoginPageProps) {
  const { t } = useI18n();
  const [error, setError] = useState<string | null>(null);

  const signIn = async (username: string, password: string) => {
    try {
      onSignedIn(await api.webLogin(username, password));
    } catch (e) {
      if (e instanceof ApiError && e.status === 429) {
        setError(t("auth.login.locked", { minutes: Math.max(1, Math.ceil((e.retryAfter ?? 900) / 60)) }));
      } else if (e instanceof ApiError && e.status === 401) {
        setError(t("auth.login.invalid"));
      } else {
        setError(t("auth.login.failed"));
      }
    }
  };

  const shownError = error ?? (sessionExpired ? t("auth.login.expired") : null);

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <SidebarBrand className="mb-4" />
          <CardTitle>{t("auth.login.title")}</CardTitle>
          <CardDescription>{t("auth.login.subtitle")}</CardDescription>
        </CardHeader>
        <CardContent>
          <LoginForm onSubmit={signIn} error={shownError} errorVariant={error ? "error" : "info"} />
        </CardContent>
      </Card>
    </div>
  );
}
