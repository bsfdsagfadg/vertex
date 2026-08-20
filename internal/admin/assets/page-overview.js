// ==========================================
// Vertex AI Proxy Admin - Overview Page
// ==========================================

async function loadOverview() {
  const container = $('#ovCards');
  if (!container) return;

  const [keysD, modelsD, nodesD] = await Promise.all([
    API.keys.list().catch(() => ({ keys: [] })),
    API.models.get().catch(() => ({ models: [] })),
    API.nodes.list().catch(() => ({ nodes: [] })),
  ]);

  const keys = (keysD.keys || []).length;
  const models = (modelsD.models || []).length;
  const nodes = (nodesD.nodes || []).length;
  const spAvail = nodesD.sticky_pool_available || 0;
  const spInUse = nodesD.sticky_pool_in_use || 0;
  const stickySub = `可用 ${spAvail} / 占用 ${spInUse}`;

  const makeCard = (label, value, cls, sub) => {
    const card = el('div', { className: 'card glass hoverable stat' }, [
      el('div', { className: 'label', text: label }),
      el('div', { className: `value ${cls || ''}`, text: String(value) }),
    ]);
    if (sub) {
      card.appendChild(el('div', { className: 'sub', text: sub }));
    }
    return card;
  };

  container.replaceChildren(
    makeCard('服务状态', '运行中', 'green', 'OpenAI / Gemini 兼容'),
    makeCard('API 密钥', keys, 'gold'),
    makeCard('模型', models, 'blue'),
    makeCard('代理节点', nodes, ''),
    makeCard('粘性节点', spAvail, 'gold', stickySub)
  );
}
