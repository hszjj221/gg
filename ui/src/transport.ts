import type { RunEvent, RunStatus, SessionSummary, SessionUpdate, Snapshot, SystemInfo, WaitResult } from './types';

export const protocolVersion = '1.1';

export class RPCError extends Error {
  constructor(
    message: string,
    readonly code = 'internal_error',
    readonly retryable = false,
    readonly numericCode?: number,
  ) {
    super(message);
    this.name = 'RPCError';
  }
}

export interface Transport {
  call<T>(method: string, params?: Record<string, unknown>): Promise<T>;
  workspaceLabel(): Promise<string>;
  watchRun(runId: string, afterSequence: number, onEvent: (event: RunEvent) => void, signal: AbortSignal): Promise<number>;
}

export class ElectronTransport implements Transport {
  async call<T>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    if (!window.ggDesktop) throw new Error('Electron bridge is unavailable');
    const payload = await window.ggDesktop.invoke<T>(method, params);
    if (payload.error) {
      throw new RPCError(
        payload.error.message || 'Request failed',
        payload.error.data?.code,
        payload.error.data?.retryable,
        payload.error.code,
      );
    }
    return payload.result as T;
  }

  workspaceLabel(): Promise<string> {
    if (!window.ggDesktop) return Promise.resolve('Desktop');
    return window.ggDesktop.workspace();
  }

  async watchRun(runId: string, afterSequence: number, onEvent: (event: RunEvent) => void, signal: AbortSignal): Promise<number> {
    let after = afterSequence;
    while (!signal.aborted) {
      const batch = await this.call<WaitResult>('run.wait', { runId, afterSequence: after });
      if (signal.aborted) throw abortError();
      for (const event of batch.events) {
        after = Math.max(after, event.sequence);
        onEvent(event);
      }
      if (batch.done) return after;
    }
    throw abortError();
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
    const payload = (await response.json()) as {
      result?: T;
      error?: { code?: number; message?: string; data?: { code?: string; retryable?: boolean } };
    };
    if (payload.error) {
      throw new RPCError(
        payload.error.message || 'Request failed',
        payload.error.data?.code,
        payload.error.data?.retryable,
        payload.error.code,
      );
    }
    return payload.result as T;
  }

  async workspaceLabel(): Promise<string> {
    return this.endpoint || window.location.origin;
  }

  async watchRun(runId: string, afterSequence: number, onEvent: (event: RunEvent) => void, signal: AbortSignal): Promise<number> {
    let after = afterSequence;
    let retryDelay = 250;
    while (!signal.aborted) {
      const cursorBeforeConnect = after;
      try {
        const terminal = await this.consumeRunStream(runId, after, signal, (event) => {
          after = Math.max(after, event.sequence);
          onEvent(event);
        });
        if (terminal) return after;
        const status = await this.call<RunStatus>('run.get', { runId });
        if (status.done) return after;
      } catch (cause) {
        if (signal.aborted || isAbortError(cause)) throw abortError();
        if (cause instanceof RPCError) throw cause;
      }
      if (after > cursorBeforeConnect) retryDelay = 250;
      await abortableDelay(retryDelay, signal);
      retryDelay = Math.min(retryDelay * 2, 5000);
    }
    throw abortError();
  }

  private async consumeRunStream(
    runId: string,
    afterSequence: number,
    signal: AbortSignal,
    onEvent: (event: RunEvent) => void,
  ): Promise<boolean> {
    const base = this.endpoint.replace(/\/$/, '');
    const query = new URLSearchParams({ runId, after: String(afterSequence) });
    const response = await fetch(`${base}/events?${query}`, {
      headers: {
        Accept: 'text/event-stream',
        Authorization: `Bearer ${this.token}`,
        'Cache-Control': 'no-cache',
      },
      signal,
    });
    if (!response.ok) {
      if (response.status >= 500) throw new Error(`Server returned HTTP ${response.status}`);
      throw new RPCError(`Server returned HTTP ${response.status}`, 'http_error', false, response.status);
    }
    if (!response.body) throw new Error('Server returned an empty event stream');

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    try {
      while (true) {
        const { value, done } = await reader.read();
        buffer += decoder.decode(value, { stream: !done });
        let boundary = findSSEBoundary(buffer);
        while (boundary) {
          const frame = buffer.slice(0, boundary.index);
          buffer = buffer.slice(boundary.index + boundary.length);
          const event = parseSSEFrame(frame);
          if (event) {
            onEvent(event);
            if (isTerminalEvent(event)) {
              await reader.cancel();
              return true;
            }
          }
          boundary = findSSEBoundary(buffer);
        }
        if (done) return false;
      }
    } finally {
      reader.releaseLock();
    }
  }
}

export class API {
  private capabilities = new Set<string>();

  constructor(readonly transport: Transport) {}

  setCapabilities(capabilities: string[]) {
    this.capabilities = new Set(capabilities);
  }

  supports(capability: string) {
    return this.capabilities.has(capability);
  }

  systemInfo() {
    return this.transport.call<SystemInfo>('system.info');
  }

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

  runStatus(runId: string) {
    return this.transport.call<RunStatus>('run.get', { runId });
  }

  activeRun(sessionId: string) {
    return this.transport.call<RunStatus | null>('run.active', { sessionId });
  }

  watchRun(runId: string, afterSequence: number, onEvent: (event: RunEvent) => void, signal: AbortSignal) {
    return this.transport.watchRun(runId, afterSequence, onEvent, signal);
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

function findSSEBoundary(buffer: string): { index: number; length: number } | null {
  const match = /\r?\n\r?\n/.exec(buffer);
  return match ? { index: match.index, length: match[0].length } : null;
}

function parseSSEFrame(frame: string): RunEvent | null {
  let eventType = 'message';
  const data: string[] = [];
  for (const line of frame.split(/\r?\n/)) {
    if (line.startsWith(':')) continue;
    const separator = line.indexOf(':');
    const field = separator < 0 ? line : line.slice(0, separator);
    const value = separator < 0 ? '' : line.slice(separator + 1).replace(/^ /, '');
    if (field === 'event') eventType = value;
    if (field === 'data') data.push(value);
  }
  if (data.length === 0) return null;
  let payload: unknown;
  try {
    payload = JSON.parse(data.join('\n'));
  } catch {
    throw new RPCError('Server returned an invalid event', 'protocol_error');
  }
  if (eventType === 'error') {
    const error = payload as { code?: number; message?: string; data?: { code?: string; retryable?: boolean } };
    throw new RPCError(error.message || 'Event stream failed', error.data?.code, error.data?.retryable, error.code);
  }
  return eventType === 'event' ? (payload as RunEvent) : null;
}

function isTerminalEvent(event: RunEvent) {
  return event.type === 'run_completed' || event.type === 'run_failed' || event.type === 'run_canceled';
}

function isAbortError(cause: unknown) {
  return cause instanceof Error && cause.name === 'AbortError';
}

function abortError() {
  const error = new Error('Operation aborted');
  error.name = 'AbortError';
  return error;
}

function abortableDelay(milliseconds: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) {
      reject(abortError());
      return;
    }
    const onAbort = () => {
      window.clearTimeout(timer);
      reject(abortError());
    };
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, milliseconds);
    signal.addEventListener('abort', onAbort, { once: true });
  });
}
