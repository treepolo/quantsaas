import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LockKeyhole, Mail, type LucideIcon } from "lucide-react";
import { useAuth } from "../../app/AuthProvider";
import { useI18n } from "../../i18n/useI18n";
import { Button } from "../../shared/ui/Button";

function Field({
  icon: Icon,
  label,
  type,
  value,
  disabled,
  onChange
}: {
  icon: LucideIcon;
  label: string;
  type: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="block">
      <span className="mb-2 block text-xs font-semibold uppercase tracking-wider text-slate-500">{label}</span>
      <span className="flex items-center gap-2 rounded-lg border border-slate-700 bg-slate-900/80 px-3 transition focus-within:border-[#2dd4bf]">
        <Icon className="h-4 w-4 text-slate-500" />
        <input
          className="h-11 min-w-0 flex-1 bg-transparent text-sm text-slate-100 outline-none placeholder:text-slate-600"
          type={type}
          value={value}
          disabled={disabled}
          required
          onChange={(event) => onChange(event.target.value)}
        />
      </span>
    </label>
  );
}

export function RegisterPage() {
  const { t } = useI18n();
  const { register, loading } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const submitting = loading;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    if (password !== confirmPassword) {
      setError(t("auth.passwordMismatch"));
      return;
    }
    try {
      await register(email, password);
      navigate("/", { replace: true });
    } catch {
      setError(t("auth.authFailed"));
    }
  }

  return (
    <form className="space-y-4" onSubmit={onSubmit}>
      <div className="mb-6">
        <h2 className="text-center text-xl font-semibold text-slate-100">{t("auth.registerTitle")}</h2>
      </div>
      <Field icon={Mail} label={t("common.email")} type="email" value={email} disabled={submitting} onChange={setEmail} />
      <Field icon={LockKeyhole} label={t("common.password")} type="password" value={password} disabled={submitting} onChange={setPassword} />
      <Field
        icon={LockKeyhole}
        label={t("common.confirmPassword")}
        type="password"
        value={confirmPassword}
        disabled={submitting}
        onChange={setConfirmPassword}
      />
      <Button className="w-full uppercase tracking-wider" loading={submitting} type="submit">
        {t("auth.registerButton")}
      </Button>
      {error ? <p className="text-sm text-[#f87171]">{error}</p> : null}
      <Link className="block text-center text-sm text-[#2dd4bf] transition hover:text-[#99f6e4]" to="/login">
        {t("auth.hasAccount")}
      </Link>
    </form>
  );
}
