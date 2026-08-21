let logsRefreshTimer = null;

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
    if (check.checked && !logsRefreshTimer) {
      toggleAutoRefreshLogs(true);
    } else if (!check.checked && logsRefreshTimer) {
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
  // Render UI
  const tbody = $('#logUITbody');
  if (tbody) {
    const lines = content.split('\n').filter(l => l.trim() !== '');
    let html = '';
    
    // Parse standard Go log format: "2026/06/29 01:21:21 [Level] Message"
    // Or our custom format like "[Config] ..."
    lines.forEach(line => {
      let level = 'info';
      let timeStr = '';
      let msg = line;
      
      // Basic heuristic parsing
      const timeMatch = line.match(/^(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2})\s+(.*)/);
      if (timeMatch) {
        timeStr = timeMatch[1];
        msg = timeMatch[2];
      }
      
      if (msg.includes('[Config]') || msg.includes('[Server]')) level = 'info';
      if (msg.includes('警告') || msg.includes('warn') || msg.includes('WARN')) level = 'warn';
      if (msg.includes('失败') || msg.includes('error') || msg.includes('ERROR') || msg.includes('错误')) level = 'error';
      
      let levelClass = '';
      let levelText = 'INFO';
      if (level === 'info') { levelClass = 'log-level-info'; levelText = 'INFO'; }
      if (level === 'warn') { levelClass = 'log-level-warn'; levelText = 'WARN'; }
      if (level === 'error') { levelClass = 'log-level-error'; levelText = 'ERRO'; }
      
      html += `<tr>
        <td>${timeStr}</td>
        <td class="${levelClass}">${levelText}</td>
        <td>${escapeHtml(msg)}</td>
      </tr>`;
    });
    
    tbody.innerHTML = html;
    const uiEl = $('#logContentUI');
    if (uiEl) uiEl.scrollTop = uiEl.scrollHeight;
  }
}

function escapeHtml(str) {
  return str.replace(/[&<>"']/g, function(m) {
    switch (m) {
      case '&': return '&amp;';
      case '<': return '&lt;';
      case '>': return '&gt;';
      case '"': return '&quot;';
      case "'": return '&#39;';
    }
  });
}

function toggleAutoRefreshLogs(silent) {
  const check = $('#autoRefreshLogsCheck');
  if (!check) return;
  if (silent !== true) {
    API.settings.put({ auto_refresh_logs: check.checked }).catch(() => {});
  }
  if (check.checked) {
    if (!logsRefreshTimer) {
      logsRefreshTimer = setInterval(() => {
        const pageLogs = $('#page-logs');
        if (pageLogs && !pageLogs.classList.contains('hidden')) {
          loadLogs();
        }
      }, 3000);
      if (silent !== true) toast('已开启自动刷新日志');
    }
  } else {
    if (logsRefreshTimer) {
      clearInterval(logsRefreshTimer);
      logsRefreshTimer = null;
      if (silent !== true) toast('已关闭自动刷新日志');
    }
  }
}
