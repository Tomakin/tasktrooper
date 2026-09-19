import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Notice } from "@/components/ui/notice";
import { useI18n } from "@/hooks/useI18n";

interface LoginFormProps {
  onSubmit: (username: string, password: string) => Promise<void>;
  /** Shown above the fields: a failed attempt, a lockout, an ended session. */
  error?: string | null;
  errorVariant?: "error" | "info";
}

export function LoginForm({ onSubmit, error, errorVariant = "error" }: LoginFormProps) {
  const { t } = useI18n();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (submitting || !username.trim() || !password) return;
    setSubmitting(true);
    try {
      await onSubmit(username.trim(), password);
    } finally {
      setPassword("");
      setSubmitting(false);
    }
  };

  return (
    <form className="space-y-4" onSubmit={handleSubmit} noValidate>
      {error && <Notice variant={errorVariant} title={error} />}
      <div className="space-y-2">
        <Label htmlFor="login-username">{t("auth.login.username")}</Label>
        <Input
          id="login-username"
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          autoFocus
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          aria-invalid={errorVariant === "error" && !!error}
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="login-password">{t("auth.login.password")}</Label>
        <Input
          id="login-password"
          name="password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={errorVariant === "error" && !!error}
        />
      </div>
      <Button type="submit" className="w-full" disabled={submitting || !username.trim() || !password}>
        {submitting ? t("auth.login.submitting") : t("auth.login.submit")}
      </Button>
    </form>
  );
}
