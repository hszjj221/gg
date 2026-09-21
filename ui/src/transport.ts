import type { SessionSummary, SessionUpdate, Snapshot, WaitResult } from './types';

export interface Transport {
  call<T>(method: string, params?: Record<string, unknown>): Promise<T>;
  workspaceLabel(): Promise<string>;
}

export class ElectronTransport implements Transport {
  async call<T>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    if (!window.ggDesktop) throw new Error('Electron bridge is unavailable');
    return window.ggDesktop.invoke<T>(method, params);
  }

  workspaceLabel(): Promise<string> {
    if (!window.ggDesktop) return Promise.resolve('Desktop');
    return window.ggDesktop.workspace();
  }
}

export class WebTransport implements Transport {
  constructor(
    private readonly endpoint: string,
    private readonly token: string,
  ) {}

  async call<T>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    const response = await fetch(`${this.endpoint.replace(/\/$/, '')}/rpc`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${this.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ jsonrpc: '2.0', id: crypto.randomUUID(), method, params }),
    });
    if (!response.ok) throw new Error(`Server returned HTTP ${response.status}`);
    const payload = (await response.json()) as { result?: T; error?: { message?: string } };
    if (payload.error) throw new Error(payload.error.message || 'Request failed');
    return payload.result as T;
  }

  async workspaceLabel(): Promise<string> {
    return this.endpoint || window.location.origin;
  }
}

export class API {
  constructor(readonly transport: Transport) {}

  listSessions() {
    return this.transport.call<SessionSummary[]>('session.list');
  }

  createSession(name = '') {
    return this.transport.call<Snapshot>('session.create', { name });
  }

  openSession(sessionId: string) {
    return this.transport.call<Snapshot>('session.open', { sessionId });
  }

  getSession(sessionId: string) {
    return this.transport.call<Snapshot>('session.get', { sessionId });
  }

  renameSession(sessionId: string, name: string) {
    return this.transport.call<Snapshot>('session.rename', { sessionId, name });
  }

  sessionAction(sessionId: string, action: 'tree' | 'fork' | 'clone', nodeId = '') {
    return this.transport.call<SessionUpdate>('session.action', { sessionId, action, nodeId });
  }

  startRun(sessionId: string, prompt: string, requireApproval = true) {
    return this.transport.call<{ runId: string }>('run.start', { sessionId, prompt, requireApproval });
  }

  waitRun(runId: string, afterSequence: number) {
    return this.transport.call<WaitResult>('run.wait', { runId, afterSequence });
  }

  cancelRun(runId: string) {
    return this.transport.call<{ ok: boolean }>('run.cancel', { runId });
  }

  approve(runId: string, approvalId: string, allow: boolean) {
    return this.transport.call<{ ok: boolean }>('run.approve', { runId, approvalId, allow });
  }

  steer(sessionId: string, text: string, followUp = false) {
    return this.transport.call<{ ok: boolean }>('run.steer', { sessionId, text, followUp });
  }
}
