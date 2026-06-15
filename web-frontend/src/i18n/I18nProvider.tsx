import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import zh from "./locales/zh.json";
import en from "./locales/en.json";

type Locale = "zh" | "en";
type Messages = typeof zh;

type I18nContextValue = {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: string) => string;
};

const dictionaries: Record<Locale, Messages | Record<string, unknown>> = { zh, en };

const I18nContext = createContext<I18nContextValue | null>(null);

function lookup(source: Record<string, unknown>, key: string): string | undefined {
  const value = key.split(".").reduce<unknown>((current, segment) => {
    if (current && typeof current === "object" && segment in current) {
      return (current as Record<string, unknown>)[segment];
    }
    return undefined;
  }, source);
  return typeof value === "string" ? value : undefined;
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(() => {
    const stored = window.localStorage.getItem("qs_locale");
    return stored === "en" ? "en" : "zh";
  });

  const value = useMemo<I18nContextValue>(() => {
    const t = (key: string) => lookup(dictionaries[locale], key) ?? lookup(zh, key) ?? key;
    return {
      locale,
      setLocale: (next) => {
        window.localStorage.setItem("qs_locale", next);
        setLocaleState(next);
      },
      t
    };
  }, [locale]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n() {
  const context = useContext(I18nContext);
  if (!context) {
    throw new Error("useI18n must be used inside I18nProvider");
  }
  return context;
}
