import { useMemo, useState, type MouseEvent } from 'react';
import { marked } from 'marked';
import DOMPurify from 'dompurify';

export function Markdown({ content, className = '' }: { content: string; className?: string }) {
  const [linkError, setLinkError] = useState('');
  const html = useMemo(
    () =>
      DOMPurify.sanitize(marked.parse(content, { async: false, breaks: true }), {
        USE_PROFILES: { html: true },
        FORBID_TAGS: ['style', 'form', 'input', 'button'],
        FORBID_ATTR: ['style', 'class', 'id'],
      }),
    [content],
  );

  async function openLink(event: MouseEvent<HTMLDivElement>) {
    const desktop = window.ggDesktop;
    if (!desktop || event.button > 1 || !(event.target instanceof window.Element)) return;
    const link = event.target.closest('a[href]');
    if (!link) return;
    event.preventDefault();
    setLinkError('');
    try {
      const url = new URL(link.getAttribute('href') || '');
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) {
        throw new Error('只支持打开 HTTP 或 HTTPS 网页链接。');
      }
      await desktop.openExternal(url.href);
    } catch {
      setLinkError('无法打开链接，请复制有效的 HTTP 或 HTTPS 地址到浏览器。');
    }
  }

  return (
    <>
      <div
        className={`markdown-body ${className}`}
        onClick={(event) => void openLink(event)}
        onAuxClick={(event) => void openLink(event)}
        dangerouslySetInnerHTML={{ __html: html }}
      />
      {linkError && (
        <p className="markdown-link-error" role="alert">{linkError}</p>
      )}
    </>
  );
}
