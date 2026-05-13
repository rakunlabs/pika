import axios from 'axios';

// Mirror of internal/service/user_preferences.go defaults. Kept in sync
// with the backend; the backend also returns these on a fresh GET so
// the UI never has to guess.
export const DEFAULT_APP_THEME = 'system' as const;
export const DEFAULT_EDITOR_THEME = 'one-dark' as const;
export const DEFAULT_EDITOR_FONT_SIZE = 13;
// Geist Mono leads the stack so new users (whose stored value is "" /
// unset) get it by default. The trailing chain keeps older preference
// rows working when their stored value is just "JetBrains Mono, ...".
export const DEFAULT_EDITOR_FONT_FAMILY =
  "'Geist Mono', 'JetBrains Mono', Fira Code, Menlo, Monaco, monospace";
export const DEFAULT_EDITOR_LINE_WRAP = false;
export const DEFAULT_LEFT_WIDTH = 250;
export const DEFAULT_RIGHT_WIDTH = 280;

export type AppTheme = 'light' | 'dark' | 'system';

// Closed enum — must match service.KnownEditorThemes in
// internal/service/user_preferences.go.
export type EditorThemeKey =
  | 'default-light'
  | 'one-dark'
  | 'github-light'
  | 'github-dark'
  | 'dracula'
  | 'solarized-light'
  | 'solarized-dark'
  | 'nord'
  | 'monokai'
  | 'gruvbox-light'
  | 'gruvbox-dark';

export interface AppPreferences {
  theme: AppTheme;
}

export interface EditorPreferences {
  theme: EditorThemeKey;
  font_size: number;
  font_family: string;
  line_wrap: boolean;
}

export interface PanelPreferences {
  left_width: number;
  right_width: number;
}

export interface UserPreferences {
  user_id?: string;
  app: AppPreferences;
  editor: EditorPreferences;
  panels: PanelPreferences;
  updated_at?: string;
}

export interface UserPreferencesPatch {
  app?: Partial<AppPreferences>;
  editor?: Partial<EditorPreferences>;
  panels?: Partial<PanelPreferences>;
}

function defaultPrefs(): UserPreferences {
  return {
    app: { theme: DEFAULT_APP_THEME },
    editor: {
      theme: DEFAULT_EDITOR_THEME,
      font_size: DEFAULT_EDITOR_FONT_SIZE,
      font_family: DEFAULT_EDITOR_FONT_FAMILY,
      line_wrap: DEFAULT_EDITOR_LINE_WRAP,
    },
    panels: {
      left_width: DEFAULT_LEFT_WIDTH,
      right_width: DEFAULT_RIGHT_WIDTH,
    },
  };
}

// localStorage key for the app theme picked on the login screen (or any
// other pre-auth surface). This persists the user's choice across
// reloads even when they haven't authenticated yet, and gets reconciled
// with the server-side preference once they log in.
const LOCAL_APP_THEME_KEY = 'pika.app_theme';

function readLocalAppTheme(): AppTheme | null {
  if (typeof window === 'undefined') return null;
  try {
    const v = window.localStorage.getItem(LOCAL_APP_THEME_KEY);
    if (v === 'light' || v === 'dark' || v === 'system') return v;
  } catch {
    // localStorage can throw in private mode / disabled storage —
    // silently fall back to the default.
  }
  return null;
}

function writeLocalAppTheme(theme: AppTheme): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(LOCAL_APP_THEME_KEY, theme);
  } catch {
    // ignore quota/private-mode errors — the in-memory state still works.
  }
}

