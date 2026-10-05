import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { API, HttpTransport, protocolVersion, RPCError } from './transport';
import { ArtifactsPane } from './Artifacts';
import { Icon, type IconName } from './Icons';
import { Markdown } from './Markdown';
import type { Approval, RunEvent, RunStatus, SessionSummary, SessionUpdate, Snapshot, TreeItem } from './types';

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
  const [prompt, setPrompt] = useState(() => sessionStorage.getItem('gg.draft.new') || '');
  const [runID, setRunID] = useState('');
  const [streamText, setStreamText] = useState('');
  const [toolLogs, setToolLogs] = useState<ToolLog[]>([]);
  const [approval, setApproval] = useState<Approval | null>(null);
  const [showTree, setShowTree] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [view, setView] = useState<'chat' | 'artifacts'>('chat');
  const [connecting, setConnecting] = useState(false);
  const [actionPending, setActionPending] = useState(false);
  const [approving, setApproving] = useState(false);
  const [steering, setSteering] = useState(false);
  const [notification, setNotification] = useState('');
  const [search, setSearch] = useState('');
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    () => localStorage.getItem('gg.sidebarCollapsed') === 'true',
  );
  const [mobileSidebar, setMobileSidebar] = useState(false);
  const [isMobile, setIsMobile] = useState(() => window.matchMedia('(max-width: 760px)').matches);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const sidebarToggleRef = useRef<HTMLButtonElement>(null);
  const messageScrollRef = useRef<HTMLElement>(null);
  const stickToBottomRef = useRef(true);
  const actionLockRef = useRef(false);
  const cancelRenameRef = useRef(false);
  const draftSessionRef = useRef('new');
  const watchAbortRef = useRef<AbortController | null>(null);
  const watchGenerationRef = useRef(0);
  const connectGenerationRef = useRef(0);

  const connected = api !== null;
  const activePath = useMemo(
    () => new Set((current?.treeItems ?? []).filter((item) => item.active).map((item) => item.id)),
    [current],
  );
  const sessionGroups = useMemo(
    () =>
      groupSessions(
        sessions.filter((session) =>
          `${session.name} ${session.preview}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()),
        ),
      ),
    [sessions, search],
  );
  const messages = (current?.messages ?? []).filter(
    (message) => message.role !== 'system' && (message.content || message.error),
  );
  const welcome = messages.length === 0 && !busy;
  const workspaceName = workspace.replace(/\/$/, '').split(/[\\/]/).pop() || '当前工作区';
  const modifier = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl';

  const loadSessions = useCallback(async (client: API) => {
    const items = await client.listSessions();
    setSessions(items);
    return items;
  }, []);

  const connect = useCallback(
    async (client: API) => {
      const generation = ++connectGenerationRef.current;
      setConnecting(true);
      watchAbortRef.current?.abort();
      watchGenerationRef.current += 1;
      setError('');
      try {
        const [items, label, info] = await Promise.all([
          loadSessions(client),
          client.transport.workspaceLabel(),
          client.systemInfo(),
        ]);
        if (connectGenerationRef.current !== generation) return;
        if (info.protocolVersion.split('.')[0] !== protocolVersion.split('.')[0]) {
          throw new Error(`协议版本不兼容：客户端 ${protocolVersion}，服务端 ${info.protocolVersion}`);
        }
        client.setCapabilities(info.capabilities);
        setAPI(client);
        setWorkspace(label);
        const savedSessionID = sessionStorage.getItem('gg.sessionId');
        const selected = items.find((item) => item.id === savedSessionID) || items[0];
        if (selected) {
          const snapshot = await client.openSession(selected.id);
          if (connectGenerationRef.current !== generation) return;
          setCurrent(snapshot);
          sessionStorage.setItem('gg.sessionId', snapshot.sessionId);
          const active = client.supports('run.reattach') ? await client.activeRun(snapshot.sessionId) : null;
          if (active && connectGenerationRef.current === generation) void attachRun(client, snapshot.sessionId, active);
        } else {
          setCurrent(null);
        }
      } catch (cause) {
        if (connectGenerationRef.current === generation) {
          setAPI(null);
          setError(errorMessage(cause));
        }
      } finally {
        if (connectGenerationRef.current === generation) setConnecting(false);
      }
    },
    [loadSessions],
  );

  useEffect(() => {
    if (!desktop) {
      const savedToken = sessionStorage.getItem('gg.token');
      if (savedToken) void connect(new API(new HttpTransport(sessionStorage.getItem('gg.endpoint') || '', savedToken)));
      return;
    }
    void (async () => {
      try {
        const conn = await window.ggDesktop?.getConnection();
        if (!conn) throw new Error('Electron bridge is unavailable');
        await connect(new API(new HttpTransport(conn.endpoint, conn.token, conn.workspace)));
      } catch (cause) {
        setError(errorMessage(cause));
      }
    })();
  }, [connect, desktop]);

  useEffect(
    () => () => {
      watchAbortRef.current?.abort();
      watchGenerationRef.current += 1;
      connectGenerationRef.current += 1;
    },
    [],
  );

  useEffect(() => setName(current?.sessionName || ''), [current?.sessionId, current?.sessionName]);

  useEffect(() => {
    const sessionId = current?.sessionId || 'new';
    if (draftSessionRef.current !== sessionId) {
      draftSessionRef.current = sessionId;
      setPrompt(sessionStorage.getItem(`gg.draft.${sessionId}`) || '');
      return;
    }
    sessionStorage.setItem(`gg.draft.${sessionId}`, prompt);
  }, [current?.sessionId, prompt]);

  useEffect(() => {
    const textarea = composerRef.current;
    if (!textarea) return;
    textarea.style.height = 'auto';
    textarea.style.height = `${Math.min(textarea.scrollHeight, 200)}px`;
  }, [prompt, view, connected]);

  useEffect(() => {
    stickToBottomRef.current = true;
    setShowScrollButton(false);
    const frame = requestAnimationFrame(() => {
      const element = messageScrollRef.current;
      if (element) element.scrollTop = welcome ? 0 : element.scrollHeight;
    });
    return () => cancelAnimationFrame(frame);
  }, [current?.sessionId, view, welcome]);

  useEffect(() => {
    if (welcome || !stickToBottomRef.current) return;
    const element = messageScrollRef.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [current?.messages, streamText, toolLogs, approval, welcome]);

  useEffect(() => {
    if (!notification) return;
    const timeout = window.setTimeout(() => setNotification(''), 3500);
    return () => window.clearTimeout(timeout);
  }, [notification]);

  useEffect(() => {
    localStorage.setItem('gg.sidebarCollapsed', String(sidebarCollapsed));
  }, [sidebarCollapsed]);

  useEffect(() => {
    const query = window.matchMedia('(max-width: 760px)');
    const update = () => {
      setIsMobile(query.matches);
      setMobileSidebar(false);
    };
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);

  useEffect(() => {
    function handleShortcut(event: KeyboardEvent) {
      if (event.isComposing) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k' && connected) {
        event.preventDefault();
        setSidebarCollapsed(false);
        setMobileSidebar(true);
        requestAnimationFrame(() => searchRef.current?.focus());
      }
      if ((event.metaKey || event.ctrlKey) && !event.shiftKey && event.key.toLowerCase() === 'n' && connected) {
        event.preventDefault();
        void createSession();
      }
      if (event.key === 'Escape') {
        setMobileSidebar(false);
        setShowTree(false);
      }
    }
    window.addEventListener('keydown', handleShortcut);
    return () => window.removeEventListener('keydown', handleShortcut);
  });

  useEffect(() => {
    if (!mobileSidebar || !isMobile) return;
    const previousFocus = document.activeElement as HTMLElement | null;
    searchRef.current?.focus();
    function trapFocus(event: KeyboardEvent) {
      if (event.key !== 'Tab') return;
      const controls = Array.from(
        document.querySelectorAll<HTMLElement>('.sidebar button:not(:disabled), .sidebar input'),
      ).filter((element) => element.getClientRects().length > 0);
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    }
    document.addEventListener('keydown', trapFocus);
    return () => {
      document.removeEventListener('keydown', trapFocus);
      if (previousFocus && previousFocus !== document.body) previousFocus.focus();
      else sidebarToggleRef.current?.focus();
    };
  }, [mobileSidebar, isMobile]);

  async function connectWeb(event: FormEvent) {
    event.preventDefault();
    sessionStorage.setItem('gg.endpoint', endpoint);
    sessionStorage.setItem('gg.token', token);
    await connect(new API(new HttpTransport(endpoint, token)));
  }

  async function createSession() {
    if (!api || busy || actionLockRef.current) return;
    await action(async () => {
      const snapshot = await api.createSession();
      setCurrent(snapshot);
      sessionStorage.setItem('gg.sessionId', snapshot.sessionId);
      setView('chat');
      setSearch('');
      setMobileSidebar(false);
      setShowTree(false);
      setStreamText('');
      setToolLogs([]);
      requestAnimationFrame(() => composerRef.current?.focus());
      await loadSessions(api);
    });
  }

  async function openSession(sessionId: string) {
    if (!api || busy || actionLockRef.current) return;
    setView('chat');
    setMobileSidebar(false);
    if (current?.sessionId === sessionId) return;
    await action(async () => {
      const snapshot = await api.openSession(sessionId);
      setCurrent(snapshot);
      sessionStorage.setItem('gg.sessionId', snapshot.sessionId);
      setStreamText('');
      setToolLogs([]);
      setApproval(null);
      const active = api.supports('run.reattach') ? await api.activeRun(snapshot.sessionId) : null;
      if (active) void attachRun(api, snapshot.sessionId, active);
    });
  }

  async function renameSession() {
    if (cancelRenameRef.current) {
      cancelRenameRef.current = false;
      return;
    }
    if (!api || !current || busy || name.trim() === current.sessionName || actionLockRef.current) return;
    await action(async () => {
      setCurrent(await api.renameSession(current.sessionId, name.trim()));
      await loadSessions(api);
    });
  }

  async function applySessionAction(actionName: 'tree' | 'fork' | 'clone', nodeId = '') {
    if (!api || !current || busy || actionLockRef.current) return;
    await action(async () => {
      const update = await api.sessionAction(current.sessionId, actionName, nodeId);
      setCurrent(updateToSnapshot(update, current.modelName));
      sessionStorage.setItem('gg.sessionId', update.sessionId);
      if (update.draft !== undefined) {
        sessionStorage.setItem(`gg.draft.${update.sessionId}`, update.draft);
        setPrompt(update.draft);
      }
      if (update.notice) setNotification(update.notice);
      else if (actionName === 'clone') setNotification('已复制会话，可以从这里继续。');
      if (actionName !== 'tree') await loadSessions(api);
      if (actionName === 'fork') setShowTree(false);
    });
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!api || busy || actionLockRef.current || !prompt.trim()) return;
    let snapshot = current;
    const text = prompt.trim();
    setPrompt('');
    setBusy(true);
    setError('');
    setStreamText('');
    setToolLogs([]);
    setApproval(null);
    let started: { runId: string };
    try {
      if (!snapshot) {
        snapshot = await api.createSession();
        sessionStorage.setItem('gg.sessionId', snapshot.sessionId);
        sessionStorage.removeItem('gg.draft.new');
        setCurrent(snapshot);
        await loadSessions(api);
      }
      const sessionID = snapshot.sessionId;
      started = await api.startRun(sessionID, text, true);
      setCurrent({ ...snapshot, messages: [...(snapshot.messages ?? []), { role: 'user', content: text }] });
      stickToBottomRef.current = true;
    } catch (cause) {
      setError(errorMessage(cause));
      setPrompt((value) => value || text);
      setBusy(false);
      return;
    }
    await attachRun(api, snapshot.sessionId, {
      id: started.runId,
      sessionId: snapshot.sessionId,
      done: false,
      startedAt: Date.now(),
      firstSequence: 1,
      lastSequence: 0,
      pendingApprovals: [],
    });
  }

  async function attachRun(client: API, sessionID: string, initialStatus: RunStatus) {
    watchAbortRef.current?.abort();
    const controller = new AbortController();
    watchAbortRef.current = controller;
    const generation = ++watchGenerationRef.current;
    let status = initialStatus;
    let after = 0;
    setBusy(true);
    setRunID(status.id);
    setStreamText('');
    setToolLogs([]);
    setApproval(status.pendingApprovals[0] || null);

    try {
      while (!controller.signal.aborted) {
        try {
          after = await client.watchRun(
            status.id,
            after,
            (eventItem) => {
              if (watchGenerationRef.current === generation) handleRunEvent(eventItem);
            },
            controller.signal,
          );
          break;
        } catch (cause) {
          if (controller.signal.aborted || isAbortError(cause)) return;
          if (!(cause instanceof RPCError) || cause.code !== 'event_history_expired') throw cause;
          if (!client.supports('run.reattach')) {
            setCurrent(await client.getSession(sessionID));
            setError('部分实时事件已过期，已恢复最新会话内容。');
            break;
          }
          const [snapshot, latest] = await Promise.all([client.getSession(sessionID), client.runStatus(status.id)]);
          if (watchGenerationRef.current !== generation) return;
          setCurrent(snapshot);
          setStreamText('');
          setToolLogs([]);
          setApproval(latest.pendingApprovals[0] || null);
          setError('部分实时事件已过期，已从最新会话状态恢复。');
          status = latest;
          if (latest.done) break;
          after = Math.max(0, latest.firstSequence - 1);
        }
      }
    } catch (cause) {
      if (!controller.signal.aborted && watchGenerationRef.current === generation) setError(errorMessage(cause));
    } finally {
      if (watchGenerationRef.current !== generation) return;
      try {
        setCurrent(await client.getSession(sessionID));
        await loadSessions(client);
      } catch (cause) {
        setError(errorMessage(cause));
      }
      if (watchGenerationRef.current === generation) {
        setBusy(false);
        setRunID('');
        setApproval(null);
        setStreamText('');
        setToolLogs([]);
        watchAbortRef.current = null;
      }
    }
  }

  function handleRunEvent(event: RunEvent) {
    if (event.type === 'agent_event' && event.agent) {
      const agentEvent = event.agent;
      if (agentEvent.type === 'text_delta') setStreamText((value) => value + (agentEvent.text || ''));
      if (agentEvent.type === 'user_message') setNotification('补充要求已交给 gg。');
      if (agentEvent.type === 'tool_call_start') {
        setToolLogs((logs) => [
          ...logs,
          {
            id: agentEvent.toolCallId || event.id,
            name: agentEvent.toolName || 'tool',
            text: agentEvent.summary || '',
            status: 'running',
          },
        ]);
      }
      if (agentEvent.type === 'tool_call_finish') {
        setToolLogs((logs) =>
          logs.map((log) =>
            log.id === agentEvent.toolCallId
              ? {
                  ...log,
                  text: agentEvent.details || agentEvent.summary || log.text,
                  status: agentEvent.isError ? 'error' : 'done',
                }
              : log,
          ),
        );
      }
    }
    if (event.type === 'approval_requested' && event.approval) setApproval(event.approval);
    if (event.type === 'approval_resolved' && event.approval) {
      setApproval((value) => (value?.id === event.approval?.id ? null : value));
    }
    if (event.type === 'run_failed') setError(event.error || '任务未能完成，请重试。');
    if (event.type === 'run_canceled') setNotification('任务已停止，已生成的内容会保留。');
  }

  async function decideApproval(allow: boolean) {
    if (!api || !runID || !approval || approving) return;
    setApproving(true);
    try {
      await api.approve(runID, approval.id, allow);
      setApproval(null);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setApproving(false);
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
    if (!api || !current || !runID || !busy || !prompt.trim() || steering || approval) return;
    const text = prompt.trim();
    setSteering(true);
    try {
      await api.steer(current.sessionId, text, followUp);
      setPrompt((value) => (value.trim() === text ? '' : value));
      setNotification(followUp ? '已加入下一轮任务。' : '已补充到当前任务。');
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSteering(false);
    }
  }

  async function action(work: () => Promise<void>) {
    if (actionLockRef.current) return;
    actionLockRef.current = true;
    setActionPending(true);
    setError('');
    try {
      await work();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      actionLockRef.current = false;
      setActionPending(false);
    }
  }

  if (!connected) {
    return (
      <main className={`connect-shell ${desktop ? 'desktop' : ''}`}>
        <div className="connect-wordmark">
          <span className="brand-mark">g</span>
          <strong>gg</strong>
          <span>你的个人 AI 工作台</span>
        </div>
        <section className="connect-card">
          <div className="welcome-symbol">
            <Icon name="sparkle" size={32} />
          </div>
          <p className="eyebrow">A LITTLE HELP. A LOT OF POSSIBILITY.</p>
          <h1>{desktop ? '正在准备你的工作台' : '让想法，从这里开始。'}</h1>
          <p className="muted">
            {desktop ? '连接本地服务后，就可以继续你的工作。' : '连接你的 gg 服务，与 AI 一起探索、创作和完成工作。'}
          </p>
          {!desktop && (
            <form onSubmit={connectWeb} className="connect-form">
              <label htmlFor="endpoint">
                服务地址 <span className="field-optional">可选</span>
              </label>
              <input
                id="endpoint"
                type="text"
                inputMode="url"
                value={endpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                placeholder="当前站点，或 http://127.0.0.1:8765"
                autoComplete="url"
                disabled={connecting}
              />
              <label htmlFor="token">访问令牌</label>
              <input
                id="token"
                type="password"
                value={token}
                onChange={(event) => setToken(event.target.value)}
                placeholder="输入服务启动时设置的 Token"
                autoComplete="off"
                disabled={connecting}
                required
              />
              <button className="primary connect-submit" type="submit" disabled={!token.trim() || connecting}>
                {connecting ? <Icon name="loader" className="spin" /> : <Icon name="arrow" />}
                {connecting ? '正在连接…' : '进入工作台'}
              </button>
              <p className="connect-hint">
                <Icon name="info" size={14} />
                服务地址留空时，连接当前站点的服务。
              </p>
            </form>
          )}
          {desktop && !error && (
            <div className="connecting-status" role="status">
              <Icon name="loader" className="spin" />
              正在连接本地服务…
            </div>
          )}
          {error && (
            <div className="error-banner" role="alert">
              <Icon name="info" size={16} />
              <span>{error}</span>
              {desktop && <button onClick={() => window.location.reload()}>重试</button>}
            </div>
          )}
        </section>
        <span className="connect-footer">一点灵感，一起完成。</span>
      </main>
    );
  }

  return (
    <div
      className={`app-shell ${desktop ? 'desktop' : ''} ${sidebarCollapsed ? 'sidebar-collapsed' : ''} ${mobileSidebar ? 'mobile-sidebar-open' : ''}`}
    >
      {mobileSidebar && (
        <button className="sidebar-backdrop" aria-label="关闭会话导航" onClick={() => setMobileSidebar(false)} />
      )}
      <aside
        className="sidebar"
        aria-label="工作台导航"
        inert={!mobileSidebar && (sidebarCollapsed || isMobile)}
        role={isMobile && mobileSidebar ? 'dialog' : undefined}
        aria-modal={isMobile && mobileSidebar ? true : undefined}
      >
        <header className="sidebar-header">
          <div className="brand">
            <span className="brand-mark">g</span>
            <strong>gg</strong>
            <span className="brand-caption">个人 AI 工作台</span>
          </div>
          <button
            className="icon-button collapse-button"
            onClick={() => {
              setSidebarCollapsed(true);
              setMobileSidebar(false);
            }}
            title="收起导航"
            aria-label="收起导航"
          >
            <Icon name="panel" />
          </button>
          <button className="icon-button mobile-close" onClick={() => setMobileSidebar(false)} aria-label="关闭导航">
            <Icon name="close" />
          </button>
        </header>
        <button
          className="new-session-button"
          onClick={() => void createSession()}
          disabled={busy || actionPending}
          title={`新建会话 (${modifier}+N)`}
        >
          <Icon name="plus" />
          <span>新建会话</span>
          <kbd>{modifier} N</kbd>
        </button>
        <div className="sidebar-nav" aria-label="视图切换">
          <button
            aria-current={view === 'chat' ? 'page' : undefined}
            className={view === 'chat' ? 'active' : ''}
            onClick={() => {
              setView('chat');
              setMobileSidebar(false);
            }}
          >
            <Icon name="chat" />
            <span>会话</span>
            <span className="nav-count">{sessions.length}</span>
          </button>
          {api?.supports('artifact') && (
            <button
              aria-current={view === 'artifacts' ? 'page' : undefined}
              className={view === 'artifacts' ? 'active' : ''}
              onClick={() => {
                setView('artifacts');
                setMobileSidebar(false);
              }}
            >
              <Icon name="document" />
              <span>文档</span>
            </button>
          )}
        </div>
        <div className="sidebar-divider" />
        <div className="session-search">
          <Icon name="search" size={16} />
          <input
            ref={searchRef}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索会话"
            aria-label="搜索会话"
          />
          {search ? (
            <button
              className="icon-button"
              aria-label="清除搜索"
              onClick={() => {
                setSearch('');
                searchRef.current?.focus();
              }}
            >
              <Icon name="close" size={14} />
            </button>
          ) : (
            <kbd>{modifier} K</kbd>
          )}
        </div>
        <nav className="session-list" aria-label="会话列表" aria-busy={actionPending}>
          {sessionGroups.map((group) => (
            <div className="session-group" key={group.label}>
              <div className="section-label">{group.label}</div>
              {group.items.map((session) => (
                <button
                  key={session.id}
                  className={`session-item ${view === 'chat' && current?.sessionId === session.id ? 'selected' : ''}`}
                  onClick={() => void openSession(session.id)}
                  disabled={busy || actionPending}
                  aria-current={view === 'chat' && current?.sessionId === session.id ? 'page' : undefined}
                  title={session.name || session.preview || '新会话'}
                >
                  <span className="session-title">
                    <Icon name="chat" size={15} />
                    <strong>{session.name || session.preview || '新会话'}</strong>
                    {current?.sessionId === session.id && busy && <span className="status-dot pulse" />}
                  </span>
                  <span className="session-meta">
                    {session.messageCount} 条消息<span>·</span>
                    {formatTime(session.updatedAt)}
                  </span>
                </button>
              ))}
            </div>
          ))}
          {sessionGroups.length === 0 && (
            <div className="empty-note">
              <Icon name={search ? 'search' : 'chat'} size={24} />
              <p>{search ? '没有找到相关会话' : '你的灵感，会留在这里。'}</p>
              {search && (
                <button className="text-button" onClick={() => setSearch('')}>
                  清除搜索
                </button>
              )}
            </div>
          )}
        </nav>
        <footer className="sidebar-footer">
          <div className="workspace-icon">
            <Icon name="folder" size={18} />
          </div>
          <div className="workspace-info">
            <strong title={workspace}>{workspaceName}</strong>
            <span>
              <i className="status-dot done" />
              服务已连接
            </span>
          </div>
          <span className="local-badge">{desktop ? '本地' : '在线'}</span>
        </footer>
      </aside>

      <main className="conversation-pane" inert={mobileSidebar && isMobile}>
        <header className="topbar">
          <div className="topbar-leading">
            <button
              ref={sidebarToggleRef}
              className="icon-button sidebar-toggle"
              onClick={() => {
                setSidebarCollapsed(false);
                setMobileSidebar(true);
              }}
              aria-label="展开会话导航"
              title="展开导航"
            >
              <Icon name="panel" />
            </button>
            <div className="title-editor">
              <span className="breadcrumb">
                工作台<span>/</span>
                {view === 'artifacts' ? '文档' : '会话'}
              </span>
              {view === 'artifacts' ? (
                <strong className="page-title">我的文档</strong>
              ) : current ? (
                <input
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  onBlur={() => void renameSession()}
                  onKeyDown={(event) => {
                    if (event.nativeEvent.isComposing) return;
                    if (event.key === 'Enter') event.currentTarget.blur();
                    if (event.key === 'Escape') {
                      cancelRenameRef.current = true;
                      setName(current.sessionName);
                      event.currentTarget.blur();
                    }
                  }}
                  placeholder="新会话"
                  aria-label="会话名称，编辑后按 Enter 保存"
                  title="点击修改会话名称"
                  disabled={busy || actionPending}
                />
              ) : (
                <strong className="page-title">新会话</strong>
              )}
            </div>
          </div>
          <div className="topbar-actions">
            {view === 'chat' && current?.modelName && (
              <span className="model-badge" title={current.modelName}>
                <span className="status-dot done" />
                {current.modelName.split(':').pop()}
              </span>
            )}
            {view === 'chat' && current && (
              <>
                <button
                  className={`icon-button tree-toggle ${showTree ? 'active' : ''}`}
                  onClick={() => setShowTree((value) => !value)}
                  aria-label="会话分支"
                  aria-expanded={showTree}
                  aria-controls="conversation-tree"
                  title="查看会话分支"
                >
                  <Icon name="branch" />
                  <span>会话分支</span>
                </button>
                <button
                  className="icon-button"
                  onClick={() => void applySessionAction('clone')}
                  disabled={busy || actionPending}
                  aria-label="复制会话"
                  title="复制会话"
                >
                  <Icon name="copy" />
                </button>
              </>
            )}
          </div>
        </header>

        {view === 'artifacts' ? (
          api && <ArtifactsPane api={api} />
        ) : (
          <div className="content-grid">
            <div className={`chat-column ${welcome ? 'is-welcome' : ''}`}>
              <section
                className="message-scroll"
                ref={messageScrollRef}
                aria-label="会话内容"
                onScroll={(event) => {
                  const element = event.currentTarget;
                  const nearBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 96;
                  stickToBottomRef.current = nearBottom;
                  setShowScrollButton(!nearBottom);
                }}
              >
                {welcome ? (
                  <div className="welcome-state">
                    <div className="welcome-symbol">
                      <Icon name="sparkle" size={30} />
                    </div>
                    <p className="eyebrow">想法有了，下一步交给我们。</p>
                    <h1>有什么想一起完成的？</h1>
                    <p className="welcome-description">
                      从一个问题、一个想法，或一件待办开始。
                      <br />
                      gg 会陪你把它变成真正的进展。
                    </p>
                    <div className="starter-grid">
                      <Starter
                        icon="code"
                        title="读懂一个项目"
                        description="梳理结构，找到下一步"
                        onClick={() => {
                          setPrompt('帮我了解当前项目：梳理目录结构、核心功能，并建议下一步可以改进的地方。');
                          composerRef.current?.focus();
                        }}
                      />
                      <Starter
                        icon="document"
                        title="把想法写下来"
                        description="从零散灵感到清晰文档"
                        onClick={() => {
                          setPrompt('帮我把下面的想法整理成一份结构清晰的文档：\n');
                          composerRef.current?.focus();
                        }}
                      />
                      <Starter
                        icon="list"
                        title="拆解一项任务"
                        description="让复杂的事情有条理"
                        onClick={() => {
                          setPrompt('帮我把下面的任务拆成可执行的步骤，并标出优先级：\n');
                          composerRef.current?.focus();
                        }}
                      />
                    </div>
                    <div className="welcome-capabilities">
                      <span>
                        <Icon name="folder" size={14} />
                        理解工作区
                      </span>
                      <span>
                        <Icon name="branch" size={14} />
                        保留会话分支
                      </span>
                      {api?.supports('artifact') && (
                        <span>
                          <Icon name="document" size={14} />
                          沉淀为文档
                        </span>
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="messages">
                    {messages.map((message, index) =>
                      message.role === 'tool' ? (
                        <details
                          className={`tool-log ${message.error ? 'has-error' : ''}`}
                          key={`${index}-${message.toolCallId || ''}`}
                        >
                          <summary>
                            <Icon name="terminal" size={15} />
                            <strong>{message.toolName || '工具执行'}</strong>
                            <span>{message.error ? '执行失败' : '执行完成'}</span>
                            <Icon name="chevron" size={14} />
                          </summary>
                          <pre>{message.content || message.error}</pre>
                        </details>
                      ) : (
                        <article className={`message ${message.role}`} key={index}>
                          {message.role === 'assistant' && (
                            <div className="message-heading">
                              <span className="assistant-avatar">g</span>
                              <strong>gg</strong>
                              <span>与你一起完成</span>
                            </div>
                          )}
                          {message.role === 'user' ? (
                            <div className="user-bubble">{message.content}</div>
                          ) : (
                            <>
                              <Markdown content={message.content || message.error || ''} />
                              <CopyButton content={message.content || ''} />
                            </>
                          )}
                        </article>
                      ),
                    )}
                    {toolLogs.map((log) => (
                      <details className={`tool-log ${log.status === 'error' ? 'has-error' : ''}`} key={log.id}>
                        <summary>
                          {log.status === 'running' ? (
                            <Icon name="loader" size={15} className="spin" />
                          ) : (
                            <Icon name={log.status === 'done' ? 'check' : 'info'} size={15} />
                          )}
                          <strong>{log.name}</strong>
                          <span>
                            {log.status === 'running' ? '执行中' : log.status === 'done' ? '已完成' : '执行失败'}
                          </span>
                          <Icon name="chevron" size={14} />
                        </summary>
                        <pre>{log.text || '暂无详细输出'}</pre>
                      </details>
                    ))}
                    {busy && (
                      <article className="message assistant streaming">
                        <div className="message-heading">
                          <span className="assistant-avatar">g</span>
                          <strong>gg</strong>
                          {!streamText && (
                            <span className="thinking-label">
                              {approval
                                ? '等待你的确认'
                                : toolLogs.some((log) => log.status === 'running')
                                  ? '正在执行任务'
                                  : '正在思考'}
                              <span className="thinking-dots">
                                <i />
                                <i />
                                <i />
                              </span>
                            </span>
                          )}
                        </div>
                        {streamText && <Markdown content={streamText} className="streaming-markdown" />}
                      </article>
                    )}
                  </div>
                )}
              </section>
              {showScrollButton && (
                <button
                  className="scroll-bottom-button"
                  onClick={() => {
                    stickToBottomRef.current = true;
                    messageScrollRef.current?.scrollTo({
                      top: messageScrollRef.current.scrollHeight,
                      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth',
                    });
                  }}
                >
                  <Icon name="down" size={15} />
                  回到最新消息
                </button>
              )}
              <footer className="composer-wrap">
                {approval && (
                  <div className="approval-card" role="region" aria-label="工具执行确认">
                    <div className="approval-heading">
                      <Icon name="info" size={18} />
                      <strong>这一步需要你的确认</strong>
                      <span>{approval.request.toolName}</span>
                    </div>
                    <p>{approval.request.summary || 'gg 请求执行此操作。'}</p>
                    {approval.request.details && (
                      <details>
                        <summary>
                          查看操作详情
                          <Icon name="chevron" size={14} />
                        </summary>
                        <pre>{approval.request.details}</pre>
                      </details>
                    )}
                    <div className="approval-actions">
                      <span>确认后，gg 将继续当前任务。</span>
                      <button onClick={() => void decideApproval(false)} disabled={approving}>
                        拒绝
                      </button>
                      <button className="primary" onClick={() => void decideApproval(true)} disabled={approving}>
                        {approving ? '提交中…' : '允许执行'}
                      </button>
                    </div>
                  </div>
                )}
                <form className={`composer ${busy ? 'is-running' : ''}`} onSubmit={submit}>
                  <label className="sr-only" htmlFor="prompt">
                    {busy ? '补充当前任务的要求' : '输入你的问题或任务'}
                  </label>
                  <textarea
                    id="prompt"
                    ref={composerRef}
                    value={prompt}
                    onChange={(event) => setPrompt(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                        event.preventDefault();
                        if (busy) void steerRun(false);
                        else event.currentTarget.form?.requestSubmit();
                      }
                    }}
                    placeholder={busy ? '有新的想法？在这里补充要求…' : '告诉 gg，你想完成什么…'}
                    rows={2}
                    disabled={actionPending}
                  />
                  <div className="composer-actions">
                    <span className="composer-context">
                      <Icon name={busy ? 'loader' : 'folder'} size={15} className={busy ? 'spin' : ''} />
                      {busy ? (approval ? '等待确认' : '正在处理你的任务') : workspaceName}
                    </span>
                    {busy ? (
                      <div className="run-actions">
                        <button
                          type="button"
                          onClick={() => void steerRun(true)}
                          disabled={!prompt.trim() || !runID || steering || Boolean(approval)}
                          title="当前任务完成后再执行"
                        >
                          下一轮
                        </button>
                        <button
                          className="primary"
                          type="button"
                          onClick={() => void steerRun(false)}
                          disabled={!prompt.trim() || !runID || steering || Boolean(approval)}
                        >
                          {steering ? '提交中…' : '补充要求'}
                        </button>
                        <button
                          className="stop-button"
                          type="button"
                          onClick={() => void cancelRun()}
                          disabled={!runID}
                          title="停止当前任务"
                        >
                          <Icon name="stop" size={14} />
                          <span>停止</span>
                        </button>
                      </div>
                    ) : (
                      <button
                        className="send-button primary"
                        type="submit"
                        disabled={!prompt.trim() || actionPending}
                        aria-label="发送消息"
                        title="发送 (Enter)"
                      >
                        <Icon name="arrow" size={19} />
                      </button>
                    )}
                  </div>
                </form>
                <div className="composer-hint">
                  <span>
                    <kbd>Enter</kbd> {busy ? '补充' : '发送'}
                    <span className="hint-divider">·</span>
                    <kbd>Shift + Enter</kbd> 换行
                  </span>
                  <span>草稿自动保留</span>
                </div>
              </footer>
            </div>
            {showTree && current && (
              <aside className="tree-panel" id="conversation-tree" aria-label="会话分支">
                <div className="tree-title">
                  <div>
                    <strong>
                      <Icon name="branch" size={16} />
                      会话分支
                    </strong>
                    <span>从任意节点继续，探索另一种可能。</span>
                  </div>
                  <button className="icon-button" onClick={() => setShowTree(false)} aria-label="关闭会话分支">
                    <Icon name="close" size={17} />
                  </button>
                </div>
                <div className="tree-list">
                  {(current.treeItems ?? []).map((node) => (
                    <TreeNode
                      key={node.id}
                      node={node}
                      active={activePath.has(node.id)}
                      disabled={busy || actionPending}
                      onCheckout={() => void applySessionAction('tree', node.id)}
                      onFork={() => void applySessionAction('fork', node.id)}
                    />
                  ))}
                  {!current.treeItems?.length && (
                    <div className="empty-note">
                      <Icon name="branch" size={26} />
                      <p>
                        发送第一条消息后，
                        <br />
                        会话分支会显示在这里。
                      </p>
                    </div>
                  )}
                </div>
              </aside>
            )}
          </div>
        )}
      </main>
      {error && (
        <div className="app-error error-banner" role="alert">
          <Icon name="info" size={18} />
          <span>{error}</span>
          <button className="icon-button" aria-label="关闭错误提示" onClick={() => setError('')}>
            <Icon name="close" size={16} />
          </button>
        </div>
      )}
      {notification && (
        <div className="toast" role="status">
          <Icon name="check" size={16} />
          {notification}
        </div>
      )}
      <div className="sr-only" role="status">
        {actionPending ? '正在加载' : busy ? (approval ? '等待操作确认' : 'gg 正在处理任务') : '已就绪'}
      </div>
    </div>
  );
}

function Starter({
  icon,
  title,
  description,
  onClick,
}: {
  icon: IconName;
  title: string;
  description: string;
  onClick: () => void;
}) {
  return (
    <button className="starter-card" onClick={onClick}>
      <span className="starter-icon">
        <Icon name={icon} size={21} />
      </span>
      <strong>
        {title}
        <Icon name="chevron" size={15} />
      </strong>
      <span>{description}</span>
    </button>
  );
}

function CopyButton({ content }: { content: string }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (!copied && !failed) return;
    const timer = window.setTimeout(() => {
      setCopied(false);
      setFailed(false);
    }, 2200);
    return () => window.clearTimeout(timer);
  }, [copied, failed]);
  return (
    <button
      className="message-copy"
      title={failed ? '复制失败，请手动选择文本' : '复制回复'}
      aria-label={failed ? '复制失败，请手动选择文本' : copied ? '已复制回复' : '复制回复'}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(content);
          setCopied(true);
        } catch {
          setFailed(true);
        }
      }}
    >
      <Icon name={copied ? 'check' : 'copy'} size={14} />
      <span>{failed ? '请手动复制' : copied ? '已复制' : '复制'}</span>
    </button>
  );
}

function TreeNode({
  node,
  active,
  disabled,
  onCheckout,
  onFork,
}: {
  node: TreeItem;
  active: boolean;
  disabled: boolean;
  onCheckout: () => void;
  onFork: () => void;
}) {
  return (
    <div
      className={`tree-node ${active ? 'active' : ''}`}
      style={{ marginLeft: `${Math.min(node.depth || 0, 6) * 12}px` }}
    >
      <button className="tree-main" onClick={onCheckout} disabled={disabled} title={`切换到此节点：${node.text}`}>
        <span className="tree-node-dot" />
        <div>
          <strong>
            {roleLabel(node.role)}
            {active && <span>当前路径</span>}
          </strong>
          <p>{node.text || '空消息'}</p>
        </div>
      </button>
      {node.role === 'user' && (
        <button
          className="fork-button"
          onClick={onFork}
          disabled={disabled}
          title="从这里创建新分支"
          aria-label="从这条消息创建新分支"
        >
          <Icon name="branch" size={13} />
        </button>
      )}
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
  return new Intl.DateTimeFormat(
    'zh-CN',
    date.toDateString() === new Date().toDateString()
      ? { hour: '2-digit', minute: '2-digit' }
      : { month: '2-digit', day: '2-digit' },
  ).format(date);
}

function groupSessions(sessions: SessionSummary[]) {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const yesterday = new Date(today);
  yesterday.setDate(yesterday.getDate() - 1);
  const groups = ['今天', '昨天', '更早'].map((label) => ({ label, items: [] as SessionSummary[] }));
  for (const session of [...sessions].sort((a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0))) {
    const timestamp = Date.parse(session.updatedAt);
    groups[timestamp >= today.getTime() ? 0 : timestamp >= yesterday.getTime() ? 1 : 2].items.push(session);
  }
  return groups.filter((group) => group.items.length);
}

function errorMessage(cause: unknown) {
  const message = cause instanceof Error ? cause.message : String(cause);
  if (message === 'Failed to fetch' || message === 'Load failed') return '无法连接服务，请检查服务地址和网络后重试。';
  if (message.includes('HTTP 401')) return '访问令牌无效，请检查后重新连接。';
  return message;
}

function isAbortError(cause: unknown) {
  return cause instanceof Error && cause.name === 'AbortError';
}
