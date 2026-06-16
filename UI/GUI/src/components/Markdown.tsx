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
 * Tolerant of partial input so it can render mid-stream.
 */
export function Markdown({ source }: MarkdownProps) {
  return (
    <div className="markdown-body">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[[rehypeKatex, { throwOnError: false, errorColor: 'var(--err)', strict: false }]]}
        components={components}
      >
        {source || ''}
      </ReactMarkdown>
    </div>
  );
}
