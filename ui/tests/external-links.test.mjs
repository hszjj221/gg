import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createExternalLinkHandler } from '../electron/external-links.cjs';

function fixture() {
  const sender = { mainFrame: {} };
  const event = { sender, senderFrame: sender.mainFrame };
  const opened = [];
  const handler = createExternalLinkHandler({
    isClient: (contents) => contents === sender,
    openExternal: async (url) => { opened.push(url); },
  });
  return { sender, event, opened, handler };
}

test('External browser IPC accepts HTTP and HTTPS URLs from the client main frame', async () => {
  const { event, handler, opened } = fixture();
  await handler(event, 'HTTPS://example.com/docs?q=hello%20world#section');
  await handler(event, 'http://localhost:3000/help');
  assert.deepEqual(opened, ['https://example.com/docs?q=hello%20world#section', 'http://localhost:3000/help']);
});

test('External browser IPC rejects other protocols, credentials, and malformed inputs', async () => {
  const { event, handler, opened } = fixture();
  for (const url of [
    'file:///etc/passwd', 'javascript:alert(1)', 'data:text/html,<h1>Unsafe</h1>',
    'mailto:team@example.com', 'custom-app://open', 'https://user:secret@example.com/',
    'https://user@example.com/', '//example.com', '/docs', 'https://', '', null, {}, 42,
  ]) {
    await assert.rejects(handler(event, url));
  }
  assert.deepEqual(opened, []);
});

test('External browser IPC rejects unrelated web contents and subframes', async () => {
  const { sender, event, handler, opened } = fixture();
  const untrusted = { mainFrame: {} };
  await assert.rejects(handler({ sender: untrusted, senderFrame: untrusted.mainFrame }, 'https://example.com/'));
  await assert.rejects(handler({ sender, senderFrame: {} }, 'https://example.com/'));
  await assert.rejects(handler({ ...event, senderFrame: null }, 'https://example.com/'));
  assert.deepEqual(opened, []);
});
