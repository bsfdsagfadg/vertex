const SETTINGS_FIELDS = [
  // 🚀 Group: pool (并发与 Token 池管理)
  { k: 'parallel_pool_enabled', label: '并发请求池', type: 'bool', group: 'pool', desc: '同时请求多个健康节点，首包到达即采纳，降低延迟' },
  { k: 'parallel_pool_retry_enabled', label: '并发池单点重试', type: 'bool', group: 'pool', desc: '开启后允许并发池内节点429后依然等待并重试（适用于少节点场景）' },
  { k: 'parallel_pool_size', label: '并发数', type: 'number', max: 20, min: 1, group: 'pool', desc: '并发抢跑的节点数 (默认 15，最大 20)' },
  { k: 'parallel_pool_delay_dynamic', label: '动态对冲延迟', type: 'bool', group: 'pool', desc: '根据节点平均响应时间动态调整并发启动间隔，平衡延迟与流量消耗' },
  { k: 'recaptcha_try_entry_or_direct', label: '优先前置/直连抓取 RT', type: 'bool', group: 'pool', desc: '开启时获取 reCAPTCHA Token 优先尝试前置代理/直连；关闭或失败时顺次轮询健康候选节点' },

  // 🛠 Group: core (核心控制与基础参数)
  { k: 'max_retries', label: '上游重试次数', type: 'number', group: 'core', desc: '上游请求失败时的重试次数；总尝试 = 此值 + 1' },
  { k: 'max_n', label: '最大候选数 (max_n)', type: 'number', group: 'core', desc: '限制客户端一次生成回答的条数上限，防滥用刷量 (默认 8)' },
  { k: 'max_spill_mb', label: '最大内存缓冲 (MB)', type: 'number', group: 'core', desc: '上传大文件时，超过此大小将写入磁盘，防爆内存 (默认 2048)' },
  { k: 'aggregate_stream', label: '聚合流式', type: 'bool', group: 'core', desc: '拦截流式请求，改为一次性返回完整结果的单块流（解决部分客户端单字流式卡顿问题）' },
  { k: 'debug_mode', label: 'Debug 日志', type: 'bool', group: 'core', desc: '开启更详细的错误与负载调试日志' },

  { k: 'default_image_size', label: '默认图片清晰度', type: 'select', group: 'core', opts: ['512', '1K', '2K', '4K'], desc: '图模型请求未指定清晰度时的默认档位（按模型能力自动降级）' },
  { k: 'default_thinking_level', label: '默认思考等级', type: 'select', group: 'core', opts: ['自动', '最低', '低', '中', '高'], desc: '文本/图模型请求未指定思考参数时的默认档位（按模型能力自动适配）' },
  { k: 'default_response_modalities', label: '默认图片输出模态', type: 'select', group: 'core', opts: ['图文', '仅图片'], desc: '图模型未指定响应内容时的默认类型（图文 = 图片+文本，仅图片 = 仅纯图片）' },
  { k: 'stream_idle_timeout_seconds', label: '流式包间空闲超时(秒)', type: 'number', group: 'core', min: 1, desc: '流式传输中连续无数据字节的超时时间（默认 20 秒），防止网络波动卡死' },
  { k: 'request_timeout_seconds', label: '请求总超时(秒)', type: 'number', group: 'core', min: 1, desc: '整个 HTTP/流式请求的最大总超时限制（默认 180 秒）' },

  // 🛡 Group: security (安全增强与模型策略)
  { k: 'drop_max_tokens', label: '移除 maxOutputTokens', type: 'bool', group: 'security', desc: '移除输出 token 上限，让模型自由输出' },
  { k: 'trailing_model_fix_enabled', label: '尾部模型回合兼容', type: 'bool', group: 'security', desc: '开启后自动修复模型末尾回合未闭合问题（多轮续写与工具调用后追加“继续”），避免上游报错' },
  { k: 'trailing_fix_models', label: '尾部兼容模型清单', type: 'model_select', group: 'security', desc: '在下拉菜单中勾选启用尾部兼容的模型（纯精确匹配，-001、-lite 等层级变体需各自勾选）。模型清单固定，新增模型时请更新模型清单' },
];

