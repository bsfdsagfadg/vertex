const SETTINGS_FIELDS = [
  // 🚀 Group: pool (并发与 Token 池管理)
  { k: 'parallel_pool_enabled', label: '并发请求池', type: 'bool', group: 'pool', desc: '同时请求多个健康节点，首包到达即采纳，降低延迟' },
  { k: 'parallel_pool_retry_enabled', label: '单节点重试', type: 'bool', group: 'pool', desc: '开启：重试全部由节点内部完成（429/认证/5xx 原地重打），竞速层不换节点；关闭：每轮节点只打一次，全部失败后换一批新节点重试（总轮数 = 重试次数 + 1）' },
  { k: 'parallel_pool_sliding_window_enabled', label: '滑动窗口并发重试', type: 'bool', group: 'pool', desc: '失败节点立即补位并发重试；开启后请求量更高，请结合节点质量谨慎使用。' },
  { k: 'parallel_pool_delay_dynamic', label: '动态并发延迟', type: 'bool', group: 'pool', desc: '根据节点平均响应时间动态调整并发启动间隔。' },
  { k: 'recaptcha_try_entry_or_direct', label: '优先前置/直连抓取 reCAPTCHA Token', type: 'bool', group: 'pool', desc: '开启后可能因 Token 与请求出口 IP 不一致触发 429 风控限制，谨慎使用；默认关闭。' },
  { k: 'sticky_node_priority', label: '粘性节点优先轮询', type: 'bool', group: 'pool', desc: '启用后优先从粘性池中逐个尝试成功节点，失败即换下一个。粘性池本身始终在工作，此开关只影响优先级的分配。' },
  { k: 'parallel_pool_size', label: '并发数', type: 'number', max: 20, min: 1, group: 'pool', desc: '每轮并发抢跑的节点数 (默认 15，最大 20)' },
  { k: 'race_timeout', label: '单节点竞速超时 (秒)', type: 'number', max: 1800, min: 0, group: 'pool', desc: '单个节点在该时间内未返回首包即单独淘汰，避免卡死节点拖住整轮竞速 (0 = 不限制)' },
  { k: 'entry_proxy_probe_enabled', label: '入口代理周期拨测', type: 'bool', group: 'pool', desc: '按周期自动测试已启用的入口代理；默认关闭，需要时手动开启。' },
  { k: 'entry_proxy_probe_interval_seconds', label: '入口代理拨测间隔 (秒)', type: 'number', max: 86400, min: 60, group: 'pool', desc: '入口代理自动拨测的执行间隔，默认 300 秒' },
  { k: 'entry_proxy_probe_cooldown_seconds', label: '入口代理失败冷却 (秒)', type: 'number', max: 86400, min: 0, group: 'pool', desc: '入口代理拨测或请求失败后暂停使用入口代理的时间，0 表示不冷却' },
  { k: 'entry_proxy_probe_auto_disable_enabled', label: '入口代理连续失败自动禁用', type: 'bool', group: 'pool', desc: '只统计入口代理自动周期拨测失败；任意一次测试成功都会清零' },
  { k: 'entry_proxy_probe_auto_disable_failures', label: '入口代理自动禁用失败次数', type: 'number', max: 100, min: 1, group: 'pool', desc: '入口代理达到连续失败次数后自动禁用，默认 10 次' },

  // 🛠 Group: core (核心控制与基础参数)
  { k: 'max_retries', label: '上游重试次数', type: 'number', group: 'core', desc: '上游请求失败时的重试次数；总尝试 = 此值 + 1' },
  { k: 'max_n', label: '最大候选数 (max_n)', type: 'number', group: 'core', desc: '限制客户端一次生成回答的条数上限，防滥用刷量 (默认 8)' },
  { k: 'max_spill_mb', label: '最大内存缓冲 (MB)', type: 'number', group: 'core', desc: '上传大文件时，超过此大小将写入磁盘，防爆内存 (默认 2048)' },
  { k: 'request_timeout', label: '请求超时', type: 'number', max: 1800, min: 1, group: 'core', desc: '单次请求的最大连接时间 (默认 180 秒，最大 1800 秒)' },
  { k: 'aggregate_stream', label: '聚合流式', type: 'bool', group: 'core', desc: '拦截流式请求，改为一次性返回完整结果的单块流（解决部分客户端单字流式卡顿问题）' },
  { k: 'fake_stream_enabled', label: '假流式总开关', type: 'bool', group: 'core', desc: '控制所有模型的 fake-/假流式- 变体；可在模型页自定义。' },
  { k: 'model_turn_guard_enabled', label: '模型尾部修复', type: 'bool', group: 'core', desc: '对 gemini-3.6/3.7/3.8-flash 与 gemini-3.5-flash-lite 等新模型，自动在消息末尾追加空用户消息，修复“末尾不能是 model”校验报错。可在模型页自定义。' },
  { k: 'debug_mode', label: 'Debug 日志', type: 'bool', group: 'core', desc: '开启更详细的错误与负载调试日志' },
  { k: 'default_image_size', label: '默认图片清晰度', type: 'select', group: 'core', opts: ['512', '1K', '2K', '4K'], desc: '图模型未指定清晰度时的默认档位；不支持的档位会按模型能力回退。' },
  { k: 'default_response_modalities', label: '默认图片输出模态', type: 'select', group: 'core', opts: ['图文', '仅图片'], desc: '图模型未指定输出模态时，默认返回图文或仅图片。' },

  // 🛡 Group: security (安全增强与模型策略)
  { k: 'drop_max_tokens', label: '移除 maxOutputTokens', type: 'bool', group: 'security', desc: '移除输出 token 上限，让模型自由输出' },
];

