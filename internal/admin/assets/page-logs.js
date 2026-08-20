// ==========================================
// Vertex AI Proxy Admin - Logs Page
// ==========================================

async function loadLogs() {
  const check = $('#autoRefreshLogsCheck');
  if (check) {
    try {
      const sRes = await API.settings.get();
      const sets = sRes.settings || sRes;
      if (sets && sets.auto_refresh_logs !== undefined) {
        check.checked = !!sets.auto_refresh_logs;
      }
    } catch (e) {}
    if (check.checked && !AppState.timers.logsRefresh) {
      toggleAutoRefreshLogs(true);
    } else if (!check.checked && AppState.timers.logsRefresh) {
      toggleAutoRefreshLogs(true);
    }
  }
  try {
    const res = await fetch('/api/admin/log');
    const data = await res.json();
    if (res.ok && data.ok) {
      renderLogs(data.content || '');
    } else {
      toast('拉取日志失败', true);
    }
  } catch (e) {
    console.error(e);
  }
}

function renderLogs(content) {
  const tbody = $('#logUITbody');
  if (!tbody) return;
  tbody.replaceChildren();

  const lines = content.split('\n').filter(l => l.trim() !== '');

  lines.forEach(line => {
    let level = 'info';
    let timeStr = '';
    let msg = line;

    const timeMatch = line.match(/^(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2})\s+(.*)/);
    if (timeMatch) {
      timeStr = timeMatch[1];
      msg = timeMatch[2];
    }

    if (msg.includes('[Config]') || msg.includes('[Server]')) level = 'info';
    if (msg.includes('警告') || msg.includes('warn') || msg.includes('WARN')) level = 'warn';
    if (msg.includes('失败') || msg.includes('error') || msg.includes('ERROR') || msg.includes('错误')) level = 'error';

    let levelClass = 'log-level-info';
    let levelText = 'INFO';
    if (level === 'warn') {
      levelClass = 'log-level-warn';
      levelText = 'WARN';
    } else if (level === 'error') {
      levelClass = 'log-level-error';
      levelText = 'ERRO';
    }

    const tr = el('tr', {}, [
      el('td', { text: timeStr }),
      el('td', { className: levelClass, text: levelText }),
      el('td', { text: msg }),
    ]);
    tbody.appendChild(tr);
  });

  const uiEl = $('#logContentUI');
  if (uiEl) uiEl.scrollTop = uiEl.scrollHeight;
}

function toggleAutoRefreshLogs(silent) {
  const check = $('#autoRefreshLogsCheck');
  if (!check) return;
  if (silent !== true) {
    API.settings.put({ auto_refresh_logs: check.checked }).catch(() => {});
  }
  if (check.checked) {
    if (!AppState.timers.logsRefresh) {
      const timer = setInterval(() => {
        const pageLogs = $('#page-logs');
        if (pageLogs && !pageLogs.classList.contains('hidden')) {
          loadLogs();
        }
      }, 3000);
      AppState.setTimer('logsRefresh', timer);
      if (silent !== true) toast('已开启自动刷新日志');
    }
  } else {
    AppState.clearTimer('logsRefresh');
    if (silent !== true) toast('已关闭自动刷新日志');
  }
}
