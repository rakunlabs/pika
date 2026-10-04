// Compact drag image for vault rows. Without it, browsers snapshot the
// dragged element's box, which for table rows and wide list rows ends
// up being a large slice of the page.

export function setDragChip(dt: DataTransfer, label: string, dotClass = 'bg-slate-400'): void {
  const chip = document.createElement('div');
  chip.className =
    'fixed -top-96 left-0 flex items-center gap-1.5 max-w-64 px-2.5 py-1.5 rounded-md text-xs font-medium shadow-lg border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-700 dark:text-slate-100';
  const dot = document.createElement('span');
  dot.className = `shrink-0 w-2 h-2 rounded-sm ${dotClass}`;
  const text = document.createElement('span');
  text.className = 'truncate';
  text.textContent = label;
  chip.append(dot, text);
  document.body.appendChild(chip);
  dt.setDragImage(chip, 12, 14);
  // The image is captured synchronously; the node can go next tick.
  setTimeout(() => chip.remove(), 0);
}
