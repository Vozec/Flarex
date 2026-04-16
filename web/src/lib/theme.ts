// Theme toggle. Persists in localStorage; defaults to system preference.
const KEY = "flarex_theme";
type Mode = "light" | "dark";

export function getTheme(): Mode {
  const stored = localStorage.getItem(KEY) as Mode | null;
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function setTheme(mode: Mode) {
  localStorage.setItem(KEY, mode);
  applyTheme(mode);
}

export function applyTheme(mode: Mode) {
  const root = document.documentElement;
  if (mode === "dark") root.classList.add("dark");
  else root.classList.remove("dark");
}

export function toggleTheme(): Mode {
  const next: Mode = getTheme() === "dark" ? "light" : "dark";
  setTheme(next);
  return next;
}
