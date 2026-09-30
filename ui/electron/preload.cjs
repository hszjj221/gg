const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('ggDesktop', {
  getConnection: () => ipcRenderer.invoke('gg:connection'),
});
