let unauthorizedHandler = null;

export function setUnauthorizedHandler(handler) { unauthorizedHandler = handler; }

export function createAPI(defaultSignal) {
const API = {
  async raw(path, opts) {
    const request = Object.assign({}, opts);
    if (defaultSignal && !request.signal) request.signal = defaultSignal;
    const headers = Object.assign({}, request.headers);
    const isForm = typeof FormData !== 'undefined' && request.body instanceof FormData;
    if (!isForm && !Object.keys(headers).some(name => name.toLowerCase() === 'content-type')) {
      headers['Content-Type'] = 'application/json';
    }
    request.headers = headers;
    const r = await fetch(path, request);
    if (r.status === 401 && path !== '/api/admin/login' && path !== '/api/admin/password') {
      if (unauthorizedHandler) unauthorizedHandler();
      throw new Error('未登录');
    }
    const ct = r.headers.get('content-type') || '';
    const body = ct.includes('json') ? await r.json() : await r.text();
    if (!r.ok) throw new Error((body && body.error && body.error.message) || body || ('HTTP ' + r.status));
    return body;
  },
  checkAuth() { return this.raw('/api/admin/check-auth'); },
  login(password) { return this.raw('/api/admin/login', { method: 'POST', body: JSON.stringify({ password }) }); },
  logout() { return this.raw('/api/admin/logout', { method: 'POST' }); },
  changePassword(oldPw, newPw) { return this.raw('/api/admin/password', { method: 'POST', body: JSON.stringify({ old_password: oldPw, new_password: newPw }) }); },
  settings: {
    get() { return API.raw('/api/admin/settings'); },
    put(v) { return API.raw('/api/admin/settings', { method: 'PUT', body: JSON.stringify({ settings: v }) }); },
  },
  keys: {
    list() { return API.raw('/api/admin/keys'); },
    add(n, k, desc) { return API.raw('/api/admin/keys', { method: 'POST', body: JSON.stringify({ name: n, key: k, description: desc }) }); },
    del(n) { return API.raw('/api/admin/keys/' + encodeURIComponent(n), { method: 'DELETE' }); },
  },
  models: {
    get() { return API.raw('/api/admin/models'); },
    put(models, alias_map) { return API.raw('/api/admin/models', { method: 'PUT', body: JSON.stringify({ models, alias_map }) }); },
  },
  nodes: {
    list() { return API.raw('/api/admin/nodes'); },
    delete(uri) { return API.raw('/api/admin/nodes', { method: 'DELETE', body: JSON.stringify({ raw_uri: uri }) }); },
    test(uri, opts) { return API.raw('/api/admin/nodes/test', { method: 'POST', body: JSON.stringify(Object.assign({ raw_uri: uri, auto_disable: true, timeout_seconds: 25 }, opts || {})) }); },
    enable(uri) { return API.raw('/api/admin/nodes/enable', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
    testAll() { return API.raw('/api/admin/nodes/test-all', { method: 'POST' }); },
    testProgress() { return API.raw('/api/admin/nodes/test-progress', { method: 'GET' }); },
    testPause() { return API.raw('/api/admin/nodes/test-pause', { method: 'POST' }); },
    testResume() { return API.raw('/api/admin/nodes/test-resume', { method: 'POST' }); },
    testTerminate() { return API.raw('/api/admin/nodes/test-terminate', { method: 'POST' }); },
    dedup() { return API.raw('/api/admin/nodes/deduplicate', { method: 'POST' }); },
    dedupPreview() { return API.raw('/api/admin/nodes/deduplicate/preview'); },
    deleteDisabled() { return API.raw('/api/admin/nodes/disabled', { method: 'DELETE' }); },
    import(text, replace) { return API.raw('/api/admin/nodes/import', { method: 'POST', body: JSON.stringify({ text, replace }) }); },
    importSingle(uri) { return API.raw('/api/admin/nodes/import', { method: 'POST', body: JSON.stringify({ text: uri, replace: false, single_uri: true }) }); },
    importJson(text, replace) { return API.raw('/api/admin/nodes/import-json', { method: 'POST', body: JSON.stringify({ text, replace }) }); },
    batchEnable(uris) { return API.raw('/api/admin/nodes/batch-enable', { method: 'POST', body: JSON.stringify({ uris }) }); },
    batchDisable(uris) { return API.raw('/api/admin/nodes/batch-disable', { method: 'POST', body: JSON.stringify({ uris }) }); },
    batchDelete(uris) { return API.raw('/api/admin/nodes/batch-delete', { method: 'POST', body: JSON.stringify({ uris }) }); },
    sort(desc) { return API.raw('/api/admin/nodes/sort', { method: 'POST', body: JSON.stringify({ desc: !!desc }) }); },
  },
  subscriptions: {
    fetch(url) { return API.raw('/api/admin/subscriptions/fetch', { method: 'POST', body: JSON.stringify({ url }) }); },
    list() { return API.raw('/api/admin/subscriptions/list'); },
    saveCustomUA(payload) { return API.raw('/api/admin/subscriptions/custom_ua/save', { method: 'POST', body: JSON.stringify(payload) }); },
    deleteCustomUA(id) { return API.raw('/api/admin/subscriptions/custom_ua/delete', { method: 'POST', body: JSON.stringify({ id }) }); },
    save(payload) { return API.raw('/api/admin/subscriptions/save', { method: 'POST', body: JSON.stringify(payload) }); },
    delete(id, deleteNodes) { return API.raw('/api/admin/subscriptions/delete', { method: 'POST', body: JSON.stringify({ id, delete_nodes: deleteNodes }) }); },
    update(id) { return API.raw('/api/admin/subscriptions/update', { method: 'POST', body: JSON.stringify({ id }) }); },
  },
  proxyNodes: {
    list(page, pageSize) { return API.raw('/api/admin/global-proxies?page=' + encodeURIComponent(page || 1) + '&page_size=' + encodeURIComponent(pageSize || 10)); },
    import(uri) { return API.raw('/api/admin/global-proxies/import', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
    importBatch(uris) { return API.raw('/api/admin/global-proxies/import-batch', { method: 'POST', body: JSON.stringify({ uris }) }); },
    promote(uri, pinned) { return API.raw('/api/admin/global-proxies/promote-request-node', { method: 'POST', body: JSON.stringify({ raw_uri: uri, pinned: !!pinned }) }); },
    pin(uri, pinned) { return API.raw('/api/admin/global-proxies/pin', { method: 'POST', body: JSON.stringify({ raw_uri: uri, pinned: !!pinned }) }); },
    enable(uri) { return API.raw('/api/admin/global-proxies/enable', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
    disable(uri) { return API.raw('/api/admin/global-proxies/disable', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
    enableBatch(uris) { return API.raw('/api/admin/global-proxies/enable-batch', { method: 'POST', body: JSON.stringify({ uris }) }); },
    disableBatch(uris) { return API.raw('/api/admin/global-proxies/disable-batch', { method: 'POST', body: JSON.stringify({ uris }) }); },
    deleteBatch(uris) { return API.raw('/api/admin/global-proxies/delete-batch', { method: 'POST', body: JSON.stringify({ uris }) }); },
    deleteDisabled() { return API.raw('/api/admin/global-proxies/disabled', { method: 'DELETE' }); },
    delete(uri) { return API.raw('/api/admin/global-proxies', { method: 'DELETE', body: JSON.stringify({ raw_uri: uri }) }); },
    test(uri) { return API.raw('/api/admin/global-proxies/test', { method: 'POST', body: JSON.stringify({ raw_uri: uri, timeout_seconds: 25 }) }); },
    testBatch(uris) { return API.raw('/api/admin/global-proxies/test-batch', { method: 'POST', body: JSON.stringify({ uris, timeout_seconds: 25 }) }); },
    testProgress() { return API.raw('/api/admin/global-proxies/test-progress'); },
  },
  logs: {
    get() { return API.raw('/api/admin/log'); },
  },
  appearance: {
    upload(fileData) { return API.raw('/api/admin/upload-bg', { method: 'POST', body: fileData }); },
    listBackgrounds() { return API.raw('/api/admin/list-bgs'); },
    deleteBackground(filename) { return API.raw('/api/admin/delete-bg', { method: 'POST', body: JSON.stringify({ filename }) }); },
  },
  build: {
    get() { return API.raw('/api/meta/build'); },
  },
  useNode(uri) { return this.raw('/api/admin/use-node', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
};
return API;
}

export const API = createAPI();