let curSettings = {};
const DEFAULT_TRAILING_MODELS = ['gemini-3.5-flash-lite', 'gemini-3.6-flash', 'gemini-3.7-flash'];

// renderModelSelect 渲染挂在“尾部模型回合兼容”开关右侧的下拉勾选组件。
// 面板列出系统已注册模型；配置中已有的非注册模型追加在列表尾部（带“已配置”标记，向后兼容不丢数据）。
// 模型名经 esc()（utils.js）转义后拼入 HTML，防止配置文件中的特殊字符造成 HTML 注入。
function renderModelSelect(f, sysModels) {
  const raw = curSettings[f.k];
  const selected = Array.isArray(raw) ? raw : DEFAULT_TRAILING_MODELS;
  const extra = selected.filter(m => !sysModels.includes(m));
  const all = sysModels.concat(extra);
  const items = all.map(m => `
    <label class="chk"><input type="checkbox" value="${esc(m)}" ${selected.includes(m) ? 'checked' : ''}>${esc(m)}${extra.includes(m) ? '<span class="model-dd-tag">已配置</span>' : ''}</label>`).join('');
  return `<div class="model-dd" id="set_${f.k}_dd">
    <button type="button" class="btn ghost model-dd-btn" id="set_${f.k}_dd_btn">已选 ${selected.length} 个模型 <span class="model-dd-caret">▾</span></button>
    <div class="model-dd-panel hidden" id="set_${f.k}_dd_panel">
      <div class="model-dd-tools">
        <button type="button" class="link" data-act="all">全选</button>
        <button type="button" class="link" data-act="none">清空</button>
        <span class="model-dd-count" id="set_${f.k}_dd_count">已选 ${selected.length}</span>
      </div>
      <div class="model-dd-list">${items || '<div class="desc">系统暂无已注册模型</div>'}</div>
      ${f.desc ? `<div class="desc">${f.desc}</div>` : ''}
    </div>
  </div>`;
}

// collectModelSelect 收集下拉面板中勾选的模型（按 DOM 顺序 = 清单顺序）。
function collectModelSelect(f) {
  const panel = $('#set_' + f.k + '_dd_panel');
  if (!panel) return [];
  const out = [];
  panel.querySelectorAll('input[type="checkbox"]:checked').forEach(cb => {
    const v = cb.value.trim();
    if (v && !out.includes(v)) out.push(v);
  });
  return out;
}

