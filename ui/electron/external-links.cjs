function createExternalLinkHandler({ isClient, openExternal }) {
  return async (event, value) => {
    if (!isClient(event.sender) || !event.senderFrame || event.senderFrame !== event.sender.mainFrame) {
      throw new Error('External links must be opened by the client main frame');
    }
    if (typeof value !== 'string') throw new Error('Expected a web URL');
    const url = new URL(value);
    if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) {
      throw new Error('Only HTTP and HTTPS URLs without credentials are supported');
    }
    await openExternal(url.href);
  };
}

module.exports = { createExternalLinkHandler };
