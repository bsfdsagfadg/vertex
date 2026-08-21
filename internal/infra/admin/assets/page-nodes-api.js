// page-nodes-api.js — 节点/外观 后台 API 通信（数据操作）
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
  if (testProgressTimer) return toast('已有批量测试正在进行中');
  const d = await API.nodes.list();
  const nodes = d.nodes || [];
  if (!nodes.length) return toast('无可测试节点');

  const enabled = nodes.filter(function (n) { return !n.disabled; });
  if (!enabled.length) return toast('没有已启用的节点可测试');

  toast('后台全量测速任务已提交启动...');
  await API.nodes.testAll();
  startTestProgressPolling();
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
async function dedupNodes() { await API.nodes.dedup(); loadNodes(); toast('去重完成'); }
async function deleteDisabledNodes() { await API.nodes.deleteDisabled(); loadNodes(); toast('清理完成'); }
async function sortNodesByLatencyToggle() {
  const desc = window.nodeSortDesc;
  await API.nodes.sort(desc);
  window.nodeSortDesc = !desc;
  localStorage.setItem('vproxy_node_sort_desc', window.nodeSortDesc ? '1' : '0');
  updateSortBtnLabel();
  await loadNodes();
  toast(desc ? '已按延迟降序重排节点' : '已按延迟升序重排节点');
}
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
async function useNode(uri) { await API.useNode(uri); loadSettings(); loadNodes(); toast('已锁定使用该节点，并关闭并发池'); }
async function unuseNode(uri) { await API.useNode(''); loadSettings(); loadNodes(); toast('已取消锁定，并恢复并发池'); }
async function delNode(uri) { if (!confirm('删除该节点？')) return; await API.nodes.delete(uri); loadNodes(); toast('已删除'); }
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
// ─── 前置代理池 ───
async function importProxyNode() {
  const uri = $('#globalProxy').value.trim();
  if (!uri) return toast('请先输入代理 URI');
  try {
    await API.proxyNodes.import(uri);
    $('#globalProxy').value = '';
    await loadNodes();
    toast('已导入前置代理并加入轮询池');
  } catch (e) {
    toast('导入失败: ' + e.message);
  }
}
async function testProxyNode(uri) {
  toast('正在测试前置代理...');
  try {
    const res = await API.proxyNodes.test(uri);
    const msg = res.ok
      ? '测试通过 ' + Math.round(res.elapsed_ms) + 'ms'
      : '测试失败 ' + (res.error || '');
    toast(msg);
    await loadNodes();
  } catch (e) {
    toast('测试出错: ' + e.message);
  }
}
async function testAllProxyNodes() {
  const nodes = window.lastProxyNodes || [];
  if (!nodes.length) return toast('前置代理池为空，无可测试节点');
  toast('开始批量测试 ' + nodes.length + ' 个前置代理...');
  let passCount = 0;
  let doneCount = 0;
  const batchSize = 5;
  for (let i = 0; i < nodes.length; i += batchSize) {
    const chunk = nodes.slice(i, i + batchSize);
    await Promise.all(chunk.map(async (n) => {
      try {
        const res = await API.proxyNodes.test(n.raw_uri);
        if (res && res.ok) passCount++;
      } catch (e) {}
      doneCount++;
    }));
    toast('前置代理测试中: ' + doneCount + '/' + nodes.length + '...');
  }
  await loadNodes();
  toast('前置代理测试完成: ' + passCount + '/' + nodes.length + ' 通过');
}
async function toggleProxyNode(uri, disabled) {
  try {
    await API.proxyNodes.toggle([uri], disabled);
    await loadNodes();
    toast(disabled ? '已禁用该前置代理' : '已启用该前置代理');
  } catch (e) {
    toast('操作失败: ' + e.message);
  }
}
async function batchToggleProxyNodes(uris, disabled) {
  if (!uris || !uris.length) return toast('请先选择要操作的前置节点');
  try {
    await API.proxyNodes.toggle(uris, disabled);
    await loadNodes();
    toast(disabled ? '已批量禁用 ' + uris.length + ' 个前置节点' : '已批量启用 ' + uris.length + ' 个前置节点');
  } catch (e) {
    toast('批量操作失败: ' + e.message);
  }
}
async function deleteProxyNode(uri) {
  if (!confirm('确定删除该前置代理节点？')) return;
  try {
    await API.proxyNodes.batchDelete([uri]);
    await loadNodes();
    toast('已删除');
  } catch (e) {
    toast('删除失败: ' + e.message);
  }
}
async function batchDeleteProxyNodes(uris) {
  if (!uris || !uris.length) return toast('请选择要删除的前置节点');
  if (!confirm('确定删除所选 ' + uris.length + ' 个前置代理节点？')) return;
  try {
    await API.proxyNodes.batchDelete(uris);
    await loadNodes();
    toast('已删除 ' + uris.length + ' 个前置节点');
  } catch (e) {
    toast('批量删除失败: ' + e.message);
  }
}
async function deleteDisabledProxyNodes() {
  if (!confirm('确定清空所有已禁用的前置代理节点？')) return;
  try {
    const res = await API.proxyNodes.deleteDisabled();
    await loadNodes();
    toast('已清空 ' + (res.deleted_count || 0) + ' 个禁用节点');
  } catch (e) {
    toast('清理失败: ' + e.message);
  }
}
async function dedupProxyNodes() {
  try {
    const res = await API.proxyNodes.dedup();
    await loadNodes();
    toast('去重完成，移除 ' + (res.removed_count || 0) + ' 个');
  } catch (e) {
    toast('去重失败: ' + e.message);
  }
}
