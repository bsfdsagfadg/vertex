(() => {
  'use strict';

  const byId = (id) => document.getElementById(id);
  const login = byId('migration-login');
  const consoleView = byId('migration-console');
  const loginError = byId('migration-login-error');
  const errorView = byId('migration-error');
  const applyButton = byId('migration-apply');
	const rollbackPanel = byId('migration-rollback-panel');
	const rollbackApplyButton = byId('migration-rollback-apply');
  let currentPlanHash = '';
	let currentRollbackPlanHash = '';
	let currentState = '';
  let pollTimer = null;

  const api = async (path, options = {}) => {
    const headers = new Headers(options.headers || {});
    if (options.body) headers.set('Content-Type', 'application/json');
    if (options.method && options.method !== 'GET') headers.set('X-VProxy-Action', 'migration');
    const response = await fetch(path, { credentials: 'same-origin', ...options, headers });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const message = body?.error?.message || `请求失败 (${response.status})`;
      throw new Error(message);
    }
    return body;
  };

  const setText = (id, value) => { byId(id).textContent = value; };
  const pretty = (value) => JSON.stringify(value, null, 2);

  const updateApplyState = () => {
		const resumableForwardFailure = currentState === 'FailedRecoverable' &&
			byId('migration-state').dataset.failedFrom !== 'RollingBack';
    applyButton.disabled = !(currentState === 'Prepared' || resumableForwardFailure) || !currentPlanHash ||
      !byId('migration-confirm-backup').checked ||
      !byId('migration-confirm-rollback').checked;
  };

	const updateRollbackApplyState = () => {
		const resumableRollbackFailure = currentState === 'FailedRecoverable' &&
			byId('migration-state').dataset.failedFrom === 'RollingBack';
		rollbackApplyButton.disabled = !(currentState === 'RollbackReady' || resumableRollbackFailure) || !currentRollbackPlanHash ||
			!byId('migration-confirm-v1-binary').checked ||
			!byId('migration-confirm-traffic-stop').checked;
	};

  const renderStatus = (status) => {
		currentState = status.state || 'Required';
    setText('migration-state', status.state || 'Required');
		byId('migration-state').dataset.failedFrom = status.failed_from || '';
    setText('migration-findings', pretty(status.findings || []));
    if (status.plan) {
      currentPlanHash = status.plan.plan_hash || '';
      setText('migration-plan', pretty(status.plan));
    }
		if (status.rollback_plan) {
			currentRollbackPlanHash = status.rollback_plan.plan_hash || '';
			setText('migration-rollback-plan', pretty(status.rollback_plan));
		}
    errorView.textContent = status.last_error || '';
    updateApplyState();
		const active = ['Migrating', 'Verifying', 'RetiringLegacy', 'Publishing', 'Finalizing', 'RollingBack'].includes(status.state);
    if (active && !pollTimer) pollTimer = window.setInterval(refreshStatus, 1200);
    if (!active && pollTimer) {
      window.clearInterval(pollTimer);
      pollTimer = null;
    }
    if (status.state === 'Completed') {
			errorView.textContent = '迁移完成。可以重启进入正常模式；如需立即回退，请先执行回滚预检。';
      applyButton.disabled = true;
    }
		const rollbackVisible = ['Completed', 'RollbackReady', 'RollingBack', 'RollbackPrepared'].includes(status.state) ||
			(status.state === 'FailedRecoverable' && status.failed_from === 'RollingBack');
		rollbackPanel.classList.toggle('hidden', !rollbackVisible);
		if (status.state === 'RollbackPrepared') {
			errorView.textContent = 'V1 数据已恢复且 V2 已归档。本服务即将退出，请启动 V1。';
		}
		updateRollbackApplyState();
  };

  const refreshStatus = async () => {
    try {
      renderStatus(await api('/api/admin/migration/status'));
    } catch (error) {
      errorView.textContent = error.message;
    }
  };

  byId('migration-login-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    loginError.textContent = '';
    try {
      await api('/api/admin/login', {
        method: 'POST',
        body: JSON.stringify({ password: byId('migration-password').value }),
      });
      login.classList.add('hidden');
      consoleView.classList.remove('hidden');
      await refreshStatus();
    } catch (error) {
      loginError.textContent = error.message;
    }
  });

  byId('migration-prepare').addEventListener('click', async () => {
    errorView.textContent = '';
    try {
      const plan = await api('/api/admin/migration/prepare', { method: 'POST', body: '{}' });
      currentPlanHash = plan.plan_hash || '';
      setText('migration-plan', pretty(plan));
      await refreshStatus();
    } catch (error) {
      errorView.textContent = error.message;
    }
  });

  byId('migration-refresh').addEventListener('click', refreshStatus);
  byId('migration-confirm-backup').addEventListener('change', updateApplyState);
  byId('migration-confirm-rollback').addEventListener('change', updateApplyState);
	byId('migration-confirm-v1-binary').addEventListener('change', updateRollbackApplyState);
	byId('migration-confirm-traffic-stop').addEventListener('change', updateRollbackApplyState);
  applyButton.addEventListener('click', async () => {
    errorView.textContent = '';
    applyButton.disabled = true;
    try {
      await api('/api/admin/migration/apply', {
        method: 'POST',
        body: JSON.stringify({
          plan_hash: currentPlanHash,
          backup_confirmed: byId('migration-confirm-backup').checked,
          rollback_understood: byId('migration-confirm-rollback').checked,
        }),
      });
      await refreshStatus();
    } catch (error) {
      errorView.textContent = error.message;
      updateApplyState();
    }
  });

	byId('migration-rollback-prepare').addEventListener('click', async () => {
		errorView.textContent = '';
		try {
			const plan = await api('/api/admin/migration/rollback/prepare', { method: 'POST', body: '{}' });
			currentRollbackPlanHash = plan.plan_hash || '';
			setText('migration-rollback-plan', pretty(plan));
			await refreshStatus();
		} catch (error) {
			errorView.textContent = error.message;
		}
	});

	rollbackApplyButton.addEventListener('click', async () => {
		errorView.textContent = '';
		rollbackApplyButton.disabled = true;
		try {
			await api('/api/admin/migration/rollback/apply', {
				method: 'POST',
				body: JSON.stringify({
					plan_hash: currentRollbackPlanHash,
					v1_binary_confirmed: byId('migration-confirm-v1-binary').checked,
					traffic_stop_confirmed: byId('migration-confirm-traffic-stop').checked,
				}),
			});
			await refreshStatus();
		} catch (error) {
			errorView.textContent = error.message;
			updateRollbackApplyState();
		}
	});

  Promise.all([
    api('/api/admin/check-auth'),
    fetch('/api/meta/build').then((response) => response.json()),
  ]).then(([auth, build]) => {
    setText('migration-build', `Version ${build.version || ''} · ${build.commit || 'local'}`);
    if (auth.authenticated) {
      login.classList.add('hidden');
      consoleView.classList.remove('hidden');
      refreshStatus();
    }
  }).catch((error) => { loginError.textContent = error.message; });
})();
