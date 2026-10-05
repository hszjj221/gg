import { useEffect, useRef, useState } from 'react';
import type { API } from './transport';
import type { ArtifactMeta, ArtifactView } from './types';
import { Icon } from './Icons';
import { Markdown } from './Markdown';

export function ArtifactsPane({ api }: { api: API }) {
  const [items, setItems] = useState<ArtifactMeta[]>([]);
  const [selected, setSelected] = useState<ArtifactView | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadingId, setLoadingId] = useState('');
  const [publishing, setPublishing] = useState(false);
  const [notice, setNotice] = useState('');
  const requestRef = useRef(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api
      .listArtifacts()
      .then((list) => {
        if (!cancelled)
          setItems([...list].sort((a, b) => (Date.parse(b.updated_at) || 0) - (Date.parse(a.updated_at) || 0)));
      })
      .catch((cause) => {
        if (!cancelled) setError(errorMessage(cause));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      requestRef.current += 1;
    };
  }, [api]);

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(''), 3500);
    return () => window.clearTimeout(timer);
  }, [notice]);

  async function open(id: string) {
    if (publishing || (selected?.meta.id === id && !loadingId)) return;
    const request = ++requestRef.current;
    setError('');
    setLoadingId(id);
    try {
      const artifact = await api.getArtifact(id);
      if (requestRef.current === request) setSelected(artifact);
    } catch (cause) {
      if (requestRef.current === request) setError(errorMessage(cause));
    } finally {
      if (requestRef.current === request) setLoadingId('');
    }
  }

  async function publish() {
    if (!selected || publishing || loadingId) return;
    const id = selected.meta.id;
    const request = requestRef.current;
    setPublishing(true);
    setError('');
    try {
      await api.publishArtifact(id);
      const [artifact, list] = await Promise.all([api.getArtifact(id), api.listArtifacts()]);
      if (requestRef.current !== request) return;
      setSelected(artifact);
      setItems(list);
      setNotice('文档已保存到资料库。');
    } catch (cause) {
      if (requestRef.current === request) setError(errorMessage(cause));
    } finally {
      if (requestRef.current === request) setPublishing(false);
    }
  }

  return (
    <div className={`artifacts-pane ${selected || loadingId ? 'has-selection' : ''}`}>
      <aside className="artifact-list" aria-label="文档列表">
        <div className="artifact-list-heading">
          <span>全部文档</span>
          <span>{items.length}</span>
        </div>
        <div className="artifact-list-items">
          {loading ? (
            <div className="loading-state" role="status">
              <Icon name="loader" className="spin" size={16} />
              正在加载文档…
            </div>
          ) : (
            items.map((item) => (
              <button
                key={item.id}
                className={`artifact-item ${selected?.meta.id === item.id || loadingId === item.id ? 'selected' : ''}`}
                onClick={() => void open(item.id)}
                disabled={publishing}
                aria-current={selected?.meta.id === item.id ? 'page' : undefined}
              >
                <Icon name={item.type === 'html' ? 'code' : 'document'} size={18} />
                <div>
                  <strong>{item.title}</strong>
                  <span>
                    {item.type === 'html' ? '网页' : '文档'} · v{item.version}
                    <br />
                    {item.published_version > 0 ? `已存入资料库 v${item.published_version}` : '尚未存入资料库'}
                  </span>
                </div>
              </button>
            ))
          )}
          {!loading && items.length === 0 && (
            <div className="empty-note">
              <Icon name="document" size={27} />
              <p>
                这里收藏你的创作成果。
                <br />让 gg 在会话里创建第一份文档吧。
              </p>
            </div>
          )}
        </div>
      </aside>
      <section className="artifact-reader" aria-label="文档阅读区" aria-busy={Boolean(loadingId)}>
        <button
          className="artifact-mobile-back text-button"
          onClick={() => {
            if (publishing) return;
            requestRef.current += 1;
            setSelected(null);
            setLoadingId('');
          }}
          disabled={publishing}
        >
          <Icon name="chevron" size={13} style={{ transform: 'rotate(180deg)' }} />
          返回文档列表
        </button>
        {loadingId ? (
          <div className="blank-state" role="status">
            <Icon name="loader" className="spin" size={26} />
            <p>正在打开文档…</p>
          </div>
        ) : selected ? (
          <>
            <header className="artifact-header">
              <div>
                <h2>{selected.meta.title}</h2>
                <span className="muted">
                  版本 {selected.version}
                  <span className="document-status">
                    {selected.publishedVersion > 0 ? `资料库 v${selected.publishedVersion}` : '草稿'}
                  </span>
                </span>
              </div>
              {selected.publishedVersion < selected.meta.version ? (
                <button className="primary" onClick={() => void publish()} disabled={publishing}>
                  {publishing ? <Icon name="loader" className="spin" size={14} /> : <Icon name="check" size={14} />}
                  {publishing ? '保存中…' : '存入资料库'}
                </button>
              ) : (
                <span className="document-status">已存入资料库</span>
              )}
            </header>
            {selected.meta.type === 'html' ? (
              <iframe sandbox="" title={selected.meta.title} srcDoc={selected.content} className="artifact-frame" />
            ) : (
              <article className="artifact-document">
                <Markdown content={selected.content} />
              </article>
            )}
          </>
        ) : (
          <div className="blank-state">
            <Icon name="document" size={38} />
            <h2>每一个想法，都值得留下。</h2>
            <p>选择一份文档，继续阅读你的创作成果。</p>
          </div>
        )}
      </section>
      {error && (
        <div className="app-error error-banner" role="alert">
          <Icon name="info" size={16} />
          <span>{error}</span>
          <button className="icon-button" aria-label="关闭错误提示" onClick={() => setError('')}>
            <Icon name="close" size={14} />
          </button>
        </div>
      )}
      {notice && (
        <div className="toast" role="status">
          <Icon name="check" size={16} />
          {notice}
        </div>
      )}
    </div>
  );
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : String(cause);
}
