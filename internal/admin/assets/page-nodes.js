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
  else if (action === 'disable-node') disableNode(uri);
  else if (action === 'add-entry-proxy') addNodeToEntryProxies(uri);
  else if (action === 'remove-entry-proxy') removeNodeFromEntryProxies(uri);
});

var curNodePage = 1;
var nodePageSize = 50;
var totalNodePages = 1;
var cachedNodesList = [];
var curProxyPage = 1;
var proxyPageSize = 10;
var totalProxyPages = 1;
var cachedEntryProxyURIs = new Set();
var cachedProxyCandidates = [];
window.selectedNodeURIs = window.selectedNodeURIs || new Set();
window.selectedProxyURIs = window.selectedProxyURIs || new Set();
var proxyTestProgressTimer = null;
var proxySortMode = '';
var testProgressTimer = null;

function entryProxyIdentity(rawURI) {
  var value = (rawURI || '').trim();
  var separator = value.indexOf('://');
  if (separator < 0) return value.split('#')[0];
  var scheme = value.slice(0, separator).toLowerCase();
  var remainder = value.slice(separator + 3).split('#')[0];
  if (scheme === 'vmess' || scheme === 'clash' || scheme === 'ssr' || scheme === 'shadowsocksr') {
    return scheme + '://' + remainder;
  }
  var pathIndex = remainder.search(/[/?]/);
  var authority = pathIndex < 0 ? remainder : remainder.slice(0, pathIndex);
  var suffix = pathIndex < 0 ? '' : remainder.slice(pathIndex);
  var userInfoEnd = authority.lastIndexOf('@') + 1;
  var userInfo = authority.slice(0, userInfoEnd);
  var host = authority.slice(userInfoEnd).toLowerCase();
  return scheme + '://' + userInfo + host + suffix;
}

function changeNodePage(p) {
  if (p < 1) p = 1;
  if (p > totalNodePages) p = totalNodePages;
  curNodePage = p;
  loadNodes();
}