function createPrefsStore() {
  // App theme is intentionally NOT part of the server-side preference
  // document. It is a purely local, per-browser choice persisted in
  // localStorage — the user toggles it manually from the login screen
  // (or anywhere else that exposes the switch) and nothing about that
  // choice ever round-trips to the backend.
  //
  // Read the initial value into a plain local first, then seed the
  // $state with it. This keeps Svelte's `state_referenced_locally`
  // warning quiet — the init-time `applyAppTheme(...)` call below
  // explicitly wants the initial value (not a reactive subscription).
  const initialTheme: AppTheme = readLocalAppTheme() ?? DEFAULT_APP_THEME;
  let appTheme = $state<AppTheme>(initialTheme);

  // Server-backed preferences (editor + panels). `app` is kept on the
  // type for backwards compatibility with the backend payload, but the
  // UI never reads or writes it through this store — the local
  // `appTheme` $state above is the single source of truth.
  let prefs = $state<UserPreferences>(defaultPrefs());
  // Server snapshot: the last document we know is persisted on the
  // backend. Updated by loadPreferences and savePreferences. `dirty`
  // compares this to `prefs` to drive the "Save" button.
  let serverPrefs = $state<UserPreferences>(defaultPrefs());
  let loaded = $state(false);
  let saving = $state(false);

  // Tracks the system color-scheme media query subscription so the app
  // theme can react to OS changes when the user picked "system".
  let mqUnsub: (() => void) | null = null;

  function applyAppTheme(theme: AppTheme): void {
    if (typeof document === 'undefined') return;
    const root = document.documentElement;

    // Tear down any previous system listener — we'll reattach if needed.
    if (mqUnsub) {
      mqUnsub();
      mqUnsub = null;
    }

    let effective: 'light' | 'dark';
    if (theme === 'system') {
      const mq = window.matchMedia('(prefers-color-scheme: dark)');
      effective = mq.matches ? 'dark' : 'light';
      const onChange = (e: MediaQueryListEvent) => {
        root.classList.toggle('dark', e.matches);
      };
      mq.addEventListener('change', onChange);
      mqUnsub = () => mq.removeEventListener('change', onChange);
    } else {
      effective = theme;
    }
    root.classList.toggle('dark', effective === 'dark');
  }

  // Apply the locally-cached theme synchronously at module init so the
  // very first paint already matches the user's choice. We use the
  // captured `initialTheme` instead of `appTheme` so Svelte doesn't
  // warn about a non-reactive read of `$state` — this is a one-shot
  // boot effect, not a subscription.
  applyAppTheme(initialTheme);

  function applyServerPayload(data: UserPreferences | undefined): UserPreferences {
    const def = defaultPrefs();
    return {
      ...def,
      ...(data ?? {}),
      // `app` is purely client-local; drop whatever the server says.
      app: def.app,
      editor: { ...def.editor, ...(data?.editor ?? {}) },
      panels: { ...def.panels, ...(data?.panels ?? {}) },
    };
  }

  async function loadPreferences(): Promise<void> {
    try {
      const res = await axios.get<UserPreferences>('/api/v1/me/preferences');
      const merged = applyServerPayload(res.data);
      prefs = merged;
      serverPrefs = merged;
    } catch {
      // 401 (logged out) or network: keep defaults; UI will retry after
      // login. We don't surface the error here — appearance falling back
      // to defaults is a non-fatal degraded mode.
      const def = defaultPrefs();
      prefs = def;
      serverPrefs = def;
    } finally {
      loaded = true;
    }
  }

  // updatePreferences applies a patch to the in-memory state ONLY. It
  // does not hit the network — call savePreferences() to persist the
  // current state to the backend in a single PUT. This gives the user
  // an explicit "did I commit this change" affordance via the Save
  // button in the Appearance settings panel.
  //
  // The `app` and `panels` patches are accepted for API symmetry but
  // are dropped: `app` (theme) is purely local, and `panels` is
  // session-only (see config store) — neither is round-tripped through
  // this store.
  function updatePreferences(patch: UserPreferencesPatch): void {
    prefs = {
      ...prefs,
      editor: { ...prefs.editor, ...(patch.editor ?? {}) },
    };
  }

  // savePreferences pushes the current in-memory editor state to the
  // backend in a single PUT. Panels are intentionally NOT included —
  // they are session-only and never persisted. Throws on network /
  // validation failure; the caller surfaces it via a toast.
  async function savePreferences(): Promise<void> {
    if (saving) return;
    saving = true;
    try {
      const payload: UserPreferencesPatch = {
        editor: { ...prefs.editor },
      };
      const res = await axios.put<UserPreferences>('/api/v1/me/preferences', payload);
      const merged = applyServerPayload(res.data);
      prefs = merged;
      serverPrefs = merged;
    } finally {
      saving = false;
    }
  }

  // Discard local edits and revert to the last known server state.
  function revertPreferences(): void {
    prefs = serverPrefs;
  }

  // setAppTheme is the ONLY way to change the app theme. It writes to
  // localStorage and applies the class on <html>. No HTTP, no store
  // round-trip — strictly local, strictly per-browser.
  function setAppTheme(theme: AppTheme): void {
    appTheme = theme;
    writeLocalAppTheme(theme);
    applyAppTheme(theme);
  }

  async function resetPreferences(): Promise<void> {
    await axios.delete('/api/v1/me/preferences');
    await loadPreferences();
  }

  // Reset the server-backed prefs to defaults without hitting the
  // server. Called on logout so a shared device doesn't leak the
  // previous user's editor / font / panel settings. The local app
  // theme is intentionally untouched.
  function resetLocal(): void {
    const def = defaultPrefs();
    prefs = def;
    serverPrefs = def;
    loaded = false;
  }

  // dirty: shallow-compare the editor section of the local document
  // against the server snapshot. Panels aren't compared because they
  // are session-only and never round-trip to the backend.
  function isDirty(): boolean {
    const a = prefs.editor;
    const b = serverPrefs.editor;
    return (
      a.theme !== b.theme ||
      a.font_size !== b.font_size ||
      a.font_family !== b.font_family ||
      a.line_wrap !== b.line_wrap
    );
  }

  return {
    get prefs() { return prefs; },
    get loaded() { return loaded; },
    get saving() { return saving; },
    get dirty() { return isDirty(); },
    // `app` reflects the local-only app theme. Consumers should treat
    // it as read-only and use setAppTheme() to change it.
    get app(): AppPreferences { return { theme: appTheme }; },
    get editor() { return prefs.editor; },
    get panels() { return prefs.panels; },
    loadPreferences,
    updatePreferences,
    savePreferences,
    revertPreferences,
    setAppTheme,
    resetPreferences,
    resetLocal,
    applyAppTheme,
  };
}

export const prefsStore = createPrefsStore();
