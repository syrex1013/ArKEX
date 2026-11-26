import { t } from "@/i18n";

export const THEME_MODE_OPTIONS = [
  { label: t("Light"), value: "light" },
  { label: t("Dark"), value: "dark" },
  { label: t("System"), value: "system" },
] as const;

export const THEME_MODE_VALUES = THEME_MODE_OPTIONS.map((o) => o.value);
export type ThemeMode = (typeof THEME_MODE_VALUES)[number];
export type ResolvedThemeMode = "light" | "dark";

// --- generated:themePresets:start ---

export const THEME_PRESET_OPTIONS = [
  {
    label: "ThreatVector",
    value: "threatvector",
    primary: {
      light: "#d40511",
      dark: "#ff3b2f",
    },
  },
  {
    label: "Dracula",
    value: "dracula",
    primary: {
      light: "#644ac9",
      dark: "#bd93f9",
    },
  },
] as const;

export const THEME_PRESET_VALUES = THEME_PRESET_OPTIONS.map((p) => p.value);

export type ThemePreset = (typeof THEME_PRESET_OPTIONS)[number]["value"];

export const DEFAULT_THEME_PRESET: ThemePreset = "threatvector";

/** Coerce any persisted or stale preset value to a currently shipped style. */
export function normalizeThemePreset(value: unknown): ThemePreset {
  return THEME_PRESET_VALUES.includes(value as ThemePreset) ? (value as ThemePreset) : DEFAULT_THEME_PRESET;
}

// --- generated:themePresets:end ---
