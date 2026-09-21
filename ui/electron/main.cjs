const { app, BrowserWindow, dialog, ipcMain } = require('electron');
const { spawn } = require('node:child_process');
const { createInterface } = require('node:readline');
const path = require('node:path');

const allowedMethods = new Set([
  'session.list',
  'session.create',
  'session.open',
  'session.get',
  'session.rename',
  'session.action',
  'run.start',
  'run.wait',
  'run.cancel',
  'run.approve',
  'run.steer',
]);

class DaemonClient {
  constructor(workspace) {
    this.workspace = workspace;
    this.nextId = 1;
    this.pending = new Map();
    this.process = null;
  }

  start() {
    if (this.process) return;
    const executable = daemonPath();
    this.process = spawn(executable, ['--cwd', this.workspace], {
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    });
    const lines = createInterface({ input: this.process.stdout });
    lines.on('line', (line) => this.receive(line));
    this.process.stderr.setEncoding('utf8');
    this.process.stderr.on('data', (text) => console.error(`[ggd] ${String(text).trimEnd()}`));
    this.process.on('error', (error) => this.failAll(error));
    this.process.on('exit', (code, signal) => {
      this.failAll(new Error(`ggd exited (${signal || code || 'unknown'})`));
      this.process = null;
    });
  }

  call(method, params = {}) {
    if (!allowedMethods.has(method)) {
      return Promise.reject(new Error(`unsupported RPC method: ${method}`));
    }
    this.start();
    const id = this.nextId++;
    const request = JSON.stringify({ jsonrpc: '2.0', id, method, params });
    return new Promise((resolve, reject) => {
      this.pending.set(String(id), { resolve, reject });
      this.process.stdin.write(`${request}\n`, (error) => {
        if (!error) return;
        this.pending.delete(String(id));
        reject(error);
      });
    });
  }

  receive(line) {
    let response;
    try {
      response = JSON.parse(line);
    } catch (error) {
      console.error('Invalid response from ggd', error);
      return;
    }
    const pending = this.pending.get(String(response.id));
    if (!pending) return;
    this.pending.delete(String(response.id));
    if (response.error) pending.reject(new Error(response.error.message || 'ggd request failed'));
    else pending.resolve(response.result);
  }

  failAll(error) {
    for (const pending of this.pending.values()) pending.reject(error);
    this.pending.clear();
  }

  stop() {
    if (!this.process) return;
    this.process.kill();
    this.process = null;
  }
}

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

let daemon;

app.whenReady().then(async () => {
  const workspace = await chooseWorkspace();
  if (!workspace) {
    app.quit();
    return;
  }
  daemon = new DaemonClient(workspace);
  ipcMain.handle('gg:rpc', (_event, method, params) => daemon.call(method, params));
  ipcMain.handle('gg:workspace', () => workspace);
  createWindow();
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('before-quit', () => daemon?.stop());
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
