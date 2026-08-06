function initSubscriptions() {
    loadSubscriptions();
}

function loadSubscriptions() {
    API.raw('/api/admin/subscriptions/list')
        .then(data => {
            if (!data) return;
            renderCustomUAs(data.custom_uas || []);
            renderSubscriptions(data.subscriptions || [], data.custom_uas || []);
            updateUaSelect(data.custom_uas || []);
        })
        .catch(err => toast('加载订阅配置失败: ' + err, 'err'));
}

function updateUaSelect(customUAs) {
    const sel = document.getElementById('subUaSelect');
    if (!sel) return;
    
    // Retain built-in options up to the first 5 elements (Chrome, clash-verge, Clash.Meta, v2rayNG, casverge)
    // and remove previously appended custom UAs.
    while (sel.options.length > 5) {
        sel.remove(sel.options.length - 1);
    }
    
    customUAs.forEach(ua => {
        const opt = document.createElement('option');
        opt.value = ua.user_agent;
        opt.textContent = `${ua.name} (${ua.user_agent})`;
        sel.appendChild(opt);
    });
}

function renderCustomUAs(uas) {
    const tbody = document.getElementById('customUAsBody');
    if (!tbody) return;
    tbody.innerHTML = '';
    if (uas.length === 0) {
        tbody.innerHTML = '<tr><td colspan="3" class="text-center text-dim">暂无自定义 UA</td></tr>';
        return;
    }
    uas.forEach(ua => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
            <td>${esc(ua.name)}</td>
            <td style="word-break:break-all;">${esc(ua.user_agent)}</td>
            <td style="text-align:right;">
                <div style="display:flex;gap:4px;flex-wrap:nowrap;justify-content:flex-end;">
                    <button class="btn ghost btn-blue" style="padding:2px 6px; font-size:12px;" onclick="editUA('${esc(ua.name)}', '${esc(ua.user_agent)}')">编辑</button>
                    <button class="btn danger" style="padding:2px 6px; font-size:12px;" onclick="deleteUA('${esc(ua.name)}')">删除</button>
                </div>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

function renderSubscriptions(subs, uas) {
    const tbody = document.getElementById('subscriptionsBody');
    if (!tbody) return;
    tbody.innerHTML = '';
    if (subs.length === 0) {
        tbody.innerHTML = '<tr><td colspan="6" class="text-center text-dim">暂无订阅</td></tr>';
        return;
    }
    subs.forEach(sub => {
        const tr = document.createElement('tr');
        const lastUpdate = sub.last_update_time > 0 ? new Date(sub.last_update_time * 1000).toLocaleString() : '从未更新';
        let errHtml = '';
        if (sub.last_error) {
            errHtml = `<div style="color:var(--red);font-size:12px;margin-top:4px;">错误: ${esc(sub.last_error)}</div>`;
        }
        
        let uaDisplay = esc(sub.user_agent);
        if (!uaDisplay || uaDisplay === 'Chrome') uaDisplay = 'Chrome (默认)';
        
        tr.innerHTML = `
            <td>
                ${esc(sub.name)}
                ${errHtml}
            </td>
            <td style="word-break:break-all;">${esc(sub.url)}</td>
            <td>${uaDisplay}</td>
            <td>${sub.update_interval > 0 ? sub.update_interval : '禁用'}</td>
            <td style="font-size:12px;">${lastUpdate}</td>
            <td style="text-align:right;">
                <div style="display:flex;gap:4px;flex-wrap:nowrap;justify-content:flex-end;">
                    <button class="btn ghost btn-blue" style="padding:2px 6px; font-size:12px;" onclick='editSub(${JSON.stringify(sub).replace(/'/g, "&apos;")})'>编辑</button>
                    <button class="btn ghost btn-green" style="padding:2px 6px; font-size:12px;" onclick="updateSub('${esc(sub.id)}')">更新</button>
                    <button class="btn danger" style="padding:2px 6px; font-size:12px;" onclick="deleteSub('${esc(sub.id)}')">删除</button>
                </div>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

function showAddUAModal() {
    document.getElementById('uaNameInput').value = '';
    document.getElementById('uaContentInput').value = '';
    document.getElementById('uaOriginalName').value = '';
    document.getElementById('uaErr').textContent = '';
    document.getElementById('addUATitle').textContent = '添加自定义 User-Agent';
    document.getElementById('addUAModal').classList.remove('hidden');
    document.getElementById('uaNameInput').focus();
}

function editUA(name, userAgent) {
    document.getElementById('uaNameInput').value = name;
    document.getElementById('uaContentInput').value = userAgent;
    document.getElementById('uaOriginalName').value = name;
    document.getElementById('uaErr').textContent = '';
    document.getElementById('addUATitle').textContent = '编辑自定义 User-Agent';
    document.getElementById('addUAModal').classList.remove('hidden');
    document.getElementById('uaNameInput').focus();
}

function closeUAModal() {
    document.getElementById('addUAModal').classList.add('hidden');
}

function submitUA() {
    const name = document.getElementById('uaNameInput').value.trim();
    const ua = document.getElementById('uaContentInput').value.trim();
    const origName = document.getElementById('uaOriginalName').value;
    
    if (!name || !ua) {
        document.getElementById('uaErr').textContent = '名称和内容不能为空';
        return;
    }
    
    API.raw('/api/admin/subscriptions/custom_ua/save', { method: 'POST', body: JSON.stringify({ name: name, user_agent: ua, original_name: origName }) })
        .then(res => {
            if (res.ok) {
                closeUAModal();
                loadSubscriptions();
                toast('UA保存成功', 'ok');
            }
        })
        .catch(err => {
            document.getElementById('uaErr').innerText = '保存失败: ' + err;
        });
}

function showDeleteConfirm(title, msg, onOk) {
    const m = document.getElementById('subDeleteModal');
    if (!m) return;
    document.getElementById('subDeleteModalTitle').textContent = title;
    document.getElementById('subDeleteModalText').innerHTML = msg;
    m.classList.remove('hidden');
    
    const okBtn = document.getElementById('subDeleteOkBtn');
    const cancelBtn = document.getElementById('subDeleteCancelBtn');
    
    const cleanup = () => {
        m.classList.add('hidden');
        okBtn.onclick = null;
        cancelBtn.onclick = null;
    };
    
    okBtn.onclick = () => { cleanup(); if(onOk) onOk(); };
    cancelBtn.onclick = () => { cleanup(); };
}

function deleteUA(name) {
    showDeleteConfirm('确认删除 UA', '确定要删除自定义 UA "' + esc(name) + '" 吗？', () => {
        API.raw('/api/admin/subscriptions/custom_ua/delete', { method: 'POST', body: JSON.stringify({ name: name }) })
            .then(res => {
                loadSubscriptions();
            })
            .catch(err => toast('删除失败: ' + err, 'err'));
    });
}

function showAddSubModal() {
    document.getElementById('addSubTitle').innerText = '添加订阅';
    document.getElementById('subIdInput').value = '';
    document.getElementById('subNameInput').value = '';
    document.getElementById('subUrlInput').value = '';
    document.getElementById('subUaSelect').value = 'Chrome';
    document.getElementById('subIntervalInput').value = '0';
    document.getElementById('subErr').innerText = '';
    document.getElementById('addSubModal').classList.remove('hidden');
}

function editSub(sub) {
    document.getElementById('addSubTitle').innerText = '编辑订阅';
    document.getElementById('subIdInput').value = sub.id || '';
    document.getElementById('subNameInput').value = sub.name || '';
    document.getElementById('subUrlInput').value = sub.url || '';
    
    const sel = document.getElementById('subUaSelect');
    let found = false;
    for (let i = 0; i < sel.options.length; i++) {
        if (sel.options[i].value === sub.user_agent) {
            found = true;
            break;
        }
    }
    if (!found) {
        // If it's a previously deleted UA, we still show it as text
        const opt = document.createElement('option');
        opt.value = sub.user_agent;
        opt.textContent = sub.user_agent + " (未在列表中)";
        sel.appendChild(opt);
    }
    
    sel.value = sub.user_agent || 'Chrome';
    document.getElementById('subIntervalInput').value = sub.update_interval || 0;
    document.getElementById('subErr').innerText = '';
    document.getElementById('addSubModal').classList.remove('hidden');
}

function closeSubModal() {
    document.getElementById('addSubModal').classList.add('hidden');
}

function submitSub() {
    const id = document.getElementById('subIdInput').value.trim();
    const name = document.getElementById('subNameInput').value.trim();
    const url = document.getElementById('subUrlInput').value.trim();
    const ua = document.getElementById('subUaSelect').value;
    const interval = parseInt(document.getElementById('subIntervalInput').value) || 0;
    
    if (!name || !url) {
        document.getElementById('subErr').innerText = '名称和链接不能为空';
        return;
    }
    
    const payload = {
        id: id,
        name: name,
        url: url,
        user_agent: ua,
        update_interval: interval
    };
    
    API.raw('/api/admin/subscriptions/save', { method: 'POST', body: JSON.stringify(payload) })
        .then(res => {
            if (res.ok) {
                closeSubModal();
                loadSubscriptions();
                toast('订阅保存成功', 'ok');
            }
        })
        .catch(err => {
            document.getElementById('subErr').innerText = '保存失败: ' + err;
        });
}

function deleteSub(id) {
    showDeleteConfirm('确认删除订阅', '确定要删除此订阅吗？<br><label style="display:flex;align-items:center;margin-top:16px;cursor:pointer;"><input type="checkbox" id="delSubNodesCheck" checked style="margin-right:8px;cursor:pointer;">同时删除该订阅导入的节点</label>', () => {
        const delNodes = document.getElementById('delSubNodesCheck') ? document.getElementById('delSubNodesCheck').checked : true;
        API.raw('/api/admin/subscriptions/delete', { method: 'POST', body: JSON.stringify({ id: id, delete_nodes: delNodes }) })
            .then(res => {
                loadSubscriptions();
            })
            .catch(err => toast('删除失败: ' + err, 'err'));
    });
}

function updateSub(id) {
    toast('已触发后台更新，请稍后刷新查看结果', 'ok');
    API.raw('/api/admin/subscriptions/update', { method: 'POST', body: JSON.stringify({ id: id }) })
        .then(res => {
            setTimeout(loadSubscriptions, 2000);
        })
        .catch(err => toast('更新请求失败: ' + err, 'err'));
}

function updateAllSubs() {
    toast('已触发全量后台更新，请稍后刷新查看结果', 'ok');
    API.raw('/api/admin/subscriptions/update', { method: 'POST', body: JSON.stringify({ id: "" }) })
        .then(res => {
            setTimeout(loadSubscriptions, 3000);
        })
        .catch(err => toast('更新请求失败: ' + err, 'err'));
}
