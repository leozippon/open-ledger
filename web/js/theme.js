const THEME_KEY = 'ledger-theme';
const ACCENT_KEY = 'ledger-accent';
const LIGHT_CHROME = '#f2f2f7';
const DARK_CHROME = '#000000';

export const ACCENTS = [
  { id: 'blue', label: '蓝', swatch: '#4a6cf7' },
  { id: 'red', label: '红', swatch: '#c41e3a' },
  { id: 'pink', label: '粉', swatch: '#d63384' },
  { id: 'violet', label: '紫', swatch: '#7c4dff' },
  { id: 'teal', label: '青', swatch: '#0d7377' },
  { id: 'orange', label: '橙', swatch: '#e85d04' },
];

export function themePreference() {
  const value = localStorage.getItem(THEME_KEY);
  return value === 'light' || value === 'dark' ? value : 'auto';
}

export function accentPreference() {
  const value = localStorage.getItem(ACCENT_KEY);
  return ACCENTS.some((item) => item.id === value) ? value : 'blue';
}

function applyTheme() {
  const pref = themePreference();
  const dark = pref === 'dark' || (pref === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.dataset.theme = dark ? 'dark' : 'light';
  document.documentElement.dataset.accent = accentPreference();
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', dark ? DARK_CHROME : LIGHT_CHROME);
}

export function setThemePreference(mode) {
  if (mode === 'light' || mode === 'dark') localStorage.setItem(THEME_KEY, mode);
  else localStorage.removeItem(THEME_KEY);
  applyTheme();
}

export function setAccentPreference(id) {
  if (id === 'blue' || !ACCENTS.some((item) => item.id === id)) localStorage.removeItem(ACCENT_KEY);
  else localStorage.setItem(ACCENT_KEY, id);
  applyTheme();
}

export function watchTheme() {
  applyTheme();
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);
}
