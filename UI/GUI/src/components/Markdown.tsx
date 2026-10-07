import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import type { Components } from 'react-markdown';

// Open links in a new tab and keep them safe.
const components: Components = {
  a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noreferrer noopener" />,
};

type MarkdownProps = {
  source: string;
};

/**
 * Renders message bodies as Markdown with GitHub-flavored extensions
 * (tables, task lists, strikethrough) plus KaTeX math ($inline$ and $$block$$).
 * A single-dollar span that contains \displaystyle is rendered at display size
 * while staying inline. Thematic breaks (---, ***, ___) become rules.
 * Tolerant of partial input so it can render mid-stream.
 *
 * Memoized on `source`: a thread re-renders on every streamed chunk, and
 * re-running the remark/rehype/KaTeX pipeline over every settled message would
 * dominate that frame. Only the message whose text actually changed re-parses.
 */
const remarkPlugins = [remarkGfm, remarkMath];

const rehypePlugins = [
  [rehypeKatex, {
    throwOnError: false,
    errorColor: 'var(--err)',
    strict: false,
  }] as const,
];

export const Markdown = memo(function Markdown({ source }: MarkdownProps) {
  return (
    <div className="markdown-body">
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        components={components}
      >
        {source || ''}
      </ReactMarkdown>
    </div>
  );
});
