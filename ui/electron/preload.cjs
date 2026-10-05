const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('ggDesktop', {
  getConnection: () => ipcRenderer.invoke('gg:connection'),
  openExternal: (url) => ipcRenderer.invoke('gg:open-external', url),
});
