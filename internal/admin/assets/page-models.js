let modelRows = [];
let modelAliasMap = {};
let modelGlobalSettings = { fake_stream_enabled: true, model_turn_guard_enabled: true };
let modelImportMode = 'models';

async function loadModels() {
  const [data, settingsData] = await Promise.all([API.models.get(), API.settings.get()]);
  modelRows = (data.models || []).map(m => typeof m === 'string'
    ? defaultModelRow(m)
    : {
        id: String(m.id || '').trim(),
        enabled: !!m.enabled,
        fake_stream_enabled: !!m.fake_stream_enabled,
        trailing_fix_enabled: !!m.trailing_fix_enabled,
      });
  modelAliasMap = Object.assign({}, data.alias_map || {});
  modelGlobalSettings = Object.assign(modelGlobalSettings, settingsData.settings || settingsData || {});
  renderModelTable();
}

function defaultModelRow(id) {
  id = String(id || '').trim();
  return {
    id,
    enabled: true,
    fake_stream_enabled: true,
    trailing_fix_enabled: id === 'gemini-3.6-flash' || id === 'gemini-3.5-flash-lite',
  };
}

function aliasesForModel(id) {
  return Object.entries(modelAliasMap).filter(([, target]) => target === id).map(([alias]) => alias).sort();
}

function renderModelTable() {
  const body = $('#modelsTableBody');
  if (!body) return;
  body.innerHTML = modelRows.map((row, index) => {
    const aliases = aliasesForModel(row.id);
    const chips = aliases.map((alias, aliasIndex) => `<span class="model-alias-chip">${esc(alias)}<button type="button" title="删除别名" onclick="removeModelAlias(${index},${aliasIndex})">×</button></span>`).join('');
    return `<tr>
      <td class="model-check"><input type="checkbox" ${row.enabled ? 'checked' : ''} onchange="setModelFlag(${index},'enabled',this.checked)"></td>
      <td class="model-check"><input type="checkbox" ${row.fake_stream_enabled ? 'checked' : ''} ${modelGlobalSettings.fake_stream_enabled ? '' : 'disabled'} onchange="setModelFlag(${index},'fake_stream_enabled',this.checked)"></td>
      <td class="model-check"><input type="checkbox" ${row.trailing_fix_enabled ? 'checked' : ''} ${modelGlobalSettings.model_turn_guard_enabled ? '' : 'disabled'} onchange="setModelFlag(${index},'trailing_fix_enabled',this.checked)"></td>
      <td><input class="model-id-input font-mono" value="${esc(row.id)}" onchange="renameModel(${index},this.value)"></td>
      <td><div class="model-alias-list">${chips}</div><div class="model-alias-add"><input id="modelAliasInput_${index}" placeholder="输入别名"><button type="button" class="btn ghost" onclick="addModelAlias(${index})">添加</button></div></td>
    </tr>`;
  }).join('');
  updateModelHeaderChecks();
  const fakeHint = $('#modelFakeGlobalHint');
  if (fakeHint) fakeHint.textContent = modelGlobalSettings.fake_stream_enabled ? '' : '全局假流式已关闭，局部选择暂时保留';
  const trailingHint = $('#modelTrailingGlobalHint');
  if (trailingHint) trailingHint.textContent = modelGlobalSettings.model_turn_guard_enabled ? '' : '尾部修复总开关已关闭，局部选择暂时保留';
}

function setModelFlag(index, key, value) {
  if (modelRows[index]) modelRows[index][key] = !!value;
  updateModelHeaderChecks();
  window.hasUnsavedSettings = true;
}

function setAllModelFlags(key, value) {
  modelRows.forEach(row => { row[key] = !!value; });
  renderModelTable();
  window.hasUnsavedSettings = true;
}

function updateModelHeaderChecks() {
  [['enabled', 'allModelsEnabled'], ['fake_stream_enabled', 'allModelsFake'], ['trailing_fix_enabled', 'allModelsTrailing']].forEach(([key, id]) => {
    const el = $('#' + id);
    if (!el) return;
    const selected = modelRows.filter(row => row[key]).length;
    el.checked = modelRows.length > 0 && selected === modelRows.length;
    el.indeterminate = selected > 0 && selected < modelRows.length;
    el.disabled = (key === 'fake_stream_enabled' && !modelGlobalSettings.fake_stream_enabled) ||
      (key === 'trailing_fix_enabled' && !modelGlobalSettings.model_turn_guard_enabled);
  });
}

