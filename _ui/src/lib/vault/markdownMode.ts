// Minimal CodeMirror stream mode for markdown source: headings, emphasis,
// inline/fenced code, links, lists and quotes. Enough for comfortable
// editing without adding @codemirror/lang-markdown to the bundle.

import type { StreamParser, StringStream } from '@codemirror/language';

interface State {
  fence: string | null;
}

export const markdownMode: StreamParser<State> = {
  name: 'markdown',
  startState: () => ({ fence: null }),
  token(stream: StringStream, state: State) {
    if (stream.sol()) {
      const fence = stream.match(/^\s*(```+|~~~+)/, false) as RegExpMatchArray | null;
      if (fence) {
        const marker = fence[1];
        if (state.fence === null) state.fence = marker;
        else if (marker.startsWith(state.fence)) state.fence = null;
        stream.skipToEnd();
        return 'meta';
      }
      if (state.fence !== null) {
        stream.skipToEnd();
        return 'string';
      }
      if (stream.match(/^#{1,6}\s.*$/)) return 'heading';
      if (stream.match(/^\s*>/)) return 'quote';
      if (stream.match(/^\s*([-*+]|\d+[.)])\s/)) return 'list';
      if (stream.match(/^\s*([-*_])(\s*\1){2,}\s*$/)) return 'contentSeparator';
    }
    if (state.fence !== null) {
      stream.skipToEnd();
      return 'string';
    }
    if (stream.match(/^`[^`]+`/)) return 'string';
    if (stream.match(/^\*\*[^*]+\*\*/) || stream.match(/^__[^_]+__/)) return 'strong';
    if (stream.match(/^\*[^*\s][^*]*\*/) || stream.match(/^_[^_\s][^_]*_/)) return 'emphasis';
    if (stream.match(/^!?\[[^\]]*\]\([^)]*\)/)) return 'link';
    if (stream.match(/^https?:\/\/\S+/)) return 'link';
    stream.next();
    stream.eatWhile(/[^`*_![h]/);
    return null;
  },
};
