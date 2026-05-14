// Tiny markdown renderer for vault secure-notes.
//
// Design goals:
//   1. Zero dependencies — every byte is hostile in a credentials
//      vault, and a real markdown library brings hundreds of KB of
//      parser code into the bundle.
//   2. HTML-escape EVERYTHING that isn't an intended markup token.
//      The function returns a trusted HTML string suitable for
//      {@html ...} only because we control the entire production
//      path: input is escaped first, then markers are recognized,
//      then matched markers emit pre-built tag pairs. We never
//      interpolate user text into an attribute.
//   3. Plain prose first. We render the small subset users actually
//      write in notes:
//        - Headings (# .. ######)
//        - Bold (**x**) and italic (*x* / _x_)
//        - Inline code (`x`) and fenced code (```)
//        - Links: [text](https://...) — only http/https/mailto
//        - Ordered + unordered lists
//        - Blockquotes (> ...)
//        - Horizontal rule (---)
//        - Hard line breaks between blocks
//
// What we deliberately DON'T support:
//   - Raw HTML passthrough (would bypass the escape).
//   - Image embedding (loads remote resources, leaks viewer IP).
//   - HTML attributes (titles, alt, class — none useful here).
//   - Tables / footnotes / definition lists — not common in notes
//     and the cost-benefit doesn't fit a vault component.
//
// If a user pastes raw HTML into a note, it appears verbatim as
// text. That's the right default for a paranoia-first product.

const ESCAPE_MAP: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESCAPE_MAP[c]);
}

// Allow only http(s) and mailto links. Anything else (javascript:,
// data:, vbscript:, file:, custom URI handlers) renders as plain
// text — the link reverts to its label without a clickable href.
function isSafeUrl(url: string): boolean {
  try {
    const trimmed = url.trim();
    if (/^mailto:/i.test(trimmed)) return true;
    const u = new URL(trimmed);
    return u.protocol === 'http:' || u.protocol === 'https:';
  } catch {
    return false;
  }
}