async function loadSettings() {
  const [d, modelsRes] = await Promise.all([
    API.settings.get(),
    API.models.get().catch(() => ({ models: [] })),
  ]);
  curSettings = d.settings || d;
  const sysModels = modelsRes.models || [];

  const fld = (f) => {
    if (f.type === 'model_select') return renderModelSelect(f, sysModels);
    const v = curSettings[f.k];
    if (f.type === 'bool') {
      const toggle = `<label class="toggle"><input type="checkbox" id="set_${f.k}" ${v ? 'checked' : ''}><span class="track"></span></label>`;
      if (f.k === 'trailing_model_fix_enabled') {
        const modelsField = SETTINGS_FIELDS.find(x => x.k === 'trailing_fix_models');
        const head = `<div class="trailing-head"><label for="set_${f.k}">${f.label}</label>${renderModelSelect(modelsField, sysModels)}</div>`;
        return `<div class="field bool trailing-fix"><div class="min-w-0">${head}${f.desc ? `<div class="desc mt-4px">${f.desc}</div>` : ''}</div>${toggle}</div>`;
      }
      return `<div class="field bool"><div class="min-w-0"><label for="set_${f.k}">${f.label}</label>${f.desc ? `<div class="desc mt-4px">${f.desc}</div>` : ''}</div>${toggle}</div>`;
    }
    let input;
    if (f.type === 'select') input = `<select id="set_${f.k}">${f.opts.map(o => `<option ${o === v ? 'selected' : ''}>${o}</option>`).join('')}</select>`;
    else input = `<input type="${f.type}" id="set_${f.k}" value="${v ?? ''}" ${f.max !== undefined ? `max="${f.max}" oninput="if(this.value!=='' && parseInt(this.value)>${f.max}) this.value='${f.max}'"` : ''} ${f.min !== undefined ? `min="${f.min}"` : ''}>`;
    return `<div class="field"><label for="set_${f.k}">${f.label}</label>${input}${f.desc ? `<div class="desc">${f.desc}</div>` : ''}</div>`;
  };

  // 【核心修改：定义视觉功能分组】
  const groups = {
    pool: { title: '🚀 并发与 Token 池管理', fields: [] },
    core: { title: '🛠 核心控制与基础参数', fields: [] },
    security: { title: '🛡 安全增强与模型策略', fields: [] }
  };

  SETTINGS_FIELDS.forEach(f => {
    if (groups[f.group]) {
      groups[f.group].fields.push(f);
    }
  });

  let sectionsHtml = '';
  for (const [key, g] of Object.entries(groups)) {
    const numFields = g.fields.filter(f => f.type !== 'bool' && f.type !== 'model_select');
    const boolFields = g.fields.filter(f => f.type === 'bool');

    let extraHtml = '';
    if (key === 'security') {
      extraHtml = `
        <div class="field" style="margin-top:12px; display:flex; justify-content:space-between; align-items:center; background:rgba(255,255,255,0.03); padding:14px; border-radius:10px; border:1px solid var(--stroke);">
          <div>
            <div style="font-weight:600; font-size:14px;">管理后台密码</div>
            <div class="desc" style="margin-top:4px;">定期修改密码有助于保障管理后台及节点会话安全</div>
          </div>
          <button type="button" class="btn ghost" style="padding:8px 16px;" onclick="showChangePasswordModal()">修改密码</button>
        </div>
      `;
    }

    sectionsHtml += `
      <div class="settings-section-title">${g.title}</div>
      ${numFields.length ? `<div class="grid grid-2">${numFields.map(fld).join('')}</div>` : ''}
      ${boolFields.length ? `<div class="grid grid-2" style="margin-top:10px;">${boolFields.map(fld).join('')}</div>` : ''}
      ${extraHtml}
    `;
  }

  $('#settingsForm').innerHTML =
    sectionsHtml +
    '<button class="btn mt-14px" onclick="saveSettings()">保存设置</button>';

  $('#settingsForm').addEventListener('input', () => window.hasUnsavedSettings = true);
  $('#settingsForm').addEventListener('change', () => window.hasUnsavedSettings = true);
  window.hasUnsavedSettings = false;

  if (!window._hasSettingsUnloadListener) {
    window.addEventListener('beforeunload', (e) => {
      if (window.hasUnsavedSettings) {
        e.preventDefault();
        e.returnValue = '';
      }
    });
    window._hasSettingsUnloadListener = true;
  }

  const parallelRetryEl = $('#set_parallel_pool_retry_enabled');
  const parallelEl = $('#set_parallel_pool_enabled');
  if (parallelEl && parallelRetryEl) {
    const updateRetryDisabled = () => {
      const disabled = !parallelEl.checked;
      parallelRetryEl.disabled = disabled;
      if (disabled) parallelRetryEl.checked = false;
      const retryContainer = parallelRetryEl.closest('.field');
      if (retryContainer) {
        retryContainer.style.opacity = disabled ? '0.5' : '1';
        retryContainer.style.pointerEvents = disabled ? 'none' : '';
        const retryDesc = retryContainer.querySelector('.desc');
        if (retryDesc) {
          retryDesc.textContent = disabled ? '需先启用并发请求池' : '开启后允许并发池内节点429后依然等待并重试（适用于少节点场景）';
        }
      }
    };
    updateRetryDisabled();
    parallelEl.addEventListener('change', updateRetryDisabled);
  }

  const trailingToggle = $('#set_trailing_model_fix_enabled');
  const trailingDd = $('#set_trailing_fix_models_dd');
  if (trailingToggle && trailingDd) {
    const updateTrailingDisabled = () => {
      const disabled = !trailingToggle.checked;
      trailingDd.style.opacity = disabled ? '0.5' : '1';
      trailingDd.style.pointerEvents = disabled ? 'none' : '';
      if (disabled) {
        const panel = $('#set_trailing_fix_models_dd_panel');
        if (panel) panel.classList.add('hidden');
      }
    };
    updateTrailingDisabled();
    trailingToggle.addEventListener('change', updateTrailingDisabled);
  }

  const ddBtn = $('#set_trailing_fix_models_dd_btn');
  const ddPanel = $('#set_trailing_fix_models_dd_panel');
  if (ddBtn && ddPanel) {
    const updateDdCount = () => {
      const n = collectModelSelect({ k: 'trailing_fix_models' }).length;
      const countEl = $('#set_trailing_fix_models_dd_count');
      if (countEl) countEl.textContent = '已选 ' + n;
      ddBtn.innerHTML = `已选 ${n} 个模型 <span class="model-dd-caret">▾</span>`;
      window.hasUnsavedSettings = true;
    };
    ddBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      ddPanel.classList.toggle('hidden');
    });
    ddPanel.addEventListener('change', (e) => {
      if (e.target.matches('input[type="checkbox"]')) updateDdCount();
    });
    ddPanel.querySelectorAll('button[data-act]').forEach(b => {
      b.addEventListener('click', () => {
        ddPanel.querySelectorAll('input[type="checkbox"]').forEach(cb => { cb.checked = b.dataset.act === 'all'; });
        updateDdCount();
      });
    });
    if (!window._hasModelDdListener) {
      document.addEventListener('click', (e) => {
        if (!e.target.closest('.model-dd')) {
          document.querySelectorAll('.model-dd-panel:not(.hidden)').forEach(p => p.classList.add('hidden'));
        }
      });
      document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
          document.querySelectorAll('.model-dd-panel:not(.hidden)').forEach(p => p.classList.add('hidden'));
        }
      });
      window._hasModelDdListener = true;
    }
  }
}

