import { useMemo } from 'react';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

export function Markdown({ content, className = '' }: { content: string; className?: string }) {
  const html = useMemo(
    () =>
      DOMPurify.sanitize(marked.parse(content, { async: false, breaks: true }), {
        USE_PROFILES: { html: true },
        FORBID_TAGS: ['style', 'form', 'input', 'button'],
        FORBID_ATTR: ['style'],
      }),
    [content],
  );

  return <div className={`markdown-body ${className}`} dangerouslySetInnerHTML={{ __html: html }} />;
}
