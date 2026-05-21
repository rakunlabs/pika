# Agent Instructions

- Before making UI changes under `_ui`, read `_ui/DESIGN_SYSTEM.md` and follow its color, surface elevation, form, button, and dark-mode rules.
- In dark mode, keep page surfaces on `dark:bg-warm-900` and card/panel surfaces one step lighter on `dark:bg-warm-800` unless `_ui/DESIGN_SYSTEM.md` defines a different tier for that component.
- Use Tailwind token classes from `_ui/src/style/global.css`; do not hard-code component colors unless there is a documented exception.