let curSettings = {};
async function loadSettings() {
  const d = await API.settings.get(); curSettings = d.settings || d;

  const gpEl = $('#globalProxy');
  if (gpEl && curSettings.proxy_url !== undefined) {
    gpEl.value = curSettings.proxy_url;
  }

  const fld = (f) => {
    const v = curSettings[f.k];
    if (f.type === 'bool') return `<div class="field bool"><div class="min-w-0"><label for="set_${f.k}">${f.label}</label>${f.desc ? `<div class="desc mt-4px">${f.desc}</div>` : ''}</div><label class="toggle"><input type="checkbox" id="set_${f.k}" ${v ? 'checked' : ''}><span class="track"></span></label></div>`;
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
    const numFields = g.fields.filter(f => f.type !== 'bool');
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

  const stickyEl = $('#set_sticky_node_priority');
  const parallelRetryEl = $('#set_parallel_pool_retry_enabled');
  const parallelEl = $('#set_parallel_pool_enabled');
  if (stickyEl && parallelEl && parallelRetryEl) {
    const updateStickyDisabled = () => {
      const disabled = !parallelEl.checked;

      stickyEl.disabled = disabled;
      if (disabled) stickyEl.checked = false;
      const container = stickyEl.closest('.field');
      if (container) {
        container.style.opacity = disabled ? '0.5' : '1';
        container.style.pointerEvents = disabled ? 'none' : '';
        const desc = container.querySelector('.desc');
        if (desc) {
          desc.textContent = disabled ? '需先启用并发请求池' : '启用后优先从粘性池中逐个尝试成功节点，失败即换下一个。粘性池本身始终在工作，此开关只影响优先级的分配。';
        }
      }

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
    updateStickyDisabled();
    parallelEl.addEventListener('change', updateStickyDisabled);
  }

  const probeEnabledEl = $('#set_entry_proxy_probe_enabled');
  const probeIntervalEl = $('#set_entry_proxy_probe_interval_seconds');
  const probeCooldownEl = $('#set_entry_proxy_probe_cooldown_seconds');
  const probeAutoDisableEl = $('#set_entry_proxy_probe_auto_disable_enabled');
  const probeFailureLimitEl = $('#set_entry_proxy_probe_auto_disable_failures');
  if (probeEnabledEl && probeIntervalEl && probeCooldownEl && probeAutoDisableEl && probeFailureLimitEl) {
    const setFieldDisabled = (el, disabled) => {
      el.disabled = disabled;
      const container = el.closest('.field');
      if (container) {
        container.style.opacity = disabled ? '0.5' : '1';
        container.style.pointerEvents = disabled ? 'none' : '';
      }
    };
    const updateProbeDisabled = () => {
      const probeDisabled = !probeEnabledEl.checked;
      setFieldDisabled(probeIntervalEl, probeDisabled);
      setFieldDisabled(probeCooldownEl, probeDisabled);
      setFieldDisabled(probeAutoDisableEl, probeDisabled);
      setFieldDisabled(probeFailureLimitEl, probeDisabled || !probeAutoDisableEl.checked);
    };
    updateProbeDisabled();
    probeEnabledEl.addEventListener('change', updateProbeDisabled);
    probeAutoDisableEl.addEventListener('change', updateProbeDisabled);
  }
}

async function saveSettings() {
  const out = {};
  for (const f of SETTINGS_FIELDS) {
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
    out['sticky_node_priority'] = false;
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
