async function loadOverview() {
  const card = (label, value, cls, sub) => `<div class="card glass hoverable stat"><div class="label">${label}</div><div class="value ${cls||''}">${value}</div>${sub?`<div class="sub">${sub}</div>`:''}</div>`;
  const [keysD, modelsD, nodesD] = await Promise.all([
    API.keys.list().catch(() => ({keys:[]})),
    API.models.get().catch(() => ({models:[]})),
    API.nodes.list().catch(() => ({nodes:[]})),
  ]);
  const keys = (keysD.keys || []).length;
  const models = (modelsD.models || []).length;
  const nodes = (nodesD.nodes || []).length;
  $('#ovCards').innerHTML =
    card('服务状态', '运行中', 'green', 'OpenAI / Gemini 兼容') +
    card('API 密钥', keys, 'gold') +
    card('模型', models, 'blue') +
    card('代理节点', nodes, '');
}
