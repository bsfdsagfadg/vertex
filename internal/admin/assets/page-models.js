// ==========================================
// Vertex AI Proxy Admin - Models Page
// ==========================================

async function loadModels() {
  const [data, settingsData] = await Promise.all([API.models.get(), API.settings.get()]);
  AppState.modelRows = (data.models || []).map(m => (typeof m === 'string'
    ? defaultModelRow(m)
    : {
        id: String(m.id || '').trim(),
        enabled: !!m.enabled,
        fake_stream_enabled: !!m.fake_stream_enabled,
        trailing_fix_enabled: !!m.trailing_fix_enabled,
      }));
  AppState.modelAliasMap = Object.assign({}, data.alias_map || {});
  AppState.modelGlobalSettings = Object.assign(
    AppState.modelGlobalSettings,
    settingsData.settings || settingsData || {}
  );
  renderModelTable();
}

function defaultModelRow(id) {
  const modelID = String(id || '').trim();
  return {
    id: modelID,
    enabled: true,
    fake_stream_enabled: true,
    trailing_fix_enabled: modelID === 'gemini-3.6-flash' || modelID === 'gemini-3.5-flash-lite',
  };
}

function aliasesForModel(id) {
  return Object.entries(AppState.modelAliasMap)
    .filter(([, target]) => target === id)
    .map(([alias]) => alias)
    .sort();
}

function renderModelTable() {
  const body = $('#modelsTableBody');
  if (!body) return;
  body.replaceChildren();

  AppState.modelRows.forEach((row, index) => {
    const aliases = aliasesForModel(row.id);
    const tr = document.createElement('tr');

    // 1. enabled
    const tdEnabled = el('td', { className: 'model-check' });
    const cbEnabled = el('input', {
      type: 'checkbox',
      checked: row.enabled,
      onchange: (e) => setModelFlag(index, 'enabled', e.target.checked),
    });
    tdEnabled.appendChild(cbEnabled);
    tr.appendChild(tdEnabled);

    // 2. fake_stream
    const tdFake = el('td', { className: 'model-check' });
    const cbFake = el('input', {
      type: 'checkbox',
      checked: row.fake_stream_enabled,
      disabled: !AppState.modelGlobalSettings.fake_stream_enabled,
      onchange: (e) => setModelFlag(index, 'fake_stream_enabled', e.target.checked),
    });
    tdFake.appendChild(cbFake);
    tr.appendChild(tdFake);

    // 3. trailing_fix
    const tdTrailing = el('td', { className: 'model-check' });
    const cbTrailing = el('input', {
      type: 'checkbox',
      checked: row.trailing_fix_enabled,
      disabled: !AppState.modelGlobalSettings.model_turn_guard_enabled,
      onchange: (e) => setModelFlag(index, 'trailing_fix_enabled', e.target.checked),
    });
    tdTrailing.appendChild(cbTrailing);
    tr.appendChild(tdTrailing);

    // 4. model id
    const tdId = el('td');
    const idInput = el('input', {
      className: 'model-id-input font-mono',
      style: { width: '260px', flex: 'none' },
      value: row.id,
      onchange: (e) => renameModel(index, e.target.value),
    });
    tdId.appendChild(idInput);
    tr.appendChild(tdId);

    // 5. aliases
    const tdAliases = el('td');
    const aliasList = el('div', { className: 'model-alias-list' });
    aliases.forEach((alias, aliasIndex) => {
      const chip = el('span', { className: 'model-alias-chip', text: alias });
      const delBtn = el('button', {
        type: 'button',
        title: '删除别名',
        text: '×',
        onclick: () => removeModelAlias(index, aliasIndex),
      });
      chip.appendChild(delBtn);
      aliasList.appendChild(chip);
    });

    const aliasAdd = el('div', { className: 'model-alias-add' });
    const aliasInput = el('input', {
      id: `modelAliasInput_${index}`,
      style: { width: '260px', flex: 'none' },
      placeholder: '输入别名',
    });
    const addBtn = el('button', {
      type: 'button',
      className: 'btn ghost',
      text: '添加',
      onclick: () => addModelAlias(index),
    });
    aliasAdd.appendChild(aliasInput);
    aliasAdd.appendChild(addBtn);

    tdAliases.appendChild(aliasList);
    tdAliases.appendChild(aliasAdd);
    tr.appendChild(tdAliases);

    // 6. actions
    const tdActions = el('td', { style: { textAlign: 'center', verticalAlign: 'middle' } });
    const delRowBtn = el('button', {
      type: 'button',
      className: 'btn danger',
      text: '删除',
      onclick: () => removeModelRow(index),
    });
    tdActions.appendChild(delRowBtn);
    tr.appendChild(tdActions);

    body.appendChild(tr);
  });

  updateModelHeaderChecks();
  const fakeHint = $('#modelFakeGlobalHint');
  if (fakeHint) {
    fakeHint.textContent = AppState.modelGlobalSettings.fake_stream_enabled ? '' : '全局假流式已关闭，局部选择暂时保留';
  }
  const trailingHint = $('#modelTrailingGlobalHint');
  if (trailingHint) {
    trailingHint.textContent = AppState.modelGlobalSettings.model_turn_guard_enabled ? '' : '尾部修复总开关已关闭，局部选择暂时保留';
  }
}

function setModelFlag(index, key, value) {
  if (AppState.modelRows[index]) AppState.modelRows[index][key] = !!value;
  updateModelHeaderChecks();
  AppState.markDirty(true);
}

function setAllModelFlags(key, value) {
  AppState.modelRows.forEach(row => { row[key] = !!value; });
  renderModelTable();
  AppState.markDirty(true);
}