function changeProxyPage(p) {
  if (p < 1) p = 1;
  if (p > totalProxyPages) p = totalProxyPages;
  curProxyPage = p;
  loadProxyNodes();
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
  var fallbackProxyCandidates = [];
  try {
    const sd = await API.settings.get();
    if (typeof curSettings !== 'undefined') {
      curSettings = sd.settings || sd;
    }
    const gpEl = document.getElementById('globalProxy');
    if (gpEl && (sd.settings || sd).proxy_url !== undefined) {
      gpEl.value = (sd.settings || sd).proxy_url;
    }
    var settings = sd.settings || sd;
    fallbackProxyCandidates = settings.proxy_url_candidates || [];
    cachedProxyCandidates = fallbackProxyCandidates;
    cachedEntryProxyURIs = new Set(fallbackProxyCandidates.map(function (candidate) { return entryProxyIdentity(candidate.raw_uri); }));
  } catch (e) { }

  await loadProxyNodes(fallbackProxyCandidates);

  try {
    var proxyProgress = await API.proxyNodes.testProgress();
    if (proxyProgress && proxyProgress.running) {
      showProxyTestProgress(proxyProgress);
      startProxyTestProgressPolling();
    }
  } catch (e) { }

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
      actionTd.className = 'node-actions-cell';
      var actionWrap = document.createElement('div');
      actionWrap.className = 'node-actions';
      var testBtn = document.createElement('button');
      testBtn.className = 'btn ghost';
      testBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
      testBtn.dataset.action = 'test-node';
      testBtn.dataset.uri = n.raw_uri;
      testBtn.textContent = '\u6D4B\u8BD5';
      actionWrap.appendChild(testBtn);
      if (n.disabled) {
        var enableBtn = document.createElement('button');
        enableBtn.className = 'btn ghost';
        enableBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;color:var(--green);';
        enableBtn.dataset.action = 'enable-node';
        enableBtn.dataset.uri = n.raw_uri;
        enableBtn.textContent = '\u542F\u7528';
        actionWrap.appendChild(enableBtn);
      } else {
        var disableBtn = document.createElement('button');
        disableBtn.className = 'btn ghost';
        disableBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;color:var(--red);';
        disableBtn.dataset.action = 'disable-node';
        disableBtn.dataset.uri = n.raw_uri;
        disableBtn.textContent = '\u7981\u7528';
        actionWrap.appendChild(disableBtn);
      }
      if (isLocked) {
        var unuseBtn = document.createElement('button');
        unuseBtn.className = 'btn ghost';
        unuseBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;color:var(--gold);';
        unuseBtn.dataset.action = 'unuse-node';
        unuseBtn.dataset.uri = n.raw_uri;
        unuseBtn.textContent = '取消锁定';
        actionWrap.appendChild(unuseBtn);
      } else {
        var useBtn = document.createElement('button');
        useBtn.className = 'btn ghost';
        useBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
        useBtn.dataset.action = 'use-node';
        useBtn.dataset.uri = n.raw_uri;
        useBtn.textContent = '\u9501\u5B9A\u4F7F\u7528';
        actionWrap.appendChild(useBtn);
      }
      var entryBtn = document.createElement('button');
      var alreadyEntry = cachedEntryProxyURIs.has(entryProxyIdentity(n.raw_uri));
      entryBtn.className = 'btn ghost';
      entryBtn.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
      entryBtn.dataset.action = alreadyEntry ? 'remove-entry-proxy' : 'add-entry-proxy';
      entryBtn.dataset.uri = n.raw_uri;
      entryBtn.textContent = alreadyEntry ? '移出入口池' : '添加至全局入口代理';
      actionWrap.appendChild(entryBtn);
      var delBtn = document.createElement('button');
      delBtn.className = 'btn danger';
      delBtn.style.cssText = 'padding:4px 10px;font-size:12px;';
      delBtn.dataset.action = 'delete-node';
      delBtn.dataset.uri = n.raw_uri;
      delBtn.textContent = '\u5220\u9664';
      actionWrap.appendChild(delBtn);
      actionTd.appendChild(actionWrap);
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

async function addAndFetchSub() {
  const u = $('#subUrl').value.trim();
  if (!u) return toast('请填订阅 URL');
  toast('正在拉取...');
  try {
    const res = await API.subscriptions.fetch(u);
    $('#subUrl').value = '';
    await loadNodes();
    toast('拉取成功，导入了 ' + (res.count || 0) + ' 个节点');
  } catch (e) {
    toast('拉取失败: ' + e.message);
  }
}

async function testAllNodes() {
  const d = await API.nodes.list();
  const nodes = d.nodes || [];
  if (!nodes.length) return toast('无可测试节点');

  const enabled = nodes.filter(function (n) { return !n.disabled; });
  if (!enabled.length) return toast('没有已启用的节点可测试');

  toast('后台全量测速任务已提交启动...');
  await API.nodes.testAll();
  startTestProgressPolling();
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

async function terminateTestAll() {
  try {
    await API.nodes.testTerminate();
    if (testProgressTimer) {
      clearInterval(testProgressTimer);
      testProgressTimer = null;
    }
    currentTestPaused = false;
    const progressEl = document.getElementById('testProgress');
    if (progressEl) progressEl.style.display = 'none';
    loadNodes();
    toast('正在终止批量测速...');
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

function showNodeDedupConfirm(preview) {
  const modal = document.getElementById('nodeDedupModal');
  const text = document.getElementById('nodeDedupModalText');
  const okButton = document.getElementById('nodeDedupOkBtn');
  const cancelButton = document.getElementById('nodeDedupCancelBtn');
  if (!modal || !text || !okButton || !cancelButton) return;
  const previousFocus = document.activeElement;

  text.textContent = `发现 ${preview.groups} 组重复节点，可合并 ${preview.duplicate_count} 个节点。(仅显示名称不同、连接参数完全一致的节点会被合并。)`;
  modal.classList.remove('hidden');
  cancelButton.focus();
  const cleanup = () => {
    modal.classList.add('hidden');
    okButton.onclick = null;
    cancelButton.onclick = null;
    if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
  };
  cancelButton.onclick = cleanup;
  okButton.onclick = async () => {
    cleanup();
    try {
      const result = await API.nodes.dedup();
      await loadNodes();
      toast(`去重完成，已合并 ${result.removed_count || 0} 个节点`);
    } catch (error) {
      toast('去重失败: ' + error, 'err');
    }
  };
}

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return;
  const modal = document.getElementById('nodeDedupModal');
  if (modal && !modal.classList.contains('hidden')) {
    document.getElementById('nodeDedupCancelBtn').click();
  }
});

async function dedupNodes() {
  try {
    const preview = await API.nodes.dedupPreview();
    if (!preview || preview.duplicate_count === 0) {
      toast('未发现可合并的重复节点');
      return;
    }
    showNodeDedupConfirm(preview);
  } catch (error) {
    toast('去重预览失败: ' + error, 'err');
  }
}
async function deleteDisabledNodes() { await API.nodes.deleteDisabled(); loadNodes(); toast('清理完成'); }
async function sortNodesByLatency() { await API.nodes.sort(false); await loadNodes(); toast('已按延迟顺序重排节点'); }
async function sortNodesByLatencyDesc() { await API.nodes.sort(true); await loadNodes(); toast('已按延迟降序重排节点'); }

async function exportNodes() {
  try {
    const d = await API.nodes.list();
    const nodes = d.nodes || [];
    if (nodes.length === 0) {
      toast('没有可导出的节点');
      return;
    }
    const text = nodes.map(n => n.raw_uri).join('\n');
    const blob = new Blob([text], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'nodes.txt';
    a.click();
    URL.revokeObjectURL(url);
    toast('已导出 ' + nodes.length + ' 个节点');
  } catch (e) {
    toast('导出失败: ' + e.message);
  }
}

async function testSingleNode(uri) {
  toast('正在测试节点...');
  try {
    const result = await API.nodes.test(uri, { auto_disable: true });
    const msg = result.ok
      ? '测试通过 ' + Math.round(result.elapsed_ms) + 'ms'
      : '测试失败 ' + (result.error || '');
    toast(msg);
    await loadNodes();
  } catch (e) {
    toast('测试出错: ' + e.message);
  }
}

async function enableNode(uri) {
  await API.nodes.enable(uri);
  await loadNodes();
  toast('已启用该节点');
}

async function disableNode(uri) {
  await API.nodes.batchDisable([uri]);
  await loadNodes();
  toast('已禁用该节点');
}

async function useNode(uri) { await API.useNode(uri); loadSettings(); loadNodes(); toast('已锁定使用该节点，并关闭并发池'); }
async function unuseNode(uri) { await API.useNode(''); loadSettings(); loadNodes(); toast('已取消锁定，并恢复并发池'); }
async function delNode(uri) { if (!confirm('删除该节点？')) return; await API.nodes.delete(uri); loadNodes(); toast('已删除'); }

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

async function batchEnableSelectedNodes() {
  const uris = getSelectedNodeURIs();
  if (!uris.length) return toast('请先勾选需要批量操作的节点');
  toast('批量启用中...');
  try {
    await API.nodes.batchEnable(uris);
    window.selectedNodeURIs.clear();
    await loadNodes();
    toast('已成功启用 ' + uris.length + ' 个节点');
  } catch (e) { toast('操作失败: ' + e.message); }
}

async function batchDisableSelectedNodes() {
  const uris = getSelectedNodeURIs();
  if (!uris.length) return toast('请先勾选需要批量操作的节点');
  toast('批量禁用中...');
  try {
    await API.nodes.batchDisable(uris);
    window.selectedNodeURIs.clear();
    await loadNodes();
    toast('已成功禁用 ' + uris.length + ' 个节点');
  } catch (e) { toast('操作失败: ' + e.message); }
}

async function batchDeleteSelectedNodes() {
  const uris = getSelectedNodeURIs();
  if (!uris.length) return toast('请先勾选需要批量操作的节点');
  if (!confirm('确定要批量删除选中 ' + uris.length + ' 个节点吗？')) return;
  toast('批量删除中...');
  try {
    await API.nodes.batchDelete(uris);
    window.selectedNodeURIs.clear();
    await loadNodes();
    toast('已成功删除 ' + uris.length + ' 个节点');
  } catch (e) { toast('操作失败: ' + e.message); }
}

async function addNodeToEntryProxies(uri) {
  try {
    var result = await API.proxyNodes.importBatch([uri]);
    var invalid = result.invalid || [];
    if (invalid.length) {
      return toast('加入入口代理失败：节点配置无效');
    }
    await loadNodes();
    if ((result.already_present || []).length) {
      toast('该节点已在全局入口代理池中');
    } else {
      toast('已加入全局入口代理池');
    }
  } catch (e) {
    toast('加入入口代理失败：' + e.message);
  }
}

async function removeNodeFromEntryProxies(uri) {
  try {
    await API.proxyNodes.delete(uri);
    await loadNodes();
    toast('已移出全局入口代理池');
  } catch (e) {
    toast('移出入口代理失败：' + e.message);
  }
}

async function addSelectedNodesToEntryProxies() {
  var uris = getSelectedNodeURIs();
  if (!uris.length) return toast('请先勾选需要加入入口代理池的节点');
  toast('正在加入全局入口代理池...');
  try {
    var result = await API.proxyNodes.importBatch(uris);
    var added = result.added || [];
    var existing = result.already_present || [];
    var invalid = result.invalid || [];
    added.forEach(function (candidate) { window.selectedNodeURIs.delete(candidate.raw_uri); });
    existing.forEach(function (uri) { window.selectedNodeURIs.delete(uri); });
    await loadNodes();
    var message = '已新增 ' + added.length + ' 个，已存在 ' + existing.length + ' 个';
    if (invalid.length) message += '，失败 ' + invalid.length + ' 个（仍保持选中）';
    toast(message);
  } catch (e) {
    toast('批量加入入口代理失败：' + e.message);
  }
}

async function removeSelectedNodesFromEntryProxies() {
  var uris = getSelectedNodeURIs();
  if (!uris.length) return toast('请先勾选需要移出入口代理池的节点');
  if (!confirm('确定要将选中 ' + uris.length + ' 个节点移出全局入口代理池吗？')) return;
  toast('正在移出全局入口代理池...');
  try {
    await API.proxyNodes.deleteBatch(uris);
    window.selectedNodeURIs.clear();
    await loadNodes();
    toast('已移出 ' + uris.length + ' 个节点');
  } catch (e) {
    toast('移出入口代理失败：' + e.message);
  }
}

function importFileNodes(replace) {
  const fileInput = document.getElementById('nodeImportFile');
  if (!fileInput.files.length) return toast('请先选择一个节点配置文件');
  const file = fileInput.files[0];
  const reader = new FileReader();
  toast('正在读取配置文件并解析...');
  reader.onload = async function (e) {
    const text = e.target.result;
    try {
      const res = await API.nodes.import(text, replace);
      await loadNodes();
      fileInput.value = '';
      toast(replace ? '替换成功，导入了 ' + res.count + ' 个节点' : '导入成功，追加了 ' + res.count + ' 个节点');
    } catch (err) {
      toast('文件导入解析失败: ' + err.message);
    }
  };
  reader.readAsText(file);
}

function importJsonNodes(replace) {
  const fileInput = document.getElementById('nodeJsonImportFile');
  if (!fileInput.files.length) return toast('请先选择一个 nodes.json 配置文件');
  const file = fileInput.files[0];
  const reader = new FileReader();
  toast('正在读取配置文件并解析...');
  reader.onload = async function (e) {
    const text = e.target.result;
    try {
      const res = await API.nodes.importJson(text, replace);
      await loadNodes();
      fileInput.value = '';
      toast(replace ? '替换成功，导入了 ' + res.count + ' 个节点' : '导入成功，追加了 ' + res.count + ' 个节点');
    } catch (err) {
      toast('nodes.json 导入解析失败: ' + err.message);
    }
  };
  reader.readAsText(file);
}

async function importProxyNode() {
  var input = document.getElementById('globalProxy');
  var uri = input.value.trim();
  if (!uri) return toast('请先输入代理 URI');
  try {
    var result = await API.proxyNodes.import(uri);
    input.value = '';
    await loadNodes();
    toast('已导入口代理候选：' + (result.candidate.name || uri));
  } catch (e) {
    toast('导入失败：' + e.message);
  }
}

async function testProxyNode(uri) {
  toast('正在隔离测试入口代理...');
  try {
    var result = await API.proxyNodes.test(uri);
    await loadNodes();
    toast(result.ok ? ('测试通过 ' + Math.round(result.elapsed_ms) + 'ms') : ('测试失败：' + (result.error || '未知错误')));
  } catch (e) {
    toast('测试出错：' + e.message);
  }
}

async function enableProxyNode(uri) {
  try {
    await API.proxyNodes.enable(uri);
    await loadNodes();
    toast('已启用该入口代理');
  } catch (e) {
    toast('启用失败：' + e.message);
  }
}

async function disableProxyNode(uri) {
  try {
    await API.proxyNodes.disable(uri);
    await loadNodes();
    toast('已停用入口代理');
  } catch (e) {
    toast('停用失败：' + e.message);
  }
}

async function deleteProxyNode(uri) {
  if (!confirm('确定删除该入口代理候选？')) return;
  try {
    await API.proxyNodes.delete(uri);
    await loadNodes();
    toast('已删除入口代理候选');
  } catch (e) {
    toast('删除失败：' + e.message);
  }
}

async function deleteDisabledProxyNodes() {
  var disabledCount = cachedProxyCandidates.filter(function (candidate) { return candidate.disabled; }).length;
  if (!disabledCount) return toast('没有已禁用的入口代理');
  if (!confirm('确定删除全部 ' + disabledCount + ' 个已禁用入口代理？')) return;
  try {
    var result = await API.proxyNodes.deleteDisabled();
    await loadNodes();
    toast('已删除 ' + (result.deleted_count || 0) + ' 个禁用入口代理');
  } catch (e) {
    toast('删除禁用入口代理失败：' + e.message);
  }
}

function proxyActionButton(label, className, handler) {
  var button = document.createElement('button');
  button.className = className;
  button.style.cssText = 'padding:4px 10px;font-size:12px;margin-right:4px;';
  button.textContent = label;
  button.onclick = handler;
  return button;
}

function getSelectedProxyURIs() {
  return Array.from(window.selectedProxyURIs);
}

function proxyLatencyValue(candidate) {
  return candidate.last_test_ms > 0 ? candidate.last_test_ms : Number.POSITIVE_INFINITY;
}

async function sortProxyCandidates(desc) {
  proxySortMode = desc ? 'desc' : 'asc';
  cachedProxyCandidates.sort(function (a, b) {
    if (!!a.disabled !== !!b.disabled) return a.disabled ? 1 : -1;
    var left = proxyLatencyValue(a);
    var right = proxyLatencyValue(b);
    if (left === right) return (a.name || '').localeCompare(b.name || '');
    return desc ? right - left : left - right;
  });
  curProxyPage = 1;
  await loadProxyNodes(cachedProxyCandidates);
  toast(desc ? '已按延迟降序重排入口代理' : '已按延迟顺序重排入口代理');
}

function sortProxyNodesByLatency() { sortProxyCandidates(false); }
function sortProxyNodesByLatencyDesc() { sortProxyCandidates(true); }

function toggleSelectAllProxyNodes() {
  if (window.selectedProxyURIs.size === cachedProxyCandidates.length && cachedProxyCandidates.length > 0) {
    window.selectedProxyURIs.clear();
  } else {
    cachedProxyCandidates.forEach(function (candidate) { window.selectedProxyURIs.add(candidate.raw_uri); });
  }
  loadProxyNodes(cachedProxyCandidates);
}

function toggleSelectAllProxyNodesCheckbox(mainCb) {
  var start = (curProxyPage - 1) * proxyPageSize;
  var page = cachedProxyCandidates.slice(start, start + proxyPageSize);
  page.forEach(function (candidate) {
    if (mainCb.checked) window.selectedProxyURIs.add(candidate.raw_uri);
    else window.selectedProxyURIs.delete(candidate.raw_uri);
  });
  loadProxyNodes(cachedProxyCandidates);
}

async function batchTestSelectedProxyNodes() {
  var uris = getSelectedProxyURIs();
  if (!uris.length) return toast('请先勾选需要批量测试的入口代理');
  toast('后台批量测速任务已启动...');
  await API.proxyNodes.testBatch(uris);
  window.selectedProxyURIs.clear();
  startProxyTestProgressPolling();
}

function showProxyTestProgress(progressData) {
  var progress = document.getElementById('proxyTestProgress');
  var progressText = document.getElementById('proxyTestProgressText');
  var progressFill = document.getElementById('proxyTestProgressFill');
  var progressDetail = document.getElementById('proxyTestProgressDetail');
  var done = progressData.done || 0;
  var total = progressData.total || 1;
  if (progress) progress.style.display = 'block';
  if (progressText) progressText.textContent = '测试中 ' + done + '/' + total + ' · 通过 ' + (progressData.ok_count || 0) + ' · 失败 ' + (progressData.fail_count || 0);
  if (progressFill) progressFill.style.width = Math.round(done / total * 100) + '%';
  if (progressDetail) progressDetail.textContent = progressData.running ? ('当前: ' + (progressData.current_node || '')) : '测试完成';
}

function startProxyTestProgressPolling() {
  if (proxyTestProgressTimer) return;
  proxyTestProgressTimer = setInterval(async function () {
    try {
      var progressData = await API.proxyNodes.testProgress();
      showProxyTestProgress(progressData);
      if (!progressData.running) {
        clearInterval(proxyTestProgressTimer);
        proxyTestProgressTimer = null;
        await loadNodes();
        toast('入口代理批量测试完成');
      }
    } catch (e) {
      clearInterval(proxyTestProgressTimer);
      proxyTestProgressTimer = null;
    }
  }, 800);
}

async function batchEnableSelectedProxyNodes() {
  var uris = getSelectedProxyURIs();
  if (!uris.length) return toast('请先勾选需要批量启用的入口代理');
  await API.proxyNodes.enableBatch(uris);
  window.selectedProxyURIs.clear();
  await loadNodes();
  toast('已启用 ' + uris.length + ' 个入口代理');
}

async function batchDisableSelectedProxyNodes() {
  var uris = getSelectedProxyURIs();
  if (!uris.length) return toast('请先勾选需要批量禁用的入口代理');
  await API.proxyNodes.disableBatch(uris);
  window.selectedProxyURIs.clear();
  await loadNodes();
  toast('已禁用 ' + uris.length + ' 个入口代理');
}

async function batchDeleteSelectedProxyNodes() {
  var uris = getSelectedProxyURIs();
  if (!uris.length) return toast('请先勾选需要批量删除的入口代理');
  if (!confirm('确定要批量删除选中 ' + uris.length + ' 个入口代理吗？')) return;
  await API.proxyNodes.deleteBatch(uris);
  window.selectedProxyURIs.clear();
  await loadNodes();
  toast('已删除 ' + uris.length + ' 个入口代理');
}

async function loadProxyNodes(fallbackCandidates) {
  var candidates = [];
  var total = 0;
  if (proxySortMode && cachedProxyCandidates.length) {
    total = cachedProxyCandidates.length;
    totalProxyPages = Math.max(1, Math.ceil(total / proxyPageSize));
    if (curProxyPage > totalProxyPages) curProxyPage = totalProxyPages;
    var sortedStart = (curProxyPage - 1) * proxyPageSize;
    candidates = cachedProxyCandidates.slice(sortedStart, sortedStart + proxyPageSize);
  } else try {
    var result = await API.proxyNodes.list(curProxyPage, proxyPageSize);
    candidates = result.candidates || [];
    total = result.total || 0;
    totalProxyPages = Math.max(1, result.total_pages || Math.ceil(total / proxyPageSize));
    if (curProxyPage > totalProxyPages) {
      curProxyPage = totalProxyPages;
      return loadProxyNodes(fallbackCandidates);
    }
  } catch (e) {
    var all = Array.isArray(fallbackCandidates) ? fallbackCandidates : cachedProxyCandidates;
    total = all.length;
    totalProxyPages = Math.max(1, Math.ceil(total / proxyPageSize));
    if (curProxyPage > totalProxyPages) curProxyPage = totalProxyPages;
    var start = (curProxyPage - 1) * proxyPageSize;
    candidates = all.slice(start, start + proxyPageSize);
  }
  renderProxyNodes(candidates);
  var startIndex = total ? (curProxyPage - 1) * proxyPageSize + 1 : 0;
  var endIndex = Math.min(curProxyPage * proxyPageSize, total);
  var info = document.getElementById('proxyNodesPaginationInfo');
  if (info) info.textContent = total ? ('显示第 ' + startIndex + ' - ' + endIndex + ' 条，共 ' + total + ' 条') : '共 0 条';
  var display = document.getElementById('proxyNodesPageNumDisplay');
  if (display) display.textContent = curProxyPage + ' / ' + totalProxyPages;
  var first = document.getElementById('btnProxyPageFirst');
  var prev = document.getElementById('btnProxyPagePrev');
  var next = document.getElementById('btnProxyPageNext');
  var last = document.getElementById('btnProxyPageLast');
  if (first) first.disabled = curProxyPage <= 1;
  if (prev) prev.disabled = curProxyPage <= 1;
  if (next) next.disabled = curProxyPage >= totalProxyPages;
  if (last) last.disabled = curProxyPage >= totalProxyPages;
}

function renderProxyNodes(candidates) {
  var tbody = document.getElementById('proxyNodesBody');
  if (!tbody) return;
  var fragment = document.createDocumentFragment();
  if (!candidates.length) {
    var emptyRow = document.createElement('tr');
    var emptyCell = document.createElement('td');
    emptyCell.colSpan = 6;
    emptyCell.style.cssText = 'color:var(--text-dim);text-align:center;';
    emptyCell.textContent = '暂无入口代理候选';
    emptyRow.appendChild(emptyCell);
    fragment.appendChild(emptyRow);
  }
  candidates.forEach(function (candidate) {
    var row = document.createElement('tr');
    var selectCell = document.createElement('td');
    selectCell.className = 'th-center';
    var checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'proxy-select-cb';
    checkbox.checked = window.selectedProxyURIs.has(candidate.raw_uri);
    checkbox.setAttribute('aria-label', '选择入口代理 ' + (candidate.name || candidate.raw_uri));
    checkbox.onchange = function () {
      if (this.checked) window.selectedProxyURIs.add(candidate.raw_uri);
      else window.selectedProxyURIs.delete(candidate.raw_uri);
      updateProxySelectionHeader();
    };
    selectCell.appendChild(checkbox);
    var nameCell = document.createElement('td');
    var nameContainer = document.createElement('div');
    nameContainer.style.cssText = 'display:flex; align-items:center; flex-wrap:wrap; gap:6px; word-break:break-all;';
    var nameSpan = document.createElement('span');
    nameSpan.textContent = candidate.name || candidate.raw_uri;
    nameContainer.appendChild(nameSpan);
    nameCell.appendChild(nameContainer);
    var typeCell = document.createElement('td');
    typeCell.textContent = candidate.type || '-';
    var enabledCell = document.createElement('td');
    var enabledPill = document.createElement('span');
    enabledPill.className = candidate.disabled ? 'pill off' : 'pill on';
    enabledPill.textContent = candidate.disabled ? '禁用' : '启用';
    enabledCell.appendChild(enabledPill);
    var stateCell = document.createElement('td');
    stateCell.style.cssText = 'white-space:normal;overflow-wrap:anywhere;word-break:break-word;max-width:420px;';
    if (!candidate.last_test_at) {
      stateCell.textContent = '未测试';
    } else if (candidate.last_test_ok) {
      stateCell.textContent = '通过 · ' + Math.round(candidate.last_test_ms) + 'ms';
    } else {
      stateCell.textContent = '失败 · ' + (candidate.last_test_error || '未知错误');
    }
    var actionCell = document.createElement('td');
    actionCell.style.cssText = 'white-space:nowrap;';
    actionCell.appendChild(proxyActionButton('测试', 'btn ghost', function () { testProxyNode(candidate.raw_uri); }));
    if (candidate.disabled) {
      actionCell.appendChild(proxyActionButton('启用', 'btn ghost', function () { enableProxyNode(candidate.raw_uri); }));
    } else {
      actionCell.appendChild(proxyActionButton('禁用', 'btn ghost', function () { disableProxyNode(candidate.raw_uri); }));
    }
    actionCell.appendChild(proxyActionButton('删除', 'btn danger', function () { deleteProxyNode(candidate.raw_uri); }));
    row.appendChild(selectCell);
    row.appendChild(nameCell);
    row.appendChild(typeCell);
    row.appendChild(enabledCell);
    row.appendChild(stateCell);
    row.appendChild(actionCell);
    fragment.appendChild(row);
  });
  tbody.textContent = '';
  tbody.appendChild(fragment);
  updateProxySelectionHeader();
}

function updateProxySelectionHeader() {
  var checkbox = document.getElementById('selectAllProxyNodesCheckbox');
  if (!checkbox) return;
  var start = (curProxyPage - 1) * proxyPageSize;
  var page = cachedProxyCandidates.slice(start, start + proxyPageSize);
  checkbox.checked = page.length > 0 && page.every(function (candidate) {
    return window.selectedProxyURIs.has(candidate.raw_uri);
  });
}
