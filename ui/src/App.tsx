import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { API, ElectronTransport, WebTransport } from './transport';
import type { Approval, RunEvent, SessionSummary, SessionUpdate, Snapshot, TreeItem } from './types';

interface ToolLog {
  id: string;
  name: string;
  text: string;
  status: 'running' | 'done' | 'error';
}

export function App() {
  const desktop = Boolean(window.ggDesktop);
  const [endpoint, setEndpoint] = useState(() => sessionStorage.getItem('gg.endpoint') || '');
  const [token, setToken] = useState(() => sessionStorage.getItem('gg.token') || '');
  const [api, setAPI] = useState<API | null>(null);
  const [workspace, setWorkspace] = useState('');
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [current, setCurrent] = useState<Snapshot | null>(null);
  const [name, setName] = useState('');
  const [prompt, setPrompt] = useState('');
  const [runID, setRunID] = useState('');
  const [streamText, setStreamText] = useState('');
  const [toolLogs, setToolLogs] = useState<ToolLog[]>([]);
  const [approval, setApproval] = useState<Approval | null>(null);
  const [showTree, setShowTree] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const connected = api !== null;
  const activePath = useMemo(() => new Set((current?.treeItems ?? []).filter((item) => item.active).map((item) => item.id)), [current]);

  const loadSessions = useCallback(async (client: API) => {
    const items = await client.listSessions();
    setSessions(items);
    return items;
  }, []);

  const connect = useCallback(
    async (client: API) => {
      setError('');
      try {
        const [items, label] = await Promise.all([loadSessions(client), client.transport.workspaceLabel()]);
        setAPI(client);
        setWorkspace(label);
        if (items[0]) {
          const snapshot = await client.openSession(items[0].id);
          setCurrent(snapshot);
        }
      } catch (cause) {
        setAPI(null);
        setError(errorMessage(cause));
      }
    },
    [loadSessions],
  );

  useEffect(() => {
    if (desktop) void connect(new API(new ElectronTransport()));
  }, [connect, desktop]);

  useEffect(() => setName(current?.sessionName || ''), [current?.sessionId, current?.sessionName]);

  async function connectWeb(event: FormEvent) {
    event.preventDefault();
    sessionStorage.setItem('gg.endpoint', endpoint);
    sessionStorage.setItem('gg.token', token);
    await connect(new API(new WebTransport(endpoint, token)));
  }

  async function createSession() {
    if (!api) return;
    await action(async () => {
      const snapshot = await api.createSession();
      setCurrent(snapshot);
      setPrompt('');
      await loadSessions(api);
    });
  }

  async function openSession(sessionId: string) {
    if (!api || busy) return;
    await action(async () => {
      setCurrent(await api.openSession(sessionId));
      setPrompt('');
      setStreamText('');
      setToolLogs([]);
    });
  }

  async function renameSession() {
    if (!api || !current) return;
    await action(async () => {
      setCurrent(await api.renameSession(current.sessionId, name));
      await loadSessions(api);
    });
  }

  async function applySessionAction(actionName: 'tree' | 'fork' | 'clone', nodeId = '') {
    if (!api || !current || busy) return;
    await action(async () => {
      const update = await api.sessionAction(current.sessionId, actionName, nodeId);
      setCurrent(updateToSnapshot(update, current.modelName));
      if (update.draft !== undefined) setPrompt(update.draft);
      if (actionName !== 'tree') await loadSessions(api);
      if (actionName === 'fork') setShowTree(false);
    });
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!api || !current || busy || !prompt.trim()) return;
    const sessionID = current.sessionId;
    const text = prompt.trim();
    setPrompt('');
    setBusy(true);
    setError('');
    setStreamText('');
    setToolLogs([]);
    setApproval(null);
    try {
      const started = await api.startRun(sessionID, text, true);
      setRunID(started.runId);
      let after = 0;
      let done = false;
      while (!done) {
        const batch = await api.waitRun(started.runId, after);
        for (const eventItem of batch.events) {
          after = eventItem.sequence;
          handleRunEvent(eventItem);
        }
        done = batch.done;
      }
      setCurrent(await api.getSession(sessionID));
      await loadSessions(api);
    } catch (cause) {
      setError(errorMessage(cause));
      setPrompt((value) => value || text);
    } finally {
      setBusy(false);
      setRunID('');
      setApproval(null);
      setStreamText('');
      setToolLogs([]);
    }
  }

  function handleRunEvent(event: RunEvent) {
    if (event.type === 'agent_event' && event.agent) {
      const agentEvent = event.agent;
      if (agentEvent.type === 'text_delta') setStreamText((value) => value + (agentEvent.text || ''));
      if (agentEvent.type === 'tool_call_start') {
        setToolLogs((logs) => [
          ...logs,
          { id: agentEvent.toolCallId || event.id, name: agentEvent.toolName || 'tool', text: agentEvent.summary || '', status: 'running' },
        ]);
      }
      if (agentEvent.type === 'tool_call_finish') {
        setToolLogs((logs) =>
          logs.map((log) =>
            log.id === agentEvent.toolCallId
              ? { ...log, text: agentEvent.details || agentEvent.summary || log.text, status: agentEvent.isError ? 'error' : 'done' }
              : log,
          ),
        );
      }
    }
    if (event.type === 'approval_requested' && event.approval) setApproval(event.approval);
    if (event.type === 'run_failed' || event.type === 'run_canceled') setError(event.error || event.type.replace('_', ' '));
  }

  async function decideApproval(allow: boolean) {
    if (!api || !runID || !approval) return;
    try {
      await api.approve(runID, approval.id, allow);
      setApproval(null);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function cancelRun() {
    if (!api || !runID) return;
    try {
      await api.cancelRun(runID);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function steerRun(followUp: boolean) {
    if (!api || !current || !busy || !prompt.trim()) return;
    const text = prompt.trim();
    try {
      await api.steer(current.sessionId, text, followUp);
      setPrompt('');
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  async function action(work: () => Promise<void>) {
    setError('');
    try {
      await work();
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  if (!connected) {
    return (
      <main className="connect-shell">
        <section className="connect-card">
          <div className="brand-mark">g</div>
          <p className="eyebrow">GO CODING AGENT</p>
          <h1>{desktop ? '正在启动本地服务' : '连接 gg 服务'}</h1>
          <p className="muted">{desktop ? '客户端会在本机启动隔离的 Go 后端。' : 'Web 端通过受保护的 HTTP API 使用同一套会话与 Agent 核心。'}</p>
          {!desktop && (
            <form onSubmit={connectWeb} className="connect-form">
              <label>
                服务地址
                <input value={endpoint} onChange={(event) => setEndpoint(event.target.value)} placeholder="留空表示当前站点" />
              </label>
              <label>
                Bearer Token
                <input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" />
              </label>
              <button className="primary" type="submit" disabled={!token}>连接</button>
            </form>
          )}
          {error && <div className="error-banner">{error}</div>}
        </section>
      </main>
    );
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <header className="sidebar-header">
          <div className="brand"><span>g</span><strong>gg</strong></div>
          <button className="icon-button" onClick={createSession} title="新建会话">＋</button>
        </header>
        <div className="workspace-label" title={workspace}>{workspace}</div>
        <nav className="session-list" aria-label="会话列表">
          {sessions.map((session) => (
            <button
              key={session.id}
              className={`session-item ${current?.sessionId === session.id ? 'selected' : ''}`}
              onClick={() => openSession(session.id)}
            >
              <strong>{session.name || session.preview || '未命名会话'}</strong>
              <span>{session.messageCount} 条消息 · {formatTime(session.updatedAt)}</span>
            </button>
          ))}
          {sessions.length === 0 && <p className="empty-note">还没有会话，点击右上角开始。</p>}
        </nav>
      </aside>

      <main className="conversation-pane">
        {current ? (
          <>
            <header className="topbar">
              <div className="title-editor">
                <input value={name} onChange={(event) => setName(event.target.value)} onBlur={renameSession} aria-label="会话名称" />
                <span>{current.modelName}</span>
              </div>
              <div className="topbar-actions">
                <button onClick={() => setShowTree((value) => !value)}>{showTree ? '收起树' : '会话树'}</button>
                <button onClick={() => applySessionAction('clone')} disabled={busy}>克隆</button>
              </div>
            </header>

            <div className="content-grid">
              <section className="message-scroll">
                <div className="messages">
                  {(current.messages ?? []).filter((message) => message.role !== 'system').map((message, index) => (
                    <article className={`message ${message.role}`} key={`${index}-${message.toolCallId || ''}`}>
                      <div className="message-role">{roleLabel(message.role)}</div>
                      <div className="message-content">{message.content || message.error || '—'}</div>
                    </article>
                  ))}
                  {toolLogs.map((log) => (
                    <article className="tool-log" key={log.id}>
                      <span className={`status-dot ${log.status}`} />
                      <strong>{log.name}</strong>
                      <pre>{log.text}</pre>
                    </article>
                  ))}
                  {streamText && (
                    <article className="message assistant streaming">
                      <div className="message-role">gg</div>
                      <div className="message-content">{streamText}<span className="cursor" /></div>
                    </article>
                  )}
                </div>
              </section>

              {showTree && (
                <aside className="tree-panel">
                  <div className="tree-title"><strong>Conversation tree</strong><span>{(current.treeItems ?? []).length} nodes</span></div>
                  <div className="tree-list">
                    {(current.treeItems ?? []).map((node) => (
                      <TreeNode
                        key={node.id}
                        node={node}
                        active={activePath.has(node.id)}
                        disabled={busy}
                        onCheckout={() => applySessionAction('tree', node.id)}
                        onFork={() => applySessionAction('fork', node.id)}
                      />
                    ))}
                  </div>
                </aside>
              )}
            </div>

            <footer className="composer-wrap">
              {approval && (
                <div className="approval-card">
                  <div><span>需要确认</span><strong>{approval.request.summary || approval.request.toolName}</strong><small>{approval.request.details}</small></div>
                  <div><button onClick={() => decideApproval(false)}>拒绝</button><button className="primary" onClick={() => decideApproval(true)}>允许</button></div>
                </div>
              )}
              {error && <div className="error-banner compact">{error}<button onClick={() => setError('')}>×</button></div>}
              <form className="composer" onSubmit={submit}>
                <textarea
                  value={prompt}
                  onChange={(event) => setPrompt(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                      event.preventDefault();
                      if (busy) void steerRun(false);
                      else event.currentTarget.form?.requestSubmit();
                    }
                  }}
                  placeholder="告诉 gg 你想完成什么…"
                  rows={3}
                  disabled={Boolean(approval)}
                />
                <div className="composer-actions">
                  <span>{busy ? 'Enter 补充当前任务' : 'Enter 发送 · Shift+Enter 换行'}</span>
                  {busy ? (
                    <div className="run-actions">
                      <button type="button" onClick={() => steerRun(true)} disabled={!prompt.trim()}>下一轮</button>
                      <button className="primary" type="button" onClick={() => steerRun(false)} disabled={!prompt.trim()}>补充</button>
                      <button type="button" onClick={cancelRun}>停止</button>
                    </div>
                  ) : <button className="primary" type="submit" disabled={!prompt.trim()}>发送</button>}
                </div>
              </form>
            </footer>
          </>
        ) : (
          <section className="blank-state"><div className="brand-mark">g</div><h2>开始一个新会话</h2><button className="primary" onClick={createSession}>新建会话</button></section>
        )}
      </main>
    </div>
  );
}

function TreeNode({ node, active, disabled, onCheckout, onFork }: { node: TreeItem; active: boolean; disabled: boolean; onCheckout: () => void; onFork: () => void }) {
  return (
    <div className={`tree-node ${active ? 'active' : ''}`} style={{ marginLeft: `${Math.min(node.depth || 0, 6) * 12}px` }}>
      <button className="tree-main" onClick={onCheckout} disabled={disabled}>
        <span>{active ? '●' : '○'}</span><div><strong>{roleLabel(node.role)}</strong><p>{node.text || '空消息'}</p></div>
      </button>
      {node.role === 'user' && <button className="fork-button" onClick={onFork} disabled={disabled}>fork</button>}
    </div>
  );
}

function updateToSnapshot(update: SessionUpdate, fallbackModel: string): Snapshot {
  return {
    sessionId: update.sessionId,
    sessionName: update.sessionName,
    modelName: update.modelName || fallbackModel,
    messages: update.messages ?? [],
    treeItems: update.treeItems ?? [],
  };
}

function roleLabel(role: string) {
  if (role === 'user') return '你';
  if (role === 'assistant') return 'gg';
  if (role === 'tool') return '工具';
  return role;
}

function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(date);
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : String(cause);
}