function updateModelHeaderChecks() {
  [['enabled', 'allModelsEnabled'], ['fake_stream_enabled', 'allModelsFake'], ['trailing_fix_enabled', 'allModelsTrailing']].forEach(([key, id]) => {
    const element = $('#' + id);
    if (!element) return;
    const selected = AppState.modelRows.filter(row => row[key]).length;
    element.checked = AppState.modelRows.length > 0 && selected === AppState.modelRows.length;
    element.indeterminate = selected > 0 && selected < AppState.modelRows.length;
    element.disabled = (key === 'fake_stream_enabled' && !AppState.modelGlobalSettings.fake_stream_enabled) ||
      (key === 'trailing_fix_enabled' && !AppState.modelGlobalSettings.model_turn_guard_enabled);
  });
}

function renameModel(index, value) {
  const row = AppState.modelRows[index];
  if (!row) return;
  const oldID = row.id;
  const newID = String(value || '').trim();
  if (!newID) { toast('模型 ID 不能为空'); renderModelTable(); return; }
  if (AppState.modelRows.some((item, i) => i !== index && item.id === newID)) { toast('模型 ID 已存在'); renderModelTable(); return; }
  row.id = newID;
  Object.keys(AppState.modelAliasMap).forEach(alias => {
    if (AppState.modelAliasMap[alias] === oldID) AppState.modelAliasMap[alias] = newID;
  });
  renderModelTable();
  AppState.markDirty(true);
}

function addModelRow() {
  let serial = AppState.modelRows.length + 1;
  while (AppState.modelRows.some(row => row.id === '新模型-' + serial)) serial++;
  AppState.modelRows.push(defaultModelRow('新模型-' + serial));
  renderModelTable();
  AppState.markDirty(true);
}

function removeModelRow(index) {
  const row = AppState.modelRows[index];
  if (!row) return;
  const oldID = row.id;
  Object.keys(AppState.modelAliasMap).forEach(alias => {
    if (AppState.modelAliasMap[alias] === oldID) delete AppState.modelAliasMap[alias];
  });
  AppState.modelRows.splice(index, 1);
  renderModelTable();
  AppState.markDirty(true);
}

function addModelAlias(index) {
  const row = AppState.modelRows[index];
  const input = $('#modelAliasInput_' + index);
  const alias = String(input && input.value || '').trim();
  if (!row || !alias) return;
  if (AppState.modelAliasMap[alias] && AppState.modelAliasMap[alias] !== row.id) {
    toast(`别名“${alias}”已指向 ${AppState.modelAliasMap[alias]}`);
    return;
  }
  AppState.modelAliasMap[alias] = row.id;
  renderModelTable();
  AppState.markDirty(true);
}

function removeModelAlias(modelIndex, aliasIndex) {
  const row = AppState.modelRows[modelIndex];
  const alias = row ? aliasesForModel(row.id)[aliasIndex] : '';
  if (alias) delete AppState.modelAliasMap[alias];
  renderModelTable();
  AppState.markDirty(true);
}

async function saveModels() {
  const ids = AppState.modelRows.map(row => row.id.trim());
  if (ids.some(id => !id)) { toast('模型 ID 不能为空'); return; }
  if (new Set(ids).size !== ids.length) { toast('模型 ID 不能重复'); return; }
  await API.models.put(AppState.modelRows, AppState.modelAliasMap);
  toast('模型设置已保存');
  AppState.markDirty(false);
  await loadModels();
}

function openModelImport(mode) {
  AppState.modelImportMode = mode;
  $('#modelImportTitle').textContent = mode === 'models' ? '导入模型列表' : '导入别名列表';
  $('#modelImportHint').textContent = mode === 'models' ? '每行一个模型 ID，将与现有列表合并。' : '每行格式：别名=模型ID，将与现有别名合并。';
  $('#modelImportText').value = '';
  $('#modelImportPreview').textContent = '粘贴内容后显示预览';
  $('#modelImportModal').classList.remove('hidden');
}

function closeModelImport() {
  $('#modelImportModal').classList.add('hidden');
}

function parseModelImport() {
  const lines = $('#modelImportText').value.split(/\r?\n/).map(s => s.trim()).filter(Boolean);
  if (AppState.modelImportMode === 'models') {
    const unique = [...new Set(lines)];
    const existing = new Set(AppState.modelRows.map(row => row.id));
    return {
      items: unique,
      added: unique.filter(id => !existing.has(id)).length,
      duplicate: lines.length - unique.length + unique.filter(id => existing.has(id)).length,
      invalid: 0,
      conflicts: 0,
    };
  }
  const items = [];
  let invalid = 0;
  let conflicts = 0;
  lines.forEach(line => {
    const pos = line.indexOf('=');
    const alias = pos > 0 ? line.slice(0, pos).trim() : '';
    const target = pos > 0 ? line.slice(pos + 1).trim() : '';
    if (!alias || !target) { invalid++; return; }
    if (AppState.modelAliasMap[alias] && AppState.modelAliasMap[alias] !== target) conflicts++;
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
  if (parsed.conflicts && !confirm(`检测到 ${parsed.conflicts} 个别名冲突，是否覆盖？`)) return;
  if (AppState.modelImportMode === 'models') {
    const existing = new Set(AppState.modelRows.map(row => row.id));
    parsed.items.forEach(id => {
      if (!existing.has(id)) {
        AppState.modelRows.push(defaultModelRow(id));
        existing.add(id);
      }
    });
  } else {
    parsed.items.forEach(({ alias, target }) => {
      if (!AppState.modelRows.some(row => row.id === target)) {
        AppState.modelRows.push(defaultModelRow(target));
      }
      AppState.modelAliasMap[alias] = target;
    });
  }
  closeModelImport();
  renderModelTable();
  AppState.markDirty(true);
  toast('导入内容已合并，请点击保存');
}