// Inline-level transforms applied AFTER block escaping. Order
// matters because earlier patterns shouldn't consume tokens that
// belong to later ones (e.g. inline code must be processed before
// bold so that `**not bold**` inside backticks stays literal).
function renderInline(line: string): string {
  let s = escapeHtml(line);

  // Inline code: `text`. Captured group is already escaped. We use
  // a placeholder so subsequent regexes don't see asterisks inside
  // code spans. The Unicode private-use marker (U+E000) is reserved
  // for application use and unlikely to appear in user notes; the
  // ESCAPE_MAP doesn't touch it so it passes through unchanged.
  const codePlaceholders: string[] = [];
  s = s.replace(/`([^`]+)`/g, (_m, code: string) => {
    const idx = codePlaceholders.length;
    codePlaceholders.push(`<code class="px-1 py-0.5 rounded bg-slate-100 dark:bg-warm-800 text-[0.85em] font-mono">${code}</code>`);
    return `\uE000${idx}\uE000`;
  });

  // Bold + italic. Stick to ** and *; underscore italics also.
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/(^|[\s(])_([^_\n]+)_(?=[\s).,;:!?]|$)/g, '$1<em>$2</em>');
  s = s.replace(/(^|[\s(])\*([^*\n]+)\*(?=[\s).,;:!?]|$)/g, '$1<em>$2</em>');

  // Links: [label](url). label is already escaped; url is validated.
  s = s.replace(/\[([^\]\n]+)\]\(([^)\s]+)\)/g, (_m, label: string, urlEscaped: string) => {
    // The captured url has already gone through escapeHtml — undo
    // ONLY the entities that legitimately appear in a URL. We use
    // the decoded form for the safety check, but emit the escaped
    // form in the href so we never break out of the attribute.
    const decoded = urlEscaped
      .replace(/&amp;/g, '&')
      .replace(/&#39;/g, "'");
    if (!isSafeUrl(decoded)) return label;
    // The escaped url is safe to drop into an attribute as-is
    // because escapeHtml already converted &, <, >, ", '.
    return `<a href="${urlEscaped}" target="_blank" rel="noopener noreferrer" class="text-accent-600 dark:text-accent-400 underline decoration-dotted hover:decoration-solid">${label}</a>`;
  });

  // Restore inline code placeholders. The literal U+E000 chars are
  // private-use; they survive escapeHtml and never appear in user
  // text, so a global find-replace is safe.
  s = s.replace(/\uE000(\d+)\uE000/g, (_m, idx: string) => codePlaceholders[Number(idx)] ?? '');

  return s;
}

interface CodeBlock {
  kind: 'code';
  lang: string;
  lines: string[];
}
interface ListBlock {
  kind: 'list';
  ordered: boolean;
  items: string[]; // raw inline content, unrendered
}
interface QuoteBlock {
  kind: 'quote';
  lines: string[]; // raw inline content
}
interface ParaBlock {
  kind: 'para';
  lines: string[];
}
interface HeadingBlock {
  kind: 'heading';
  level: number;
  text: string;
}
interface RuleBlock {
  kind: 'rule';
}

type Block = CodeBlock | ListBlock | QuoteBlock | ParaBlock | HeadingBlock | RuleBlock;

function tokenize(src: string): Block[] {
  const blocks: Block[] = [];
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();

    // Fenced code block.
    const fence = /^```(\w*)\s*$/.exec(trimmed);
    if (fence) {
      const lang = fence[1] ?? '';
      const buf: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i].trim())) {
        buf.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // consume closing fence
      blocks.push({ kind: 'code', lang, lines: buf });
      continue;
    }

    // Horizontal rule.
    if (/^-{3,}\s*$/.test(trimmed) || /^\*{3,}\s*$/.test(trimmed)) {
      blocks.push({ kind: 'rule' });
      i++;
      continue;
    }

    // Heading.
    const head = /^(#{1,6})\s+(.*)$/.exec(trimmed);
    if (head) {
      blocks.push({ kind: 'heading', level: head[1].length, text: head[2] });
      i++;
      continue;
    }

    // Blockquote group.
    if (/^>\s?/.test(trimmed)) {
      const buf: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i].trim())) {
        buf.push(lines[i].trim().replace(/^>\s?/, ''));
        i++;
      }
      blocks.push({ kind: 'quote', lines: buf });
      continue;
    }

    // Unordered / ordered list.
    const ul = /^[-*+]\s+(.*)$/.exec(trimmed);
    const ol = /^(\d+)\.\s+(.*)$/.exec(trimmed);
    if (ul || ol) {
      const ordered = !!ol;
      const items: string[] = [];
      while (i < lines.length) {
        const m = ordered
          ? /^(\d+)\.\s+(.*)$/.exec(lines[i].trim())
          : /^[-*+]\s+(.*)$/.exec(lines[i].trim());
        if (!m) break;
        items.push(ordered ? m[2] : m[1]);
        i++;
      }
      blocks.push({ kind: 'list', ordered, items });
      continue;
    }

    // Blank line — paragraph separator.
    if (trimmed === '') {
      i++;
      continue;
    }

    // Paragraph: collect contiguous non-empty lines that don't
    // start another block.
    const buf: string[] = [];
    while (i < lines.length) {
      const t = lines[i].trim();
      if (t === '') break;
      if (/^#{1,6}\s+/.test(t)) break;
      if (/^```/.test(t)) break;
      if (/^>\s?/.test(t)) break;
      if (/^[-*+]\s+/.test(t)) break;
      if (/^\d+\.\s+/.test(t)) break;
      if (/^-{3,}\s*$/.test(t) || /^\*{3,}\s*$/.test(t)) break;
      buf.push(lines[i]);
      i++;
    }
    if (buf.length > 0) blocks.push({ kind: 'para', lines: buf });
  }

  return blocks;
}

const HEADING_CLASSES: Record<number, string> = {
  1: 'text-xl font-bold mt-4 mb-2 leading-tight',
  2: 'text-lg font-bold mt-4 mb-2 leading-tight',
  3: 'text-base font-bold mt-3 mb-1.5 leading-tight',
  4: 'text-sm font-bold mt-3 mb-1.5 uppercase tracking-wider text-slate-700 dark:text-slate-200',
  5: 'text-xs font-bold mt-2 mb-1 uppercase tracking-wider text-slate-600 dark:text-slate-300',
  6: 'text-xs font-semibold mt-2 mb-1 uppercase tracking-wider text-slate-500 dark:text-slate-400',
};

export function renderMarkdown(src: string): string {
  if (!src) return '';
  const blocks = tokenize(src);
  const out: string[] = [];
  for (const b of blocks) {
    switch (b.kind) {
      case 'heading':
        out.push(`<h${b.level} class="${HEADING_CLASSES[b.level]}">${renderInline(b.text)}</h${b.level}>`);
        break;
      case 'rule':
        out.push('<hr class="my-3 border-slate-200 dark:border-warm-800" />');
        break;
      case 'code': {
        // No syntax highlighting (would need another dep); we just
        // emit the language as a small label. Content is escaped.
        const body = b.lines.map(escapeHtml).join('\n');
        const label = b.lang
          ? `<div class="text-[10px] uppercase tracking-wider text-slate-400 mb-1">${escapeHtml(b.lang)}</div>`
          : '';
        out.push(
          `<div class="my-2">${label}<pre class="px-3 py-2 rounded bg-slate-100 dark:bg-warm-900 border border-slate-200 dark:border-warm-800 text-xs font-mono overflow-x-auto"><code>${body}</code></pre></div>`,
        );
        break;
      }
      case 'quote': {
        const body = b.lines.map(renderInline).join('<br/>');
        out.push(
          `<blockquote class="my-2 pl-3 border-l-2 border-slate-300 dark:border-warm-700 text-slate-600 dark:text-slate-300 italic">${body}</blockquote>`,
        );
        break;
      }
      case 'list': {
        const tag = b.ordered ? 'ol' : 'ul';
        const cls = b.ordered ? 'list-decimal' : 'list-disc';
        const items = b.items.map((it) => `<li>${renderInline(it)}</li>`).join('');
        out.push(`<${tag} class="${cls} pl-5 my-2 space-y-0.5">${items}</${tag}>`);
        break;
      }
      case 'para': {
        const body = b.lines.map(renderInline).join('<br/>');
        out.push(`<p class="my-2 leading-relaxed">${body}</p>`);
        break;
      }
    }
  }
  return out.join('\n');
}
