function initSubscriptions() {
  loadSubscriptions();
}

function loadSubscriptions() {
  API.raw('/api/admin/subscriptions/list')
    .then(data => {
      if (!data) return;
      const customUAs = data.custom_uas || [];
      renderCustomUAs(customUAs);
      renderSubscriptions(data.subscriptions || [], customUAs);
      updateUaSelect(customUAs);
    })
    .catch(err => toast('加载订阅配置失败: ' + err, 'err'));
}

function updateUaSelect(customUAs) {
  const select = document.getElementById('subUaSelect');
  if (!select) return;
  select.querySelectorAll('option[data-custom-ua="true"]').forEach(option => option.remove());
  customUAs.forEach(ua => {
    const option = document.createElement('option');
    option.value = 'custom:' + ua.id;
    option.textContent = `${ua.name} (${ua.user_agent})`;
    option.dataset.customUa = 'true';
    select.appendChild(option);
  });
}

function createSubscriptionAction(label, className, onClick) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = className;
  button.textContent = label;
  button.addEventListener('click', onClick);
  return button;
}

function appendSubscriptionCell(row, text, className) {
  const cell = document.createElement('td');
  if (className) cell.className = className;
  cell.textContent = text;
  row.appendChild(cell);
  return cell;
}

function renderCustomUAs(customUAs) {
  const tbody = document.getElementById('customUAsBody');
  if (!tbody) return;
  tbody.replaceChildren();
  if (customUAs.length === 0) {
    const row = document.createElement('tr');
    const cell = appendSubscriptionCell(row, '暂无自定义 UA', 'text-center text-dim');
    cell.colSpan = 3;
    tbody.appendChild(row);
    return;
  }

  customUAs.forEach(ua => {
    const row = document.createElement('tr');
    appendSubscriptionCell(row, ua.name || '');
    appendSubscriptionCell(row, ua.user_agent || '', 'subscription-break-all');
    const actionCell = document.createElement('td');
    actionCell.className = 'subscription-actions-cell';
    const actions = document.createElement('div');
    actions.className = 'subscription-actions';
    actions.appendChild(createSubscriptionAction('编辑', 'btn ghost btn-blue compact-action', () => editUA(ua)));
    actions.appendChild(createSubscriptionAction('删除', 'btn danger compact-action', () => deleteUA(ua)));
    actionCell.appendChild(actions);
    row.appendChild(actionCell);
    tbody.appendChild(row);
  });
}

function renderSubscriptions(subscriptions, customUAs) {
  const tbody = document.getElementById('subscriptionsBody');
  if (!tbody) return;
  tbody.replaceChildren();
  if (subscriptions.length === 0) {
    const row = document.createElement('tr');
    const cell = appendSubscriptionCell(row, '暂无订阅', 'text-center text-dim');
    cell.colSpan = 6;
    tbody.appendChild(row);
    return;
  }

  const customByID = new Map(customUAs.map(ua => [ua.id, ua]));
  subscriptions.forEach(sub => {
    const row = document.createElement('tr');
    const nameCell = appendSubscriptionCell(row, sub.name || '');
    if (sub.last_error) {
      const error = document.createElement('div');
      error.className = 'subscription-error';
      error.textContent = '错误: ' + sub.last_error;
      nameCell.appendChild(error);
    }
    appendSubscriptionCell(row, sub.url || '', 'subscription-break-all');

    let uaDisplay = sub.user_agent || 'Chrome (默认)';
    if (sub.custom_ua_id) {
      const customUA = customByID.get(sub.custom_ua_id);
      uaDisplay = customUA ? `${customUA.name} (${customUA.user_agent})` : '自定义 UA 已不存在';
    } else if (sub.user_agent === 'Chrome') {
      uaDisplay = 'Chrome (默认)';
    }
    appendSubscriptionCell(row, uaDisplay);
    appendSubscriptionCell(row, sub.update_interval > 0 ? String(sub.update_interval) : '禁用');
    appendSubscriptionCell(
      row,
      sub.last_update_time > 0 ? new Date(sub.last_update_time * 1000).toLocaleString() : '从未更新',
      'subscription-time'
    );

    const actionCell = document.createElement('td');
    actionCell.className = 'subscription-actions-cell';
    const actions = document.createElement('div');
    actions.className = 'subscription-actions';
    actions.appendChild(createSubscriptionAction('编辑', 'btn ghost btn-blue compact-action', () => editSub(sub)));
    actions.appendChild(createSubscriptionAction('更新', 'btn ghost btn-green compact-action', () => updateSub(sub.id)));
    actions.appendChild(createSubscriptionAction('删除', 'btn danger compact-action', () => deleteSub(sub.id)));
    actionCell.appendChild(actions);
    row.appendChild(actionCell);
    tbody.appendChild(row);
  });
}

