function initSubscriptions() {
    loadSubscriptions();
}

function loadSubscriptions() {
    apiGet('/api/admin/subscriptions/list')
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
    sel.innerHTML = '<option value="Chrome">Chrome (默认)</option>' +
        '<option value="clash-verge/v2.5.2">clash-verge/v2.5.2</option>' +
        '<option value="Clash.Meta">Clash.Meta</option>' +
        '<option value="v2rayNG/1.8.5">v2rayNG/1.8.5</option>';
    
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
            <td>${escapeHTML(ua.name)}</td>
            <td style="word-break:break-all;">${escapeHTML(ua.user_agent)}</td>
            <td>
                <button class="btn danger" style="padding:2px 6px; font-size:12px;" onclick="deleteUA('${escapeHTML(ua.name)}')">删除</button>
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
            errHtml = `<div style="color:var(--red);font-size:12px;margin-top:4px;">错误: ${escapeHTML(sub.last_error)}</div>`;
        }
        
        let uaDisplay = escapeHTML(sub.user_agent);
        if (!uaDisplay || uaDisplay === 'Chrome') uaDisplay = 'Chrome (默认)';
        
        tr.innerHTML = `
            <td>
                ${escapeHTML(sub.name)}
                ${errHtml}
            </td>
            <td style="word-break:break-all;">${escapeHTML(sub.url)}</td>
            <td>${uaDisplay}</td>
            <td>${sub.update_interval > 0 ? sub.update_interval : '禁用'}</td>
            <td style="font-size:12px;">${lastUpdate}</td>
            <td>
                <div style="display:flex;gap:4px;flex-wrap:wrap;">
                    <button class="btn ghost btn-blue" style="padding:2px 6px; font-size:12px;" onclick='editSub(${JSON.stringify(sub).replace(/'/g, "&apos;")})'>编辑</button>
                    <button class="btn ghost btn-green" style="padding:2px 6px; font-size:12px;" onclick="updateSub('${escapeHTML(sub.id)}')">更新</button>
                    <button class="btn danger" style="padding:2px 6px; font-size:12px;" onclick="deleteSub('${escapeHTML(sub.id)}')">删除</button>
                </div>
            </td>
        `;
        tbody.appendChild(tr);
    });
}

function showAddUAModal() {
    document.getElementById('addUATitle').innerText = '添加自定义 User-Agent';
    document.getElementById('uaNameInput').value = '';
    document.getElementById('uaNameInput').disabled = false;
    document.getElementById('uaContentInput').value = '';
    document.getElementById('uaErr').innerText = '';
    document.getElementById('addUAModal').classList.remove('hidden');
}

function closeUAModal() {
    document.getElementById('addUAModal').classList.add('hidden');
}

function submitUA() {
    const name = document.getElementById('uaNameInput').value.trim();
    const ua = document.getElementById('uaContentInput').value.trim();
    if (!name || !ua) {
        document.getElementById('uaErr').innerText = '名称和User-Agent不能为空';
        return;
    }
    
    apiPost('/api/admin/subscriptions/custom_ua/save', { name: name, user_agent: ua })
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

function deleteUA(name) {
    if (!confirm('确定要删除自定义 UA "' + name + '" 吗？')) return;
    apiPost('/api/admin/subscriptions/custom_ua/delete', { name: name })
        .then(res => {
            if (res.ok) {
                loadSubscriptions();
                toast('已删除 UA', 'ok');
            }
        })
        .catch(err => toast('删除失败: ' + err, 'err'));
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
    
    apiPost('/api/admin/subscriptions/save', payload)
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
    if (!confirm('确定要删除此订阅吗？这将同时删除通过此订阅导入的所有节点。')) return;
    apiPost('/api/admin/subscriptions/delete', { id: id })
        .then(res => {
            if (res.ok) {
                loadSubscriptions();
                toast('已删除订阅及相关节点', 'ok');
            }
        })
        .catch(err => toast('删除失败: ' + err, 'err'));
}

function updateSub(id) {
    toast('已触发后台更新，请稍后刷新查看结果', 'ok');
    apiPost('/api/admin/subscriptions/update', { id: id })
        .then(res => {
            if (res.ok) {
                setTimeout(loadSubscriptions, 2000);
            }
        })
        .catch(err => toast('更新请求失败: ' + err, 'err'));
}

function updateAllSubs() {
    toast('已触发全量后台更新，请稍后刷新查看结果', 'ok');
    apiPost('/api/admin/subscriptions/update', { id: "" })
        .then(res => {
            if (res.ok) {
                setTimeout(loadSubscriptions, 3000);
            }
        })
        .catch(err => toast('更新请求失败: ' + err, 'err'));
}
