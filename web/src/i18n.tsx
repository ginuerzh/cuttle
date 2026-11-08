import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { enUS } from './locales/en-US';

export const LANG_STORAGE_KEY = 'cuttle-lang';

export const LANGUAGES = [
  { id: 'zh-CN', label: '简体中文' },
  { id: 'en-US', label: 'English' },
] as const;

export type Lang = (typeof LANGUAGES)[number]['id'];

/** Normalize stored/browser language values; invalid values fall back to the browser. */
export function normalizeLang(raw?: string | null): Lang {
  const value = (raw ?? '').trim().toLowerCase();
  if (value.startsWith('zh')) return 'zh-CN';
  if (value.startsWith('en')) return 'en-US';
  const browser = typeof navigator !== 'undefined' ? navigator.language : 'en-US';
  return browser.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US';
}

let activeLang: Lang = normalizeLang();

/** Translate one Chinese source key into a specific language. */
export function translateFor(lang: Lang, key: string, params?: Record<string, string | number>): string {
  const message = lang === 'en-US' ? enUS[key] ?? key : key;
  if (!params) return message;
  return message.replace(/\{(\w+)\}/g, (match, name: string) =>
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match,
  );
}

/** Set the active language outside React (tests and non-React callers). */
export function setActiveLang(lang: Lang) {
  activeLang = lang;
}

/** Translate one Chinese source key into the active language. */
export function translate(key: string, params?: Record<string, string | number>): string {
  return translateFor(activeLang, key, params);
}

interface I18nValue {
  lang: Lang;
  setLang: (lang: Lang) => void;
  t: typeof translate;
}

const I18nContext = createContext<I18nValue | null>(null);

function readStoredLang(): string | null {
  try {
    return localStorage.getItem(LANG_STORAGE_KEY);
  } catch {
    return null;
  }
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(() => normalizeLang(readStoredLang()));

  activeLang = lang;

  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);

  const value = useMemo<I18nValue>(
    () => ({
      lang,
      setLang: (next) => {
        setActiveLang(next);
        setLangState(next);
        try {
          localStorage.setItem(LANG_STORAGE_KEY, next);
        } catch {
          /* ignore quota/private mode */
        }
      },
      t: translate,
    }),
    [lang],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const value = useContext(I18nContext);
  if (!value) throw new Error('useI18n must be used inside I18nProvider');
  return value;
}
