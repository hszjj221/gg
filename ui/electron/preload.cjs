const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('ggDesktop', {
  invoke: (method, params) => ipcRenderer.invoke('gg:rpc', method, params),
  workspace: () => ipcRenderer.invoke('gg:workspace'),
});
