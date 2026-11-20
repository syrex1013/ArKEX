"use client";

import { useEffect, useState } from "react";

const fonts = [
  "Geist", "Geist Mono", "Geist Pixel Square", "Inter", "Noto Sans", "Nunito Sans", "Figtree",
  "Roboto", "Raleway", "DM Sans", "Public Sans", "Outfit", "JetBrains Mono", "Noto Serif",
  "Roboto Slab", "Merriweather", "Lora", "Playfair Display",
] as const;

const options = {
  mode: ["Light", "Dark", "System"],
  preset: ["Default", "Brutalist", "Soft Pop", "Tangerine"],
  font: fonts,
  content: ["Centered", "Full Width"],
  navbar: ["Sticky", "Scroll"],
  sidebar: ["Sidebar", "Inset", "Floating"],
  collapse: ["Icon", "Offcanvas"],
} as const;

type Preferences = { [K in keyof typeof options]: (typeof options)[K][number] };
type PreferenceKey = keyof Preferences;

const defaults: Preferences = {
  mode: "Light", preset: "Default", font: "Geist", content: "Full Width",
  navbar: "Sticky", sidebar: "Sidebar", collapse: "Icon",
};
const key = "arkex-appearance";
const preferenceKeys = Object.keys(options) as PreferenceKey[];

function readPreferences(): Preferences {
  try {
    const saved = JSON.parse(localStorage.getItem(key) ?? "null");
    return Object.fromEntries(preferenceKeys.map((name) => [
      name, (options[name] as readonly unknown[]).includes(saved?.[name]) ? saved[name] : defaults[name],
    ])) as Preferences;
  } catch {
    return defaults;
  }
}

export function useAppearance() {
  const [preferences, setPreferences] = useState(defaults);
  const [ready, setReady] = useState(false);
  const [persistent, setPersistent] = useState(true);

  useEffect(() => {
    setPreferences(readPreferences());
    setReady(true);
  }, []);

  useEffect(() => {
    if (!ready) return;
    try {
      localStorage.setItem(key, JSON.stringify(preferences));
    } catch {
      setPersistent(false);
    }
  }, [preferences, ready]);

  useEffect(() => {
    const system = window.matchMedia("(prefers-color-scheme: dark)");
    const applyMode = () => {
      document.documentElement.dataset.mode = preferences.mode === "System"
        ? (system.matches ? "dark" : "light")
        : preferences.mode.toLowerCase();
    };
    applyMode();
    if (preferences.mode !== "System") return;
    system.addEventListener("change", applyMode);
    return () => system.removeEventListener("change", applyMode);
  }, [preferences.mode]);

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.preset = preferences.preset;
    const builtIn = {
      Geist: "var(--font-geist-sans)",
      "Geist Mono": "var(--font-geist-mono)",
      "Geist Pixel Square": "var(--font-geist-pixel-square)",
    }[preferences.font as "Geist" | "Geist Mono" | "Geist Pixel Square"];
    root.style.setProperty("--selected-font", builtIn ?? `"${preferences.font}", var(--font-geist-sans)`);
    if (builtIn) return;
    const stylesheet = document.createElement("link");
    stylesheet.rel = "stylesheet";
    stylesheet.href = `https://fonts.googleapis.com/css2?family=${encodeURIComponent(preferences.font)}:wght@400;500;600;700&display=swap`;
    document.head.append(stylesheet);
    return () => stylesheet.remove();
  }, [preferences.font, preferences.preset]);

  return { preferences, setPreferences, persistent };
}

export function AppearanceControls({ appearance }: { appearance: ReturnType<typeof useAppearance> }) {
  const { preferences, setPreferences, persistent } = appearance;
  const labels: Record<PreferenceKey, string> = {
    mode: "Theme mode", preset: "Theme preset", font: "Font", content: "Page layout",
    navbar: "Navbar behavior", sidebar: "Sidebar style", collapse: "Sidebar collapse mode",
  };

  return (
    <details className="appearance">
      <summary className="btn-secondary">Preferences</summary>
      <div className="appearance-panel card">
        {preferenceKeys.map((name) => (
          <label key={name}>
            <span>{labels[name]}</span>
            <select value={preferences[name]} onChange={(event) => {
              const value = event.target.value;
              if ((options[name] as readonly string[]).includes(value)) {
                setPreferences((previous) => ({ ...previous, [name]: value }));
              }
            }}>
              {options[name].map((value) => <option key={value}>{value}</option>)}
            </select>
          </label>
        ))}
        <button type="button" className="btn-secondary" onClick={() => setPreferences(defaults)}>Reset preferences</button>
        {!persistent && <p role="status">Browser storage is unavailable.</p>}
      </div>
    </details>
  );
}
