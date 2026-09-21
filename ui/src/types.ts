export type Role = 'system' | 'user' | 'assistant' | 'tool';

export interface Message {
  role: Role;
  content?: string;
  toolCallId?: string;
  toolName?: string;
  error?: string;
}

export interface TreeItem {
  id: string;
  parentId?: string;
  depth?: number;
  role: Role;
  text: string;
  active: boolean;
}

export interface Snapshot {
  sessionId: string;
  sessionName: string;
  modelName: string;
  messages: Message[];
  treeItems: TreeItem[];
}

export interface SessionSummary {
  id: string;
  name: string;
  updatedAt: string;
  messageCount: number;
  preview: string;
}

export interface SessionUpdate extends Snapshot {
  draft?: string;
  notice?: string;
}

export interface AgentEvent {
  type: 'text_delta' | 'tool_call_start' | 'tool_call_finish' | 'user_message';
  text?: string;
  toolCallId?: string;
  toolName?: string;
  summary?: string;
  details?: string;
  isError?: boolean;
}

export interface Approval {
  id: string;
  request: {
    toolName: string;
    summary: string;
    details?: string;
  };
}

export interface RunEvent {
  id: string;
  sessionId: string;
  runId: string;
  sequence: number;
  timestamp: number;
  type:
    | 'run_started'
    | 'agent_event'
    | 'approval_requested'
    | 'approval_resolved'
    | 'run_completed'
    | 'run_failed'
    | 'run_canceled';
  agent?: AgentEvent;
  approval?: Approval;
  result?: { content: string; modelName: string };
  error?: string;
}

export interface WaitResult {
  events: RunEvent[];
  done: boolean;
}

export interface DesktopBridge {
  invoke<T>(method: string, params?: Record<string, unknown>): Promise<T>;
  workspace(): Promise<string>;
}

declare global {
  interface Window {
    ggDesktop?: DesktopBridge;
  }
}