function renameModel(index, value) {
  const row = modelRows[index];
  if (!row) return;
  const oldID = row.id;
  const newID = String(value || '').trim();
  if (!newID) { toast('模型 ID 不能为空'); renderModelTable(); return; }
  if (modelRows.some((item, i) => i !== index && item.id === newID)) { toast('模型 ID 已存在'); renderModelTable(); return; }
  row.id = newID;
  Object.keys(modelAliasMap).forEach(alias => { if (modelAliasMap[alias] === oldID) modelAliasMap[alias] = newID; });
  renderModelTable();
  window.hasUnsavedSettings = true;
}

function addModelRow() {
  let serial = modelRows.length + 1;
  while (modelRows.some(row => row.id === '新模型-' + serial)) serial++;
  modelRows.push(defaultModelRow('新模型-' + serial));
  renderModelTable();
  window.hasUnsavedSettings = true;
}

function addModelAlias(index) {
  const row = modelRows[index];
  const input = $('#modelAliasInput_' + index);
  const alias = String(input && input.value || '').trim();
  if (!row || !alias) return;
  if (modelAliasMap[alias] && modelAliasMap[alias] !== row.id) { toast(`别名“${alias}”已指向 ${modelAliasMap[alias]}`); return; }
  modelAliasMap[alias] = row.id;
  renderModelTable();
  window.hasUnsavedSettings = true;
}

function removeModelAlias(modelIndex, aliasIndex) {
  const row = modelRows[modelIndex];
  const alias = row ? aliasesForModel(row.id)[aliasIndex] : '';
  if (alias) delete modelAliasMap[alias];
  renderModelTable();
  window.hasUnsavedSettings = true;
}

async function saveModels() {
  const ids = modelRows.map(row => row.id.trim());
  if (ids.some(id => !id)) { toast('模型 ID 不能为空'); return; }
  if (new Set(ids).size !== ids.length) { toast('模型 ID 不能重复'); return; }
  await API.models.put(modelRows, modelAliasMap);
  toast('模型设置已保存');
  window.hasUnsavedSettings = false;
  await loadModels();
}

function openModelImport(mode) {
  modelImportMode = mode;
  $('#modelImportTitle').textContent = mode === 'models' ? '导入模型列表' : '导入别名列表';
  $('#modelImportHint').textContent = mode === 'models' ? '每行一个模型 ID，将与现有列表合并。' : '每行格式：别名=模型ID，将与现有别名合并。';
  $('#modelImportText').value = '';
  $('#modelImportPreview').textContent = '粘贴内容后显示预览';
  $('#modelImportModal').classList.remove('hidden');
}

function closeModelImport() { $('#modelImportModal').classList.add('hidden'); }

function parseModelImport() {
  const lines = $('#modelImportText').value.split(/\r?\n/).map(s => s.trim()).filter(Boolean);
  if (modelImportMode === 'models') {
    const unique = [...new Set(lines)];
    const existing = new Set(modelRows.map(row => row.id));
    return { items: unique, added: unique.filter(id => !existing.has(id)).length, duplicate: lines.length - unique.length + unique.filter(id => existing.has(id)).length, invalid: 0, conflicts: 0 };
  }
  const items = [];
  let invalid = 0; let conflicts = 0;
  lines.forEach(line => {
    const pos = line.indexOf('=');
    const alias = pos > 0 ? line.slice(0, pos).trim() : '';
    const target = pos > 0 ? line.slice(pos + 1).trim() : '';
    if (!alias || !target) { invalid++; return; }
    if (modelAliasMap[alias] && modelAliasMap[alias] !== target) conflicts++;
    items.push({ alias, target });
  });
  return { items, added: items.length, duplicate: 0, invalid, conflicts };
}

function previewModelImport() {
  const p = parseModelImport();
  $('#modelImportPreview').textContent = `可导入 ${p.added} 项；重复 ${p.duplicate} 项；无效 ${p.invalid} 项；冲突 ${p.conflicts} 项`;
}

function applyModelImport() {
  const parsed = parseModelImport();
  if (parsed.invalid) { toast('请先修正无效导入行'); return; }
  if (parsed.conflicts && !window.confirm(`检测到 ${parsed.conflicts} 个别名冲突，是否覆盖？`)) return;
  if (modelImportMode === 'models') {
    const existing = new Set(modelRows.map(row => row.id));
    parsed.items.forEach(id => { if (!existing.has(id)) { modelRows.push(defaultModelRow(id)); existing.add(id); } });
  } else {
    parsed.items.forEach(({ alias, target }) => {
      if (!modelRows.some(row => row.id === target)) modelRows.push(defaultModelRow(target));
      modelAliasMap[alias] = target;
    });
  }
  closeModelImport();
  renderModelTable();
  window.hasUnsavedSettings = true;
  toast('导入内容已合并，请点击保存');
}
