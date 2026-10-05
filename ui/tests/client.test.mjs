import assert from 'node:assert/strict';
import { after, afterEach, beforeEach, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { createServer } from 'vite';
import react from '@vitejs/plugin-react';
import { act, createElement } from 'react';

const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  url: 'http://gg.test',
  pretendToBeVisual: true,
});
for (const name of ['window', 'document', 'HTMLElement', 'sessionStorage', 'localStorage']) {
  globalThis[name] = dom.window[name];
}
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
globalThis.IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = (media) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} });

const { createRoot } = await import('react-dom/client');
const vite = await createServer({
  root: fileURLToPath(new URL('../', import.meta.url)),
  configFile: false,
  plugins: [react()],
  server: { middlewareMode: true, hmr: false, watch: null },
  appType: 'custom',
  logLevel: 'silent',
});
const { App } = await vite.ssrLoadModule('/src/App.tsx');
const { Markdown } = await vite.ssrLoadModule('/src/Markdown.tsx');
const originalFetch = globalThis.fetch;
let root;
let fixture;

function response(result) {
  return new Response(JSON.stringify({ jsonrpc: '2.0', id: 1, result }), {
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  sessionStorage.setItem('gg.token', 'unit-test-token');
  document.body.innerHTML = '<div id="root"></div>';
  root = createRoot(document.getElementById('root'));
  fixture = {
    calls: [],
    failStart: false,
    pendingSteer: null,
    sessions: ['一', '二'].map((suffix, index) => ({
      sessionId: `s${index + 1}`,
      sessionName: `会话${suffix}`,
      modelName: 'test:model',
      messages: [],
      treeItems: [],
    })),
  };
  globalThis.fetch = async (url, options = {}) => {
    if (String(url).startsWith('/events?')) {
      const stream = new ReadableStream({
        start(controller) {
          const abort = () => controller.error(new DOMException('Aborted', 'AbortError'));
          if (options.signal.aborted) abort();
          else options.signal.addEventListener('abort', abort, { once: true });
        },
      });
      return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } });
    }
    const { method, params } = JSON.parse(options.body);
    fixture.calls.push({ method, params });
    const session = fixture.sessions.find((item) => item.sessionId === params.sessionId);
    switch (method) {
      case 'system.info':
        return response({ protocolVersion: '1.3', capabilities: [] });
      case 'session.list':
        return response(
          fixture.sessions.map((item) => ({
            id: item.sessionId,
            name: item.sessionName,
            preview: '',
            messageCount: item.messages.length,
            updatedAt: '2026-10-05T00:00:00Z',
          })),
        );
      case 'session.open':
      case 'session.get':
        return response(session);
      case 'session.rename':
        session.sessionName = params.name;
        return response(session);
      case 'run.start':
        if (fixture.failStart)
          return new Response(
            JSON.stringify({
              jsonrpc: '2.0',
              id: 1,
              error: { code: -1, message: '服务暂时不可用' },
            }),
          );
        return response({ runId: 'run1' });
      case 'run.steer':
        if (fixture.pendingSteer) await fixture.pendingSteer;
        return response({ ok: true });
      default:
        throw new Error(`Unexpected RPC: ${method}`);
    }
  };
});

afterEach(async () => {
  await act(async () => root.unmount());
  globalThis.fetch = originalFetch;
});

after(async () => {
  await vite.close();
  dom.window.close();
});

async function mountApp() {
  await act(async () => root.render(createElement(App)));
  assert.equal(document.querySelector('.title-editor input').value, '会话一');
}

async function input(element, value) {
  const prototype =
    element.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, value);
    element.dispatchEvent(new window.Event('input', { bubbles: true }));
  });
}

async function click(element) {
  assert.ok(element, 'Expected an interactive element');
  await act(async () => element.dispatchEvent(new window.MouseEvent('click', { bubbles: true })));
}

function sessionButton(name) {
  return [...document.querySelectorAll('.session-item')].find(
    (item) => item.querySelector('strong').textContent === name,
  );
}

