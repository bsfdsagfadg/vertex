import { API, setUnauthorizedHandler } from './api.js';
import { $, showConfirm } from './utils.js';
import { configureOverviewService, loadOverview } from './page-overview.js';
import * as settings from './page-settings.js';
import { configureKeysService, loadKeys } from './page-keys.js';
import * as models from './page-models.js';
import { configureNodesService, loadNodes, teardownNodes } from './page-nodes.js';
import { configureSubscriptionsService, loadSubscriptions, teardownSubscriptions } from './page-subscriptions.js';
import { configureAppearanceService, initAppearance, loadAppearance, teardownAppearance } from './page-appearance.js';
import { configureLogsService, loadLogs, teardownLogs } from './page-logs.js';
import { configureBuildService, loadBuildInfo } from './page-build.js';

configureOverviewService({ keys: API.keys, models: API.models, nodes: API.nodes });
settings.configureSettingsService({ settings: API.settings, changePassword: API.changePassword.bind(API) });
configureKeysService({ keys: API.keys });
models.configureModelsService({ models: API.models, settings: API.settings });
configureNodesService({ settings: API.settings, nodes: API.nodes, proxyNodes: API.proxyNodes, useNode: API.useNode.bind(API) });
configureSubscriptionsService({ subscriptions: API.subscriptions, nodes: API.nodes });
configureAppearanceService({ session: API.checkAuth.bind(API), settings: API.settings, appearance: API.appearance });
initAppearance();
configureLogsService({ settings: API.settings, logs: API.logs });
configureBuildService({ build: API.build });

const features = {
  overview: { load: loadOverview },
  settings: { load: settings.loadSettings, dirty: settings.isSettingsDirty, discard: settings.discardSettingsChanges, save: settings.saveSettings },
  keys: { load: loadKeys },
  models: { load: models.loadModels, dirty: models.isModelsDirty, discard: models.discardModelChanges, save: models.saveModels },
  nodes: { load: loadNodes, teardown: teardownNodes },
  subscriptions: { load: loadSubscriptions, teardown: teardownSubscriptions },
  appearance: { load: loadAppearance, teardown: teardownAppearance },
  logs: { load: loadLogs, teardown: teardownLogs },
  build: { load: loadBuildInfo },
};

let currentPage = null;
let navigationPending = false;
let navigationToken = 0;

function showApp() {
  const loginPanel = $('#login');
  const app = $('#app');
  if (!loginPanel.classList.contains('hidden')) {
    loginPanel.classList.add('out');
    setTimeout(() => loginPanel.classList.add('hidden'), 420);
  }
  app.classList.remove('hidden');
  requestAnimationFrame(() => app.classList.add('in'));
  navigate('overview', true);
}

function showLogin() {
  const loginPanel = $('#login');
  const app = $('#app');
  app.classList.remove('in');
  setTimeout(() => app.classList.add('hidden'), 360);
  loginPanel.classList.remove('hidden', 'out');
}

async function login() {
  $('#loginErr').textContent = '';
  try {
    await API.login($('#pw').value);
    showApp();
  } catch (error) {
    $('#loginErr').textContent = '密码错误或登录失败';
  }
}

async function logout() {
  try { await API.logout(); } catch (error) {}
  navigationToken++;
  if (currentPage && features[currentPage]?.teardown) features[currentPage].teardown();
  currentPage = null;
  showLogin();
}

function requestNavigation(page, instant) {
  if (navigationPending || page === currentPage) return;
  const currentFeature = features[currentPage];
  if (currentFeature?.dirty?.()) {
    navigationPending = true;
    showConfirm('您对设置进行了修改且未保存，离开将丢失这些更改。\n确认离开吗？', () => {
      currentFeature.discard?.();
      navigationPending = false;
      navigate(page, instant);
    }, () => { navigationPending = false; }, async () => {
      try {
        const saved = await currentFeature.save?.();
        if (saved === false) {
          navigationPending = false;
          return;
        }
        navigationPending = false;
        navigate(page, instant);
      } catch (error) {
        navigationPending = false;
      }
    });
    return;
  }
  navigate(page, instant);
}

function navigate(page, instant = false) {
  const next = $('#page-' + page);
  if (!next || !features[page]) return;
  const token = ++navigationToken;
  document.querySelectorAll('nav button').forEach(button => button.classList.toggle('active', button.dataset.page === page));
  const previousPage = currentPage;
  const current = previousPage ? $('#page-' + previousPage) : null;
  if (previousPage && features[previousPage]?.teardown) features[previousPage].teardown();
  currentPage = page;

  const enter = () => {
    if (token !== navigationToken) return;
    next.classList.add('entering');
    next.classList.remove('hidden');
    requestAnimationFrame(() => requestAnimationFrame(() => {
      if (token !== navigationToken) return;
      next.classList.remove('entering');
      Promise.resolve(features[page].load?.()).catch(() => {});
    }));
  };
  if (current && current !== next && !instant) {
    current.classList.add('leaving');
    setTimeout(() => {
      if (token !== navigationToken) return;
      current.classList.add('hidden');
      current.classList.remove('leaving');
      enter();
    }, 150);
  } else {
    document.querySelectorAll('main section').forEach(section => {
      if (section !== next) section.classList.add('hidden');
    });
    enter();
  }
}

function toggleMenu() {
  document.querySelector('aside').classList.toggle('open');
  $('#menuOverlay').classList.toggle('open');
}

function closeMenu() {
  document.querySelector('aside').classList.remove('open');
  $('#menuOverlay').classList.remove('open');
}

setUnauthorizedHandler(showLogin);
$('#pw').addEventListener('keydown', event => { if (event.key === 'Enter') login(); });
document.addEventListener('admin:logout', logout);
window.addEventListener('beforeunload', event => {
  if (!Object.values(features).some(feature => feature.dirty?.())) return;
  event.preventDefault();
  event.returnValue = '';
});
document.addEventListener('click', event => {
  const navButton = event.target.closest('nav button[data-page]');
  if (navButton) {
    requestNavigation(navButton.dataset.page);
    closeMenu();
    return;
  }
  const action = event.target.closest('[data-shell-action]')?.dataset.shellAction;
  if (action === 'login') login();
  else if (action === 'logout') logout();
  else if (action === 'toggle-menu') toggleMenu();
  else if (action === 'close-menu') closeMenu();
  else if (action === 'refresh-overview') loadOverview();
});

(async () => {
  try {
    const result = await API.checkAuth();
    if (result.authenticated) showApp();
    else showLogin();
  } catch (error) {
    showLogin();
  }
})();
