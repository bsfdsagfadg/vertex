// page-nodes-ui.js — 页面 DOM 渲染与交互
document.getElementById('nodesBody').addEventListener('click', function (e) {
  var btn = e.target.closest('[data-action]');
  if (!btn) return;
  var uri = btn.dataset.uri;
  var action = btn.dataset.action;
  if (action === 'use-node') useNode(uri);
  else if (action === 'unuse-node') unuseNode(uri);
  else if (action === 'delete-node') delNode(uri);
  else if (action === 'test-node') testSingleNode(uri);
  else if (action === 'enable-node') enableNode(uri);
});

var curNodePage = 1;
var nodePageSize = 50;
var totalNodePages = 1;
var cachedNodesList = [];
window.selectedNodeURIs = window.selectedNodeURIs || new Set();
var testProgressTimer = null;
window.nodeSortDesc = localStorage.getItem('vproxy_node_sort_desc') === '1';

function changeNodePage(p) {
  if (p < 1) p = 1;
  if (p > totalNodePages) p = totalNodePages;
  curNodePage = p;
  loadNodes();
}

function updateSelectHeaderAndBanner() {
  var mainCb = document.getElementById('selectAllNodesCheckbox');
  var banner = document.getElementById('crossPageSelectBanner');
  var bannerText = document.getElementById('crossPageSelectText');
  var bannerTotal = document.getElementById('crossPageSelectTotal');

  if (!cachedNodesList.length) {
    if (mainCb) mainCb.checked = false;
    if (banner) banner.style.display = 'none';
    return;
  }

  var startIdx = (curNodePage - 1) * nodePageSize;
  var endIdx = Math.min(startIdx + nodePageSize, cachedNodesList.length);
  var pageNodes = cachedNodesList.slice(startIdx, endIdx);

  var allPageChecked = pageNodes.length > 0 && pageNodes.every(function (n) { return window.selectedNodeURIs.has(n.raw_uri); });
  if (mainCb) mainCb.checked = allPageChecked;

  if (allPageChecked && cachedNodesList.length > pageNodes.length && window.selectedNodeURIs.size < cachedNodesList.length) {
    if (banner) {
      banner.style.display = 'block';
      if (bannerText) bannerText.textContent = '当前已选择本页 ' + pageNodes.length + ' 个节点。';
      if (bannerTotal) bannerTotal.textContent = cachedNodesList.length;
    }
  } else {
    if (banner) banner.style.display = 'none';
  }
}

function selectAllNodesAcrossPages() {
  cachedNodesList.forEach(function (n) { window.selectedNodeURIs.add(n.raw_uri); });
  var cbs = document.querySelectorAll('.node-select-cb');
  cbs.forEach(function (cb) { cb.checked = true; });
  var banner = document.getElementById('crossPageSelectBanner');
  if (banner) banner.style.display = 'none';
  var mainCb = document.getElementById('selectAllNodesCheckbox');
  if (mainCb) mainCb.checked = true;
  toast('已选择全部 ' + window.selectedNodeURIs.size + ' 个节点');
}