async function saveSettings() {
  const out = {};
  for (const f of SETTINGS_FIELDS) {
    if (f.type === 'model_select') {
      out[f.k] = collectModelSelect(f);
      continue;
    }
    const el = $('#set_' + f.k);
    if (!el) continue;
    if (f.type === 'bool') out[f.k] = el.checked;
    else if (f.type === 'number') out[f.k] = parseInt(el.value || '0', 10);
    else out[f.k] = el.value;
  }
  // Keep sending whatever telemetry_enabled is in curSettings to prevent config loss/errors
  if (curSettings.telemetry_enabled !== undefined) {
    out['telemetry_enabled'] = curSettings.telemetry_enabled;
  }
  if (!out['parallel_pool_enabled']) {
    out['parallel_pool_retry_enabled'] = false;
  }
  await API.settings.put(out); toast('设置已保存');
  window.hasUnsavedSettings = false;
  await loadSettings();
}

function showChangePasswordModal() {
  $('#oldPwInput').value = '';
  $('#newPwInput').value = '';
  $('#confirmPwInput').value = '';
  $('#changePwErr').textContent = '';
  $('#changePasswordModal').classList.remove('hidden');
}

function closeChangePasswordModal() {
  $('#changePasswordModal').classList.add('hidden');
}

async function submitChangePassword() {
  const oldPw = $('#oldPwInput').value;
  const newPw = $('#newPwInput').value;
  const confirmPw = $('#confirmPwInput').value;
  const errEl = $('#changePwErr');

  if (!oldPw) { errEl.textContent = '请输入原密码'; return; }
  if (newPw.length < 6) { errEl.textContent = '新密码长度至少需要 6 个字符'; return; }
  if (newPw !== confirmPw) { errEl.textContent = '两次输入的新密码不一致'; return; }

  errEl.textContent = '';
  try {
    await API.changePassword(oldPw, newPw);
    closeChangePasswordModal();
    toast('密码修改成功，请使用新密码重新登录！');
    setTimeout(() => { logout(); }, 1500);
  } catch (e) {
    errEl.textContent = e.message || '修改密码失败';
  }
}