<script lang="ts">
  import CodeMirror from "svelte-codemirror-editor";
  import type { Extension } from "@codemirror/state";
  import type { LanguageSupport } from "@codemirror/language";
  import type { EditorView } from "@codemirror/view";

  import { prefsStore } from "@/lib/store/prefs.svelte";
  import { resolveEditorTheme, gutterStylesFor } from "@/lib/editor/themes";

  // Single source of truth for every CodeMirror mount in the app. Consumes
  // the user's preference store so font size, font family, theme and line
  // wrapping stay in lockstep across the three places we instantiate an
  // editor (config editor, raw viewer, render preview).

  interface Props {
    value: string;
    lang?: LanguageSupport | null;
    extensions?: Extension[];
    readonly?: boolean;
    onchange?: (value: string) => void;
    onready?: (view: EditorView) => void;
    onreconfigure?: (view: EditorView) => void;
    lineWrapping?: boolean;
    // Optional override for the gutter styling — RenderPreview historically
    // didn't paint its gutter, so callers may pass `hideGutter` to keep
    // CodeMirror's intrinsic look. Defaults: gutter painted to match theme.
    hideGutter?: boolean;
  }

  let {
    value,
    lang,
    extensions = [],
    readonly = false,
    onchange,
    onready,
    onreconfigure,
    lineWrapping,
    hideGutter = false,
  }: Props = $props();

  const themeMeta = $derived(resolveEditorTheme(prefsStore.editor.theme));
  const gutter = $derived(gutterStylesFor(themeMeta));

  // Build the `styles` (ThemeSpec) object reactively so every preference
  // change is reflected immediately without requiring a full remount.
  const styles = $derived.by(() => {
    const s: Record<string, Record<string, string>> = {
      // The host fills its container and lets CodeMirror's own
      // `.cm-scroller` handle the actual scrollbars. We explicitly opt
      // out of nesting a second overflow on `&` (it would create two
      // competing scrollbars when line_wrap is off and lines extend
      // past the viewport).
      "&": {
        height: "100%",
        fontSize: `${prefsStore.editor.font_size}px`,
      },
      ".cm-scroller": {
        overflow: "auto",
      },
      ".cm-content": {
        fontFamily: prefsStore.editor.font_family,
      },
    };
    if (!hideGutter) {
      s[".cm-gutters"] = {
        backgroundColor: gutter.background,
        color: gutter.color,
        border: "none",
      };
    }
    return s;
  });
</script>

<CodeMirror
  {value}
  {lang}
  {extensions}
  {readonly}
  {onchange}
  {onready}
  {onreconfigure}
  theme={themeMeta.extension}
  lineWrapping={lineWrapping ?? prefsStore.editor.line_wrap}
  {styles}
/>
