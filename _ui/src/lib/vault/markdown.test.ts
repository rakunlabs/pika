import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown';

// Extract every href="..." value from rendered HTML.
function hrefs(html: string): string[] {
  return [...html.matchAll(/href="([^"]*)"/g)].map((m) => m[1]);
}

describe('renderMarkdown', () => {
  it('returns empty string for empty input', () => {
    expect(renderMarkdown('')).toBe('');
  });

  it('renders basic markup', () => {
    const html = renderMarkdown('# Title\n\n**bold** and *it* and `code`');
    expect(html).toContain('<h1');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toContain('<em>it</em>');
    expect(html).toContain('<code');
  });

  it('escapes raw HTML / script tags', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n<img src=x onerror=alert(1)>');
    expect(html).not.toContain('<script');
    expect(html).not.toContain('<img');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
  });

  it('escapes HTML inside every block type', () => {
    const payload = '<b onmouseover="x">';
    const src = [
      `# ${payload}`,
      `> ${payload}`,
      `- ${payload}`,
      `1. ${payload}`,
      '```',
      payload,
      '```',
      payload,
    ].join('\n');
    const html = renderMarkdown(src);
    expect(html).not.toMatch(/<b[\s>]/);
    expect(html).not.toContain('onmouseover="');
  });

  it('escapes the fenced-code language label', () => {
    const html = renderMarkdown('```<x>\ncode\n```');
    expect(html).not.toContain('<x>');
  });

  it('allows http, https and mailto links', () => {
    const html = renderMarkdown(
      '[a](https://example.com) [b](http://example.com/x?y=1&z=2) [c](mailto:me@example.com)',
    );
    expect(hrefs(html)).toEqual([
      'https://example.com',
      'http://example.com/x?y=1&amp;z=2',
      'mailto:me@example.com',
    ]);
    expect(html).toContain('rel="noopener noreferrer"');
  });

  it.each([
    'javascript:alert(1)',
    'JavaScript:alert(1)',
    'javascript&#58;alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'file:///etc/passwd',
    '//evil.example.com',
    '/relative/path',
  ])('drops unsafe link %s', (url) => {
    const html = renderMarkdown(`[click](${url})`);
    expect(hrefs(html)).toEqual([]);
    expect(html).toContain('click');
  });

  it('cannot break out of the href attribute', () => {
    const html = renderMarkdown('[x](https://example.com/"onmouseover="alert(1))');
    for (const h of hrefs(html)) {
      expect(h).not.toContain('"');
    }
    expect(html).not.toMatch(/\sonmouseover=/);
  });

  it('cannot inject attributes via single quotes', () => {
    const html = renderMarkdown("[x](https://example.com/'onmouseover='alert(1))");
    expect(html).not.toMatch(/\sonmouseover=/);
  });

  it('does not let the private-use placeholder smuggle markup', () => {
    // A user typing the code-placeholder marker must not pull in
    // another span's HTML or produce raw tags.
    const html = renderMarkdown('\uE0000\uE000 <i>x</i>');
    expect(html).not.toContain('<i>');
  });

  it('terminates on fence-like lines the fence regex does not accept', () => {
    // Regression: "```c++" / "```<x>" used to hang the tokenizer.
    expect(renderMarkdown('```c++\nint x;\n```')).toContain('int x;');
    expect(renderMarkdown('```<x> y\ncode')).toContain('code');
  });

  it('keeps markdown inside inline code literal', () => {
    const html = renderMarkdown('`**not bold**`');
    expect(html).not.toContain('<strong>');
  });
});
