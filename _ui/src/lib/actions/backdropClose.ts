// backdropClose is a Svelte action for modal backdrops that close
// on "click away" — but only when the click *truly* originated on
// the backdrop.
//
// The naive approach (`<div onclick={onClose}>`) misfires when the
// user starts a drag inside the modal (e.g. selecting text in a
// textarea) and releases the mouse outside on the backdrop. In that
// flow the browser still synthesizes a `click` whose target is the
// backdrop, so the modal closes and any in-progress edit is lost.
// That UX regression has bitten us in three different modals already
// (external resource editor, vault item editor's password generator,
// vault new-item dialog) — hence this shared helper.
//
// The fix is to track `mousedown` and `mouseup` independently and
// only close when *both* fired on the backdrop element itself. Any
// drag that starts inside the modal contents is silently ignored,
// because mousedown happened on a descendant rather than the
// backdrop root.
//
// Usage:
//   <div use:backdropClose={onClose} class="fixed inset-0 ...">
//     <div onclick={(e) => e.stopPropagation()}>...modal body...</div>
//   </div>
//
// The inner stopPropagation isn't strictly necessary anymore (we no
// longer attach a click handler) but keeping it makes the intent
// obvious to future readers and protects against accidentally adding
// onclick to the backdrop in the future.

import type { Action } from 'svelte/action';

export const backdropClose: Action<HTMLElement, () => void> = (node, onClose) => {
  // Captured at mousedown so we can compare against mouseup. Stored
  // as `EventTarget | null` rather than HTMLElement to match the DOM
  // event shape — we only ever compare with strict equality.
  let mouseDownTarget: EventTarget | null = null;
  // Latest handler. Kept in a closure variable so `update()` can swap
  // it without re-binding the DOM listeners.
  let handler = onClose;

  const onMouseDown = (e: MouseEvent) => {
    mouseDownTarget = e.target;
  };
  const onMouseUp = (e: MouseEvent) => {
    // Both ends of the click must be on the backdrop node itself.
    // A drag that started in the modal body has mouseDownTarget set
    // to a descendant, so it will never match `node` here.
    if (e.target === node && mouseDownTarget === node) {
      handler();
    }
    mouseDownTarget = null;
  };

  node.addEventListener('mousedown', onMouseDown);
  node.addEventListener('mouseup', onMouseUp);

  return {
    update(next: () => void) {
      handler = next;
    },
    destroy() {
      node.removeEventListener('mousedown', onMouseDown);
      node.removeEventListener('mouseup', onMouseUp);
    },
  };
};
