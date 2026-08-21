async function loadModels() { const d = await API.models.get(); $('#modelsList').textContent = (d.models || []).join('\n'); $('#aliasMap').textContent = Object.entries(d.alias_map || {}).map(([a, b]) => a + '=' + b).join('\n'); PAGE_CACHE['models'] = $('#page-models').innerHTML; }
async function saveModels() {
  const models = $('#modelsList').value.split('\n').map(s => s.trim()).filter(Boolean); const alias_map = {};
  $('#aliasMap').value.split('\n').forEach(line => { const i = line.indexOf('='); if (i > 0) alias_map[line.slice(0, i).trim()] = line.slice(i + 1).trim(); });
  await API.models.put(models, alias_map); toast('模型已保存');
}