test('Markdown preserves headings, code, links and tables', async () => {
  const content =
    '## A useful reply\n\n**Clear next steps**\n\n```js\nconst html = "<script>";\n```\n\n[Docs](https://example.com/docs)\n\n| Task | Status |\n| --- | --- |\n| Review | Done |';
  await act(async () => root.render(createElement(Markdown, { content })));
  assert.equal(document.querySelector('h2').textContent, 'A useful reply');
  assert.equal(document.querySelector('strong').textContent, 'Clear next steps');
  assert.match(document.querySelector('pre code').textContent, /<script>/);
  assert.equal(document.querySelector('a').getAttribute('href'), 'https://example.com/docs');
  assert.equal(document.querySelectorAll('table tbody tr').length, 1);
});

test('Untrusted Markdown cannot retain executable markup or navigation payloads', async () => {
  const content =
    '**Keep this text**\n\n<script>alert(1)</script><img src="x" onerror="alert(1)"><a href="javascript:alert(1)">unsafe link</a><svg onload="alert(1)"></svg><form><input autofocus onfocus="alert(1)"></form><style>body{display:none}</style><p style="position:fixed" onclick="alert(1)">Readable text</p>';
  await act(async () => root.render(createElement(Markdown, { content })));
  assert.equal(document.querySelector('strong').textContent, 'Keep this text');
  assert.match(document.querySelector('.markdown-body').textContent, /Readable text/);
  assert.equal(
    document.querySelectorAll(
      'script, svg, form, input, style, [style], [onerror], [onclick], [onload], [onfocus], a[href^="javascript:"]',
    ).length,
    0,
  );
});

test('Switching sessions and remounting restore each session draft independently', async () => {
  await mountApp();
  await input(document.querySelector('#prompt'), 'Draft for session one');
  await click(sessionButton('会话二'));
  assert.equal(document.querySelector('#prompt').value, '');
  await input(document.querySelector('#prompt'), 'Draft for session two');
  await click(sessionButton('会话一'));
  assert.equal(document.querySelector('#prompt').value, 'Draft for session one');
  await act(async () => root.unmount());
  root = createRoot(document.getElementById('root'));
  await mountApp();
  assert.equal(document.querySelector('#prompt').value, 'Draft for session one');
  await click(sessionButton('会话二'));
  assert.equal(document.querySelector('#prompt').value, 'Draft for session two');
});

test('Escape cancels a title edit without sending a rename request', async () => {
  await mountApp();
  const title = document.querySelector('.title-editor input');
  await input(title, 'Do not save this title');
  await act(async () => {
    title.focus();
    title.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  });
  assert.equal(title.value, '会话一');
  assert.equal(fixture.calls.filter((call) => call.method === 'session.rename').length, 0);
});

test('A failed send restores the prompt and does not display an unsent message', async () => {
  await mountApp();
  fixture.failStart = true;
  await input(document.querySelector('#prompt'), 'Keep this request for retry');
  await click(document.querySelector('[aria-label="发送消息"]'));
  assert.equal(document.querySelector('#prompt').value, 'Keep this request for retry');
  assert.match(document.querySelector('[role="alert"]').textContent, /服务暂时不可用/);
  assert.equal(document.querySelectorAll('.user-bubble').length, 0);
});

test('Submitting supplemental instructions preserves text typed while the request is pending', async () => {
  await mountApp();
  await input(document.querySelector('#prompt'), 'Start a task');
  await click(document.querySelector('[aria-label="发送消息"]'));
  assert.equal(document.querySelector('.user-bubble').textContent, 'Start a task');
  assert.ok(document.querySelector('.composer.is-running'));
  let resolveSteer;
  fixture.pendingSteer = new Promise((resolve) => {
    resolveSteer = resolve;
  });
  await input(document.querySelector('#prompt'), 'First instruction');
  const supplement = [...document.querySelectorAll('.run-actions button')].find(
    (button) => button.textContent === '补充要求',
  );
  await click(supplement);
  await input(document.querySelector('#prompt'), 'A second instruction still being drafted');
  await act(async () => resolveSteer());
  assert.equal(document.querySelector('#prompt').value, 'A second instruction still being drafted');
  assert.equal(fixture.calls.find((call) => call.method === 'run.steer').params.text, 'First instruction');
});
