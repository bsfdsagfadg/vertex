let analyticsState = { page: 1, page_size: 20, status: 'all', key_name: 'all', model: 'all', range: 'all' };
async function loadAnalytics(reset) {
  if (reset) analyticsState.page = 1;
  analyticsState.status = ($('#analyticsStatus') || {}).value || analyticsState.status;
  analyticsState.key_name = ($('#analyticsKey') || {}).value || analyticsState.key_name;
  analyticsState.model = ($('#analyticsModel') || {}).value || analyticsState.model;
  analyticsState.range = ($('#analyticsRange') || {}).value || analyticsState.range;
  try {
    const res = await API.analytics.query(analyticsState); const s = res.stats || {};
    $('#analyticsStats').innerHTML = `<div class="card glass stat"><div class="label">总请求</div><div class="value">${s.total_requests || 0}</div></div><div class="card glass stat"><div class="label">成功</div><div class="value">${s.success_requests || 0}</div></div><div class="card glass stat"><div class="label">Token</div><div class="value">${s.total_tokens || 0}</div></div><div class="card glass stat"><div class="label">平均耗时</div><div class="value">${Math.round(s.avg_duration_ms || 0)}ms</div></div>`;
    $('#analyticsBody').innerHTML = (res.items || []).map(i => `<tr><td>${new Date(i.created_at * 1000).toLocaleString('zh-CN')}</td><td>${esc(i.model)}</td><td>${i.is_stream ? '流式' : '非流式'}</td><td>${i.status_code}</td><td>${i.total_duration_ms}ms</td><td>${i.total_tokens || 0}</td></tr>`).join('');
    updateAnalyticsOptions('analyticsKey', res.available_keys || [], '全部密钥');
    updateAnalyticsOptions('analyticsModel', res.available_models || [], '全部模型');
    const page = Number(res.page || analyticsState.page), size = Number(res.page_size || analyticsState.page_size), total = Number(res.total || 0);
    $('#analyticsPageInfo').textContent = `第 ${page} 页 / 共 ${Math.max(1, Math.ceil(total / size))} 页`;
    $('#analyticsPrev').disabled = page <= 1;
    $('#analyticsNext').disabled = page * size >= total;
  } catch (e) { toast('加载调用统计失败: ' + e.message); }
}
function updateAnalyticsOptions(id, values, label) {
  const el = $('#' + id); if (!el) return;
  const selected = el.value || 'all';
  el.innerHTML = `<option value="all">${label}</option>` + values.map(v => `<option value="${esc(v)}">${esc(v)}</option>`).join('');
  el.value = values.includes(selected) ? selected : 'all';
}
function changeAnalyticsPage(delta) { analyticsState.page = Math.max(1, analyticsState.page + delta); loadAnalytics(false); }
async function clearAnalytics() { if (!confirm('确定清空调用统计？')) return; try { await API.analytics.clear(); await loadAnalytics(true); } catch (e) { toast('清空调用统计失败: ' + e.message); } }
