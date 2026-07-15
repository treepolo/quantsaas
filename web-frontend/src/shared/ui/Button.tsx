import type { ButtonHTMLAttributes } from "react";
import type { LucideIcon } from "lucide-react";
import { Loader2 } from "lucide-react";
import { cn } from "../lib/cn";

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  icon?: LucideIcon;
  loading?: boolean;
  variant?: ButtonVariant;
  size?: "sm" | "md";
};

const variants: Record<ButtonVariant, string> = {
  primary: "border-[#2dd4bf]/40 bg-[#2dd4bf] text-slate-950 shadow-[0_0_30px_rgb(45_212_191/0.18)] hover:bg-[#5eead4]",
  secondary: "border-white/10 bg-white/[0.04] text-slate-200 hover:bg-white/[0.08]",
  ghost: "border-transparent bg-transparent text-slate-400 hover:bg-white/[0.05] hover:text-slate-100",
  danger: "border-[#f87171]/30 bg-[#f87171]/10 text-[#fecaca] hover:bg-[#f87171]/20"
};

export function Button({ className, icon: Icon, loading, variant = "primary", size = "md", children, disabled, ...props }: ButtonProps) {
  return (
    <button
      className={cn(
        "inline-flex min-h-10 items-center justify-center gap-2 rounded-lg border px-4 py-2 text-sm font-semibold transition duration-150 disabled:opacity-50",
        variants[variant],
        size === "sm" && "min-h-8 px-3 py-1 text-xs",
        className
      )}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : Icon ? <Icon className="h-4 w-4" /> : null}
      {children}
    </button>
  );
}
