// ==========================================
// Vertex AI Proxy Admin - Main Controller
// ==========================================

function showApp() {
  const lg = $('#login');
  const app = $('#app');
  if (lg && !lg.classList.contains('hidden')) {
    lg.classList.add('out');
    setTimeout(() => lg.classList.add('hidden'), 420);
  }
  if (app) {
    app.classList.remove('hidden');
    requestAnimationFrame(() => app.classList.add('in'));
  }
  go('overview', true);
}

function showLogin() {
  const lg = $('#login');
  const app = $('#app');
  if (app) {
    app.classList.remove('in');
    setTimeout(() => app.classList.add('hidden'), 360);
  }
  if (lg) {
    lg.classList.remove('hidden', 'out');
  }
}

async function login() {
  const errEl = $('#loginErr');
  const pwEl = $('#pw');
  if (errEl) errEl.textContent = '';
  if (!pwEl) return;
  try {
    await API.login(pwEl.value);
    showApp();
  } catch (e) {
    if (errEl) errEl.textContent = '密码错误或登录失败';
  }
}

async function logout() {
  try {
    await API.logout();
  } catch (e) {}
  showLogin();
}

const pwInput = $('#pw');
if (pwInput) {
  pwInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') login();
  });
}

const LOADERS = {
  overview: typeof loadOverview === 'function' ? loadOverview : () => {},
  settings: typeof loadSettings === 'function' ? loadSettings : () => {},
  keys: typeof loadKeys === 'function' ? loadKeys : () => {},
  models: typeof loadModels === 'function' ? loadModels : () => {},
  nodes: typeof loadNodes === 'function' ? loadNodes : () => {},
  subscriptions: typeof loadSubscriptions === 'function' ? loadSubscriptions : () => {},
  appearance: typeof loadAppearance === 'function' ? loadAppearance : () => {},
  logs: typeof loadLogs === 'function' ? loadLogs : () => {},
};

function go(page, instant) {
  const curPage = AppState.curPage;
  if ((curPage === 'settings' || curPage === 'models') && page !== curPage && AppState.hasUnsavedSettings) {
    showConfirm('您对设置进行了修改且未保存，离开将丢失这些更改。\n确认离开吗？', () => {
      AppState.markDirty(false);
      go(page, instant);
    }, null, async () => {
      if (curPage === 'settings' && typeof saveSettings === 'function') {
        await saveSettings();
      } else if (curPage === 'models' && typeof saveModels === 'function') {
        await saveModels();
      }
      AppState.markDirty(false);
      go(page, instant);
    });
    return;
  }

  document.querySelectorAll('nav button').forEach(b => {
    b.classList.toggle('active', b.dataset.page === page);
  });

  const next = $('#page-' + page);
  const cur = curPage && $('#page-' + curPage);

  if (curPage === 'nodes') {
    AppState.clearTimer('test');
    AppState.clearTimer('nodeProgress');
    AppState.clearTimer('proxyProgress');
  }

  AppState.curPage = page;

  const enter = () => {
    if (!next) return;
    next.classList.add('entering');
    next.classList.remove('hidden');
    requestAnimationFrame(() => requestAnimationFrame(() => {
      next.classList.remove('entering');
      if (!AppState.pageCache[page]) {
        (LOADERS[page] || (() => {}))();
      }
    }));
  };

  if (cur && cur !== next && !instant) {
    cur.classList.add('leaving');
    setTimeout(() => {
      cur.classList.add('hidden');
      cur.classList.remove('leaving');
      if (AppState.pageCache[page] && next) {
        next.innerHTML = AppState.pageCache[page];
      }
      enter();
    }, 150);
  } else {
    document.querySelectorAll('main section').forEach(s => {
      if (s !== next) s.classList.add('hidden');
    });
    if (AppState.pageCache[page] && next) {
      next.innerHTML = AppState.pageCache[page];
    }
    enter();
  }
}

function toggleMenu() {
  const aside = document.querySelector('aside');
  const overlay = document.getElementById('menuOverlay');
  if (aside) aside.classList.toggle('open');
  if (overlay) overlay.classList.toggle('open');
}

function closeMenu() {
  const aside = document.querySelector('aside');
  const overlay = document.getElementById('menuOverlay');
  if (aside) aside.classList.remove('open');
  if (overlay) overlay.classList.remove('open');
}

document.querySelectorAll('nav button').forEach(b => {
  b.onclick = () => go(b.dataset.page);
  b.addEventListener('click', closeMenu);
});

(async () => {
  try {
    const r = await API.checkAuth();
    if (r && r.authenticated) {
      showApp();
    } else {
      showLogin();
    }
  } catch (e) {
    showLogin();
  }
})();
