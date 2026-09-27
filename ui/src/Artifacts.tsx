import { useEffect, useState } from 'react';
import { marked } from 'marked';
import type { API } from './transport';
import type { ArtifactMeta, ArtifactView } from './types';

// ArtifactsPane is the Web reader for agent-produced deliverables.
// Markdown renders via marked; HTML renders inside a sandboxed iframe so
// embedded scripts can never execute.
export function ArtifactsPane({ api }: { api: API }) {
  const [items, setItems] = useState<ArtifactMeta[]>([]);
  const [selected, setSelected] = useState<ArtifactView | null>(null);
  const [error, setError] = useState('');
  const [publishing, setPublishing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    api
      .listArtifacts()
      .then((list) => {
        if (!cancelled) setItems(list);
      })
      .catch((cause) => {
        if (!cancelled) setError(errorMessage(cause));
      });
    return () => {
      cancelled = true;
    };
  }, [api]);

  async function open(id: string) {
    setError('');
    try {
      setSelected(await api.getArtifact(id));
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function publish() {
    if (!selected || publishing) return;
    setPublishing(true);
    setError('');
    try {
      await api.publishArtifact(selected.meta.id);
      setSelected(await api.getArtifact(selected.meta.id));
      setItems(await api.listArtifacts());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setPublishing(false);
    }
  }

  return (
    <div className="artifacts-pane">
      <aside className="artifact-list" aria-label="文档列表">
        {items.map((item) => (
          <button
            key={item.id}
            className={`artifact-item ${selected?.meta.id === item.id ? 'selected' : ''}`}
            onClick={() => void open(item.id)}
          >
            <strong>{item.title}</strong>
            <span>
              {item.type === 'html' ? '页面' : '文档'} · v{item.version}
              {item.published_version > 0 ? ` · 已发布 v${item.published_version}` : ' · 草稿'}
            </span>
          </button>
        ))}
        {items.length === 0 && <p className="empty-note">还没有文档，让 agent 创建一个吧。</p>}
      </aside>

      <section className="artifact-reader">
        {error && <div className="error-banner compact">{error}<button onClick={() => setError('')}>×</button></div>}
        {selected ? (
          <>
            <header className="artifact-header">
              <div>
                <h2>{selected.meta.title}</h2>
                <span className="muted">
                  v{selected.version}
                  {selected.publishedVersion > 0 ? ` · 已发布 v${selected.publishedVersion}` : ' · 草稿未发布'}
                </span>
              </div>
              {selected.publishedVersion < selected.meta.version && (
                <button className="primary" onClick={() => void publish()} disabled={publishing}>
                  {publishing ? '发布中…' : '发布'}
                </button>
              )}
            </header>
            {selected.meta.type === 'html' ? (
              <iframe
                sandbox=""
                title={selected.meta.title}
                srcDoc={selected.content}
                className="artifact-frame"
              />
            ) : (
              <article
                className="markdown-body"
                dangerouslySetInnerHTML={{ __html: marked.parse(selected.content) as string }}
              />
            )}
          </>
        ) : (
          <div className="blank-state"><h2>选择左侧文档开始阅读</h2></div>
        )}
      </section>
    </div>
  );
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : String(cause);
}
