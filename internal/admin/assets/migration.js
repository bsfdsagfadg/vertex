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
  let applying = false;
  let rollbackApplying = false;
  let statusRetryTimer = null;
  let pollTimer = null;

  const api = async (path, options = {}) => {
    const headers = new Headers(options.headers || {});
    if (options.body) headers.set('Content-Type', 'application/json');
    if (options.method && options.method !== 'GET') headers.set('X-VProxy-Action', 'migration');
    const response = await fetch(path, { credentials: 'same-origin', ...options, headers });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const message = body?.error?.message || `请求失败 (${response.status})`;
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    return body;
  };

  const setText = (id, value) => { byId(id).textContent = value; };
  const pretty = (value) => JSON.stringify(value, null, 2);

  const updateApplyState = () => {
    // The server remains authoritative about the current state. The UI only
    // needs a plan and both confirmations; transient status reads (including
    // the 503 returned while the migration process is switching) must not
    // leave the action permanently disabled.
    // Keep the button actionable once a plan exists. The server remains the
    // authority for the two confirmations and returns a precise validation
    // error instead of leaving the user with a permanently disabled button.
    applyButton.disabled = applying || !currentPlanHash;
  };

	const updateRollbackApplyState = () => {
		rollbackApplyButton.disabled = rollbackApplying || !currentRollbackPlanHash;
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
			applying = false;
			errorView.textContent = '迁移完成。可以重启进入正常模式；如需立即回退，请先执行回滚预检。';
      applyButton.disabled = true;
    }
		if (status.state === 'FailedRecoverable') {
			if (status.failed_from !== 'RollingBack') applying = false;
			if (status.failed_from === 'RollingBack') rollbackApplying = false;
		}
		const rollbackVisible = ['Completed', 'RollbackReady', 'RollingBack', 'RollbackPrepared'].includes(status.state) ||
			(status.state === 'FailedRecoverable' && status.failed_from === 'RollingBack');
		rollbackPanel.classList.toggle('hidden', !rollbackVisible);
		if (status.state === 'RollbackPrepared') {
			rollbackApplying = false;
			errorView.textContent = 'V1 数据已恢复且 V2 已归档。本服务即将退出，请启动 V1。';
		}
		updateRollbackApplyState();
  };

  const scheduleStatusRetry = () => {
    if (statusRetryTimer) return;
    statusRetryTimer = window.setTimeout(() => {
      statusRetryTimer = null;
      refreshStatus();
    }, 1200);
  };

  const refreshStatus = async () => {
    try {
      renderStatus(await api('/api/admin/migration/status'));
    } catch (error) {
      if (applying || rollbackApplying || error.status === 503) {
        errorView.textContent = '迁移服务正在处理请求，状态暂时不可读，正在重试…';
        scheduleStatusRetry();
      } else {
        errorView.textContent = error.message;
      }
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
    applying = true;
    updateApplyState();
    try {
      await api('/api/admin/migration/apply', {
        method: 'POST',
        body: JSON.stringify({
          plan_hash: currentPlanHash,
          backup_confirmed: byId('migration-confirm-backup').checked,
          rollback_understood: byId('migration-confirm-rollback').checked,
        }),
      });
      // The apply operation is asynchronous. Start polling even if the
      // immediate follow-up status read races with an atomic status update.
      if (!pollTimer) pollTimer = window.setInterval(refreshStatus, 1200);
      await refreshStatus();
    } catch (error) {
      errorView.textContent = error.message;
      applying = false;
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
		rollbackApplying = true;
		updateRollbackApplyState();
		try {
			await api('/api/admin/migration/rollback/apply', {
				method: 'POST',
				body: JSON.stringify({
					plan_hash: currentRollbackPlanHash,
					v1_binary_confirmed: byId('migration-confirm-v1-binary').checked,
					traffic_stop_confirmed: byId('migration-confirm-traffic-stop').checked,
				}),
			});
			if (!pollTimer) pollTimer = window.setInterval(refreshStatus, 1200);
			await refreshStatus();
		} catch (error) {
			errorView.textContent = error.message;
			rollbackApplying = false;
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
