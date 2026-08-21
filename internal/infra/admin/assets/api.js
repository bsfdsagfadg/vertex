const API = {
  async raw(path, opts) {
    const r = await fetch(path, Object.assign({ headers: { 'Content-Type': 'application/json' } }, opts));
    if (r.status === 401 && path !== '/api/admin/login' && path !== '/api/admin/password') {
      showLogin(); throw new Error('未登录');
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
    deleteDisabled() { return API.raw('/api/admin/nodes/disabled', { method: 'DELETE' }); },
    import(text, replace) { return API.raw('/api/admin/nodes/import', { method: 'POST', body: JSON.stringify({ text, replace }) }); },
    batchEnable(uris) { return API.raw('/api/admin/nodes/batch-enable', { method: 'POST', body: JSON.stringify({ uris }) }); },
    batchDisable(uris) { return API.raw('/api/admin/nodes/batch-disable', { method: 'POST', body: JSON.stringify({ uris }) }); },
    batchDelete(uris) { return API.raw('/api/admin/nodes/batch-delete', { method: 'POST', body: JSON.stringify({ uris }) }); },
    sort(desc) { return API.raw('/api/admin/nodes/sort', { method: 'POST', body: JSON.stringify({ desc: !!desc }) }); },
  },
  subscriptions: {
    fetch(url) { return API.raw('/api/admin/subscriptions/fetch', { method: 'POST', body: JSON.stringify({ url }) }); },
  },
  proxyNodes: {
    list() { return API.raw('/api/admin/proxy-nodes', { method: 'GET' }); },
    import(uri) { return API.raw('/api/admin/proxy-nodes/import', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
    toggle(uris, disabled) { return API.raw('/api/admin/proxy-nodes/toggle', { method: 'POST', body: JSON.stringify({ uris, disabled: !!disabled }) }); },
    batchDelete(uris) { return API.raw('/api/admin/proxy-nodes/batch-delete', { method: 'POST', body: JSON.stringify({ uris }) }); },
    deleteDisabled() { return API.raw('/api/admin/proxy-nodes/delete-disabled', { method: 'POST' }); },
    dedup() { return API.raw('/api/admin/proxy-nodes/dedup', { method: 'POST' }); },
    test(uri, opts) { return API.raw('/api/admin/proxy-nodes/test', { method: 'POST', body: JSON.stringify(Object.assign({ raw_uri: uri, timeout_seconds: 25 }, opts || {})) }); },
  },
  useNode(uri) { return this.raw('/api/admin/use-node', { method: 'POST', body: JSON.stringify({ raw_uri: uri }) }); },
};