function showAddUAModal() {
  document.getElementById('uaNameInput').value = '';
  document.getElementById('uaContentInput').value = '';
  document.getElementById('uaIdInput').value = '';
  document.getElementById('uaErr').textContent = '';
  document.getElementById('addUATitle').textContent = '添加自定义 User-Agent';
  document.getElementById('addUAModal').classList.remove('hidden');
  document.getElementById('uaNameInput').focus();
}

function editUA(ua) {
  document.getElementById('uaNameInput').value = ua.name || '';
  document.getElementById('uaContentInput').value = ua.user_agent || '';
  document.getElementById('uaIdInput').value = ua.id || '';
  document.getElementById('uaErr').textContent = '';
  document.getElementById('addUATitle').textContent = '编辑自定义 User-Agent';
  document.getElementById('addUAModal').classList.remove('hidden');
  document.getElementById('uaNameInput').focus();
}

function closeUAModal() {
  document.getElementById('addUAModal').classList.add('hidden');
}

function submitUA() {
  const id = document.getElementById('uaIdInput').value.trim();
  const name = document.getElementById('uaNameInput').value.trim();
  const userAgent = document.getElementById('uaContentInput').value.trim();
  if (!name || !userAgent) {
    document.getElementById('uaErr').textContent = '名称和内容不能为空';
    return;
  }

  API.raw('/api/admin/subscriptions/custom_ua/save', {
    method: 'POST',
    body: JSON.stringify({ id, name, user_agent: userAgent })
  })
    .then(() => {
      closeUAModal();
      loadSubscriptions();
      toast('UA 保存成功', 'ok');
    })
    .catch(err => {
      document.getElementById('uaErr').textContent = '保存失败: ' + err;
    });
}

function showDeleteConfirm(options) {
  const modal = document.getElementById('subDeleteModal');
  if (!modal) return;
  const title = document.getElementById('subDeleteModalTitle');
  const message = document.getElementById('subDeleteModalText');
  const nodeOption = document.getElementById('subDeleteNodesOption');
  const checkbox = document.getElementById('delSubNodesCheck');
  const okButton = document.getElementById('subDeleteOkBtn');
  const cancelButton = document.getElementById('subDeleteCancelBtn');
  const previousFocus = document.activeElement;

  title.textContent = options.title;
  message.textContent = options.message;
  nodeOption.classList.toggle('hidden', !options.allowNodeChoice);
  checkbox.checked = true;
  modal.classList.remove('hidden');
  cancelButton.focus();

  const cleanup = () => {
    modal.classList.add('hidden');
    okButton.onclick = null;
    cancelButton.onclick = null;
    if (previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
  };
  okButton.onclick = () => {
    const deleteNodes = options.allowNodeChoice ? checkbox.checked : false;
    cleanup();
    options.onConfirm(deleteNodes);
  };
  cancelButton.onclick = cleanup;
}

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return;
  const modal = document.getElementById('subDeleteModal');
  if (modal && !modal.classList.contains('hidden')) {
    document.getElementById('subDeleteCancelBtn').click();
  }
});

function deleteUA(ua) {
  showDeleteConfirm({
    title: '确认删除 UA',
    message: `确定要删除自定义 UA “${ua.name}”吗？`,
    allowNodeChoice: false,
    onConfirm: () => {
      API.raw('/api/admin/subscriptions/custom_ua/delete', {
        method: 'POST',
        body: JSON.stringify({ id: ua.id })
      })
        .then(() => {
          loadSubscriptions();
          toast('UA 已删除', 'ok');
        })
        .catch(err => toast('删除失败: ' + err, 'err'));
    }
  });
}