async function loadNodes() {
  try {
    const sd = await API.settings.get();
    if (typeof curSettings !== 'undefined') {
      curSettings = sd.settings || sd;
    }
  } catch (e) { }

  try {
    const pd = await API.proxyNodes.list();
    renderProxyNodes(pd.nodes || [], pd.health || {});
  } catch (e) { }

  updateSortBtnLabel();

  const d = await API.nodes.list();
  const nodes = d.nodes || [];
  cachedNodesList = nodes;

  try {
    const prog = await API.nodes.testProgress();
    if (prog && prog.running) {
      showTestProgressUI(prog);
      startTestProgressPolling();
    } else if (!testProgressTimer) {
      const progressEl = document.getElementById('testProgress');
      if (progressEl) progressEl.style.display = 'none';
    }
  } catch (e) { }

  const enabledCount = nodes.filter(n => !n.disabled).length;
  const disabledCount = nodes.filter(n => n.disabled).length;
  document.getElementById('nodesSummary').textContent = '\u5F53\u524D\u5171 ' + nodes.length + ' \u4E2A\u8282\u70B9\uFF08\u542F\u7528 ' + enabledCount + ' / \u7981\u7528 ' + disabledCount + '\uFF09';

  totalNodePages = Math.max(1, Math.ceil(nodes.length / nodePageSize));
  if (curNodePage > totalNodePages) curNodePage = totalNodePages;

  const startIdx = (curNodePage - 1) * nodePageSize;
  const endIdx = Math.min(startIdx + nodePageSize, nodes.length);
  const pageNodes = nodes.slice(startIdx, endIdx);

  const tbody = document.getElementById('nodesBody');
  const frag = document.createDocumentFragment();

  if (pageNodes.length === 0) {
    var tr = document.createElement('tr');
    var td = document.createElement('td');
    td.colSpan = 5;
    td.style.cssText = 'color:var(--text-dim); text-align:center;';
    td.textContent = '\u6682\u65E0\u8282\u70B9';
    tr.appendChild(td);
    frag.appendChild(tr);
  } else {
    for (var i = 0; i < pageNodes.length; i++) {
      var n = pageNodes[i];
      var tr = document.createElement('tr');

      var cbTd = document.createElement('td');
      cbTd.style.cssText = 'text-align:center;vertical-align:middle;';
      var cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'node-select-cb';
      cb.dataset.uri = n.raw_uri;
      cb.checked = window.selectedNodeURIs.has(n.raw_uri);
      cb.setAttribute('aria-label', '选择节点 ' + n.name);
      cb.onchange = function () {
        if (this.checked) window.selectedNodeURIs.add(this.dataset.uri);
        else window.selectedNodeURIs.delete(this.dataset.uri);
        updateSelectHeaderAndBanner();
      };
      cbTd.appendChild(cb);
      tr.appendChild(cbTd);

      var nameTd = document.createElement('td');
      var nameDiv = document.createElement('div');
      nameDiv.style.cssText = 'font-weight:600;font-size:13.5px;color:var(--text);';
      nameDiv.textContent = n.name;
      var isLocked = n.raw_uri === curSettings.active_node_uri;
      if (isLocked) {
        var badge = document.createElement('span');
        badge.className = 'pill on';
        badge.style.cssText = 'font-size:10px;padding:2px 8px;margin-left:5px;';
        badge.textContent = '\u9501\u5B9A\u4F7F\u7528\u4E2D';
        nameDiv.appendChild(badge);
      } else if (n.disabled) {
        var badge = document.createElement('span');
        badge.className = 'pill off';
        badge.style.cssText = 'font-size:10px;padding:2px 8px;margin-left:5px;background:rgba(236,138,124,0.16);color:var(--red);';
        badge.textContent = '\u5DF2\u7981\u7528';
        nameDiv.appendChild(badge);
      } else {
        var badge = document.createElement('span');
        badge.className = 'pill off';
        badge.style.cssText = 'font-size:10px;padding:2px 8px;margin-left:5px;background:rgba(143,208,232,0.15);color:var(--blue);';
        badge.textContent = '\u5019\u9009';
        nameDiv.appendChild(badge);
      }
      nameTd.appendChild(nameDiv);

      var serverInfo = '';
      try {
        if (n.raw_uri.startsWith('vmess://')) {
          var b64Str = n.raw_uri.slice(8).split(/[?#]/)[0];
          var b = atob(b64Str.replace(/-/g, '+').replace(/_/g, '/'));
          var info = JSON.parse(b);
          serverInfo = (info.add || 'unknown') + ':' + (info.port || '');
        } else {
          var urlObj = new URL(n.raw_uri);
          serverInfo = urlObj.hostname + ':' + (urlObj.port || '443');
        }
      } catch (e) {
        serverInfo = '\u914D\u7F6E\u683C\u5F0F\u590D\u6742';
      }

      var addrDiv = document.createElement('div');
      addrDiv.style.cssText = 'font-size:11px;color:var(--text-dim);margin-top:4px;';
      addrDiv.appendChild(document.createTextNode('\u5730\u5740: '));
      var code = document.createElement('code');
      code.style.cssText = 'font-size:11px;background:rgba(0,0,0,0.25);color:var(--blue);padding:1px 4px;border-radius:4px;';
      code.textContent = serverInfo;
      addrDiv.appendChild(code);
      nameTd.appendChild(addrDiv);
      tr.appendChild(nameTd);

      var typeTd = document.createElement('td');
      var typeCode = document.createElement('code');
      typeCode.textContent = _tmap[n.type.toLowerCase()] || n.type.toUpperCase();
      typeTd.appendChild(typeCode);
      tr.appendChild(typeTd);

      var statusTd = document.createElement('td');
      var health = d.health[n.raw_uri];
      if (!health || (!health.last_success_at && !health.last_fail_at)) {
        var pill = document.createElement('span');
        pill.className = 'pill off';
        pill.style.cssText = 'background:rgba(195,182,164,0.15);color:var(--text-dim);';
        pill.textContent = '\u672A\u6D4B\u8BD5';
        statusTd.appendChild(pill);
      } else if (health.consecutive_failures === 0) {
        var ms = health.last_test_ms ? Math.round(health.last_test_ms) + 'ms' : '';
        var pill = document.createElement('span');
        pill.className = 'pill on';
        pill.style.cssText = 'background:rgba(132,214,160,0.16);color:var(--green);margin-right:5px;';
        pill.textContent = '\u6D4B\u8BD5\u901A\u8FC7 ' + ms;
        statusTd.appendChild(pill);
        var avail = document.createElement('span');
        avail.style.cssText = 'color:var(--green);font-weight:600;font-size:11px;';
        avail.textContent = '\u53EF\u7528';
        statusTd.appendChild(avail);
      } else {
        var pill = document.createElement('span');
        pill.className = 'pill off';
        pill.style.cssText = 'background:rgba(236,138,124,0.16);color:var(--red);margin-right:5px;';
        pill.textContent = '\u6D4B\u8BD5\u5931\u8D25';
        statusTd.appendChild(pill);

        if (health.last_test_error) {
          var errSpan = document.createElement('div');
          errSpan.className = 'node-err-msg';
          errSpan.textContent = health.last_test_error;
          statusTd.appendChild(errSpan);
        }
      }
      tr.appendChild(statusTd);

      var actionTd = document.createElement('td');
      actionTd.style.cssText = 'text-align:right;white-space:nowrap;';
      var testBtn = document.createElement('button');
      testBtn.className = 'btn ghost';
      testBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
      testBtn.dataset.action = 'test-node';
      testBtn.dataset.uri = n.raw_uri;
      testBtn.textContent = '\u6D4B\u8BD5';
      actionTd.appendChild(testBtn);
      if (n.disabled) {
        var enableBtn = document.createElement('button');
        enableBtn.className = 'btn ghost';
        enableBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;color:var(--green);';
        enableBtn.dataset.action = 'enable-node';
        enableBtn.dataset.uri = n.raw_uri;
        enableBtn.textContent = '\u542F\u7528';
        actionTd.appendChild(enableBtn);
      }
      if (isLocked) {
        var unuseBtn = document.createElement('button');
        unuseBtn.className = 'btn ghost';
        unuseBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;color:var(--gold);';
        unuseBtn.dataset.action = 'unuse-node';
        unuseBtn.dataset.uri = n.raw_uri;
        unuseBtn.textContent = '取消锁定';
        actionTd.appendChild(unuseBtn);
      } else {
        var useBtn = document.createElement('button');
        useBtn.className = 'btn ghost';
        useBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
        useBtn.dataset.action = 'use-node';
        useBtn.dataset.uri = n.raw_uri;
        useBtn.textContent = '\u9501\u5B9A\u4F7F\u7528';
        actionTd.appendChild(useBtn);
      }
      var delBtn = document.createElement('button');
      delBtn.className = 'btn danger';
      delBtn.style.cssText = 'padding:4px 10px;font-size:12px;';
      delBtn.dataset.action = 'delete-node';
      delBtn.dataset.uri = n.raw_uri;
      delBtn.textContent = '\u5220\u9664';
      actionTd.appendChild(delBtn);
      tr.appendChild(actionTd);
      frag.appendChild(tr);
    }
  }

  tbody.textContent = '';
  tbody.appendChild(frag);

  const pageNumDisplay = document.getElementById('nodesPageNumDisplay');
  if (pageNumDisplay) pageNumDisplay.textContent = curNodePage + ' / ' + totalNodePages;
  const pageInfo = document.getElementById('nodesPaginationInfo');
  if (pageInfo) pageInfo.textContent = nodes.length > 0 ? ('显示第 ' + (startIdx + 1) + ' - ' + endIdx + ' 条，共 ' + nodes.length + ' 条') : '共 0 条';

  const btnFirst = document.getElementById('btnPageFirst');
  const btnPrev = document.getElementById('btnPagePrev');
  const btnNext = document.getElementById('btnPageNext');
  const btnLast = document.getElementById('btnPageLast');
  if (btnFirst) btnFirst.disabled = curNodePage <= 1;
  if (btnPrev) btnPrev.disabled = curNodePage <= 1;
  if (btnNext) btnNext.disabled = curNodePage >= totalNodePages;
  if (btnLast) btnLast.disabled = curNodePage >= totalNodePages;

  updateSelectHeaderAndBanner();
}
let currentTestPaused = false;
function showTestProgressUI(prog) {
  const progressEl = document.getElementById('testProgress');
  const progressText = document.getElementById('testProgressText');
  const progressFill = document.getElementById('testProgressFill');
  const progressDetail = document.getElementById('testProgressDetail');
  const btnPause = document.getElementById('btnTestPauseResume');
  if (!progressEl) return;
  progressEl.style.display = 'block';
  currentTestPaused = !!prog.paused;
  if (btnPause) {
    btnPause.textContent = currentTestPaused ? '恢复' : '暂停';
    btnPause.className = 'btn ghost';
  }
  if (currentTestPaused && testProgressTimer) {
    clearInterval(testProgressTimer);
    testProgressTimer = null;
  }
  const done = prog.done || 0;
  const total = prog.total || 1;
  const ok = prog.ok_count || 0;
  const failed = prog.fail_count || 0;
  progressFill.style.width = Math.round(done / total * 100) + '%';
  const statusStr = currentTestPaused ? '已暂停' : '测试中';
  progressText.textContent = statusStr + ' ' + done + '/' + total + ' \u00B7 \u901A\u8FC7 ' + ok + ' \u00B7 \u5931\u8D25 ' + failed;
  progressDetail.textContent = '当前状态: ' + (prog.current_node || '');
}
async function toggleTestPauseResume() {
  try {
    if (currentTestPaused) {
      await API.nodes.testResume();
      currentTestPaused = false;
      const btnPause = document.getElementById('btnTestPauseResume');
      if (btnPause) {
        btnPause.textContent = '暂停';
        btnPause.className = 'btn ghost';
      }
      const progressText = document.getElementById('testProgressText');
      if (progressText && progressText.textContent.startsWith('已暂停')) {
        progressText.textContent = progressText.textContent.replace(/^已暂停/, '测试中');
      }
      startTestProgressPolling();
      toast('已恢复批量测速');
    } else {
      await API.nodes.testPause();
      currentTestPaused = true;
      if (testProgressTimer) {
        clearInterval(testProgressTimer);
        testProgressTimer = null;
      }
      const btnPause = document.getElementById('btnTestPauseResume');
      if (btnPause) {
        btnPause.textContent = '恢复';
        btnPause.className = 'btn ghost';
      }
      const progressText = document.getElementById('testProgressText');
      if (progressText && progressText.textContent.startsWith('测试中')) {
        progressText.textContent = progressText.textContent.replace(/^测试中/, '已暂停');
      }
      toast('批量测速已暂停');
    }
  } catch (e) {
    toast(e.message || '操作失败');
  }
}
function startTestProgressPolling() {
  if (testProgressTimer) return;
  testProgressTimer = setInterval(async function () {
    try {
      const prog = await API.nodes.testProgress();
      if (prog && prog.running) {
        showTestProgressUI(prog);
      } else {
        clearInterval(testProgressTimer);
        testProgressTimer = null;
        const progressEl = document.getElementById('testProgress');
        if (progressEl) progressEl.style.display = 'none';
        toast('全局批量测速结束！');
        loadNodes();
      }
    } catch (e) { }
  }, 1000);
}
function updateSortBtnLabel() {
  const b = document.getElementById('btnSortByLatency');
  if (b) b.textContent = window.nodeSortDesc ? '按延迟降序排序' : '按延迟升序排序';
}
function getSelectedNodeURIs() {
  return Array.from(window.selectedNodeURIs);
}
function toggleSelectAllNodes() {
  if (window.selectedNodeURIs.size === cachedNodesList.length && cachedNodesList.length > 0) {
    window.selectedNodeURIs.clear();
  } else {
    cachedNodesList.forEach(function (n) { window.selectedNodeURIs.add(n.raw_uri); });
  }
  loadNodes();
}
function toggleSelectAllNodesCheckbox(mainCb) {
  const startIdx = (curNodePage - 1) * nodePageSize;
  const endIdx = Math.min(startIdx + nodePageSize, cachedNodesList.length);
  const pageNodes = cachedNodesList.slice(startIdx, endIdx);

  pageNodes.forEach(function (n) {
    if (mainCb.checked) window.selectedNodeURIs.add(n.raw_uri);
    else window.selectedNodeURIs.delete(n.raw_uri);
  });
  loadNodes();
}
function renderProxyNodes(nodes, health) {
  window.lastProxyNodes = nodes || [];
  const tbody = document.getElementById('proxyNodesBody');
  const frag = document.createDocumentFragment();

  if (!nodes || nodes.length === 0) {
    const tr = document.createElement('tr');
    const td = document.createElement('td');
    td.colSpan = 4;
    td.style.cssText = 'color:var(--text-dim);text-align:center;';
    td.textContent = '暂无前置代理节点（可在此导入节点后自动启用为第一跳 SOCKS5 通道）';
    tr.appendChild(td);
    frag.appendChild(tr);
  } else {
    for (const n of nodes) {
      const h = health[n.raw_uri] || {};
      const tr = document.createElement('tr');

      const nameTd = document.createElement('td');
      const nameDiv = document.createElement('div');
      nameDiv.style.cssText = 'font-weight:600;font-size:13.5px;color:var(--text);';
      nameDiv.textContent = n.name || n.raw_uri;
      if (n.disabled) {
        const badge = document.createElement('span');
        badge.className = 'pill off';
        badge.style.cssText = 'font-size:10px;padding:2px 8px;margin-left:5px;';
        badge.textContent = '已禁用';
        nameDiv.appendChild(badge);
      }
      nameTd.appendChild(nameDiv);
      tr.appendChild(nameTd);

      const typeTd = document.createElement('td');
      const typeCode = document.createElement('code');
      typeCode.textContent = (n.type || '').toUpperCase();
      typeTd.appendChild(typeCode);
      tr.appendChild(typeTd);

      const statusTd = document.createElement('td');
      if (h.cooldown_until > Math.floor(Date.now() / 1000)) {
        const pill = document.createElement('span');
        pill.className = 'pill off';
        pill.style.cssText = 'background:rgba(236,138,124,0.16);color:var(--gold);';
        pill.textContent = '冷却中';
        statusTd.appendChild(pill);
      } else if ((h.last_success_at || 0) > (h.last_fail_at || 0)) {
        const pill = document.createElement('span');
        pill.className = 'pill on';
        pill.style.cssText = 'background:rgba(132,214,160,0.16);color:var(--green);';
        const ms = h.last_test_ms ? Math.round(h.last_test_ms) + 'ms' : '';
        pill.textContent = '测试通过 ' + ms;
        statusTd.appendChild(pill);
      } else if (h.last_fail_at > 0) {
        const pill = document.createElement('span');
        pill.className = 'pill off';
        pill.style.cssText = 'background:rgba(236,138,124,0.16);color:var(--red);margin-right:5px;';
        pill.textContent = '测试失败';
        statusTd.appendChild(pill);
        if (h.last_test_error) {
          const errSpan = document.createElement('div');
          errSpan.style.cssText = 'font-size:11px;color:var(--red);margin-top:2px;';
          errSpan.textContent = h.last_test_error;
          statusTd.appendChild(errSpan);
        }
      } else {
        const pill = document.createElement('span');
        pill.className = 'pill off';
        pill.style.cssText = 'background:rgba(195,182,164,0.15);color:var(--text-dim);';
        pill.textContent = '未测试';
        statusTd.appendChild(pill);
      }
      tr.appendChild(statusTd);

      const actionTd = document.createElement('td');
      actionTd.style.cssText = 'text-align:right;white-space:nowrap;';

      const testBtn = document.createElement('button');
      testBtn.className = 'btn ghost';
      testBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
      testBtn.textContent = '测试';
      testBtn.onclick = function () { testProxyNode(n.raw_uri); };
      actionTd.appendChild(testBtn);

      const toggleBtn = document.createElement('button');
      toggleBtn.className = 'btn ghost';
      toggleBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;' + (n.disabled ? 'color:var(--green);' : 'color:var(--gold);');
      toggleBtn.textContent = n.disabled ? '启用' : '禁用';
      toggleBtn.onclick = function () { toggleProxyNode(n.raw_uri, !n.disabled); };
      actionTd.appendChild(toggleBtn);

      const delBtn = document.createElement('button');
      delBtn.className = 'btn danger';
      delBtn.style.cssText = 'padding:4px 10px;font-size:12px;';
      delBtn.textContent = '删除';
      delBtn.onclick = function () { deleteProxyNode(n.raw_uri); };
      actionTd.appendChild(delBtn);

      tr.appendChild(actionTd);
      frag.appendChild(tr);
    }
  }

  tbody.textContent = '';
  tbody.appendChild(frag);
}
