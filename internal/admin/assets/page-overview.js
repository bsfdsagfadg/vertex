import { $ } from './utils.js';

let API;
export function configureOverviewService(service) { API = service; }

export async function loadOverview() {
  const card = (label, value, cls, sub) => `<div class="card glass hoverable stat"><div class="label">${label}</div><div class="value ${cls||''}">${value}</div>${sub?`<div class="sub">${sub}</div>`:''}</div>`;
  const [keysD, modelsD, nodesD] = await Promise.all([
    API.keys.list().catch(() => ({keys:[]})),
    API.models.get().catch(() => ({models:[]})),
    API.nodes.list().catch(() => ({nodes:[]})),
  ]);
  const keys = (keysD.keys || []).length;
  const models = (modelsD.models || []).length;
  const nodes = (nodesD.nodes || []).length;
  const spAvail = nodesD.sticky_pool_available || 0;
  const spInUse = nodesD.sticky_pool_in_use || 0;
  const stickySub = `可用 ${spAvail} / 占用 ${spInUse}`;
  $('#ovCards').innerHTML =
    card('服务状态', '运行中', 'green', 'OpenAI / Gemini 兼容') +
    card('API 密钥', keys, 'gold') +
    card('模型', models, 'blue') +
    card('代理节点', nodes, '') +
    card('粘性节点', spAvail, 'gold', stickySub);
}