function showAddSubModal() {
  document.getElementById('addSubTitle').textContent = '添加订阅';
  document.getElementById('subIdInput').value = '';
  document.getElementById('subNameInput').value = '';
  document.getElementById('subUrlInput').value = '';
  document.getElementById('subUaSelect').value = 'Chrome';
  document.getElementById('subIntervalInput').value = '0';
  document.getElementById('subErr').textContent = '';
  document.getElementById('addSubModal').classList.remove('hidden');
}

function editSub(sub) {
  document.getElementById('addSubTitle').textContent = '编辑订阅';
  document.getElementById('subIdInput').value = sub.id || '';
  document.getElementById('subNameInput').value = sub.name || '';
  document.getElementById('subUrlInput').value = sub.url || '';
  const select = document.getElementById('subUaSelect');
  const selectedValue = sub.custom_ua_id ? 'custom:' + sub.custom_ua_id : (sub.user_agent || 'Chrome');
  if (![...select.options].some(option => option.value === selectedValue)) {
    const option = document.createElement('option');
    option.value = selectedValue;
    option.textContent = '原自定义 UA 已不存在';
    option.dataset.customUa = 'true';
    select.appendChild(option);
  }
  select.value = selectedValue;
  document.getElementById('subIntervalInput').value = sub.update_interval || 0;
  document.getElementById('subErr').textContent = '';
  document.getElementById('addSubModal').classList.remove('hidden');
}

function closeSubModal() {
  document.getElementById('addSubModal').classList.add('hidden');
}

function submitSub() {
  const id = document.getElementById('subIdInput').value.trim();
  const name = document.getElementById('subNameInput').value.trim();
  const url = document.getElementById('subUrlInput').value.trim();
  const uaSelection = document.getElementById('subUaSelect').value;
  const interval = parseInt(document.getElementById('subIntervalInput').value, 10) || 0;
  if (!name || !url) {
    document.getElementById('subErr').textContent = '名称和链接不能为空';
    return;
  }

  const isCustomUA = uaSelection.startsWith('custom:');
  const payload = {
    id,
    name,
    url,
    user_agent: isCustomUA ? '' : uaSelection,
    custom_ua_id: isCustomUA ? uaSelection.slice('custom:'.length) : '',
    update_interval: interval
  };
  API.raw('/api/admin/subscriptions/save', { method: 'POST', body: JSON.stringify(payload) })
    .then(() => {
      closeSubModal();
      loadSubscriptions();
      toast('订阅保存成功', 'ok');
    })
    .catch(err => {
      document.getElementById('subErr').textContent = '保存失败: ' + err;
    });
}

function deleteSub(id) {
  showDeleteConfirm({
    title: '确认删除订阅',
    message: '确定要删除此订阅吗？',
    allowNodeChoice: true,
    onConfirm: deleteNodes => {
      API.raw('/api/admin/subscriptions/delete', {
        method: 'POST',
        body: JSON.stringify({ id, delete_nodes: deleteNodes })
      })
        .then(() => {
          loadSubscriptions();
          toast(deleteNodes ? '订阅及其独占节点已删除' : '订阅已删除，节点转为手动管理', 'ok');
        })
        .catch(err => toast('删除失败: ' + err, 'err'));
    }
  });
}

function updateSub(id) {
  API.raw('/api/admin/subscriptions/update', { method: 'POST', body: JSON.stringify({ id }) })
    .then(result => {
      toast(result.triggered ? '已触发后台更新' : '该订阅正在更新', 'ok');
      setTimeout(loadSubscriptions, 2000);
    })
    .catch(err => toast('更新请求失败: ' + err, 'err'));
}

function updateAllSubs() {
  API.raw('/api/admin/subscriptions/update', { method: 'POST', body: JSON.stringify({ id: '' }) })
    .then(result => {
      toast(`已触发 ${result.triggered || 0} 个订阅更新`, 'ok');
      setTimeout(loadSubscriptions, 3000);
    })
    .catch(err => toast('更新请求失败: ' + err, 'err'));
}
