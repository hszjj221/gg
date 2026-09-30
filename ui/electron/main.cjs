const { app, BrowserWindow, dialog, ipcMain } = require('electron');
const { spawn } = require('node:child_process');
const { randomBytes } = require('node:crypto');
const net = require('node:net');
const path = require('node:path');

const READY_TIMEOUT_MS = 15000;
const READY_POLL_MS = 250;

function daemonPath() {
  if (process.env.GGD_PATH) return process.env.GGD_PATH;
  const name = process.platform === 'win32' ? 'ggd.exe' : 'ggd';
  if (app.isPackaged) return path.join(process.resourcesPath, 'bin', name);
  return path.join(__dirname, '..', 'bin', name);
}

async function chooseWorkspace() {
  if (process.env.GG_WORKSPACE) return path.resolve(process.env.GG_WORKSPACE);
  const result = await dialog.showOpenDialog({
    title: '选择 gg 工作目录',
    message: 'gg 的文件和命令工具只在这个目录中运行。',
    properties: ['openDirectory', 'createDirectory'],
  });
  return result.canceled ? null : result.filePaths[0];
}

function findFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      server.close(() => resolve(address.port));
    });
  });
}

async function waitForDaemon(endpoint, token) {
  const deadline = Date.now() + READY_TIMEOUT_MS;
  const body = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'system.info', params: {} });
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`${endpoint}/rpc`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body,
      });
      if (response.ok) {
        const payload = await response.json();
        if (payload && !payload.error) return;
      }
    } catch {
      // ggd is not up yet; keep polling.
    }
    await new Promise((resolve) => setTimeout(resolve, READY_POLL_MS));
  }
  throw new Error(`ggd 未在 ${READY_TIMEOUT_MS / 1000} 秒内就绪`);
}

function createWindow() {
  const window = new BrowserWindow({
    width: 1240,
    height: 820,
    minWidth: 880,
    minHeight: 600,
    titleBarStyle: process.platform === 'darwin' ? 'hiddenInset' : 'default',
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  window.webContents.on('will-navigate', (event) => event.preventDefault());
  window.loadFile(path.join(__dirname, '..', 'dist', 'index.html'));
}

let daemonProcess = null;
let connection = null;

function stopDaemon() {
  if (!daemonProcess) return;
  daemonProcess.kill();
  daemonProcess = null;
}

app.whenReady().then(async () => {
  const workspace = await chooseWorkspace();
  if (!workspace) {
    app.quit();
    return;
  }
  try {
    const port = await findFreePort();
    const token = randomBytes(32).toString('hex');
    const endpoint = `http://127.0.0.1:${port}`;
    daemonProcess = spawn(daemonPath(), ['--http', `127.0.0.1:${port}`, '--token', token, '--exit-on-stdin-eof', '--cwd', workspace], {
      // stdin is a control pipe, never written to: if the Electron main
      // process dies without cleanup (crash/SIGKILL skips before-quit), the
      // pipe closes, stdin reaches EOF, and ggd shuts itself down instead of
      // lingering as an orphan with a lost token and port.
      stdio: ['pipe', 'ignore', 'pipe'],
      windowsHide: true,
    });
    daemonProcess.stderr.setEncoding('utf8');
    daemonProcess.stderr.on('data', (text) => console.error(`[ggd] ${String(text).trimEnd()}`));
    daemonProcess.on('error', (error) => console.error('[ggd] failed to start', error));
    daemonProcess.on('exit', (code, signal) => {
      console.error(`[ggd] exited (${signal || code || 'unknown'})`);
      daemonProcess = null;
    });
    await waitForDaemon(endpoint, token);
    connection = { endpoint, token, workspace };
  } catch (error) {
    stopDaemon();
    dialog.showErrorBox('gg 启动失败', `无法启动本地 gg 服务：${error instanceof Error ? error.message : String(error)}`);
    app.quit();
    return;
  }
  ipcMain.handle('gg:connection', () => connection);
  createWindow();
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('before-quit', stopDaemon);
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
