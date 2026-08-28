var _testTimer = null;
function showApp() { const lg = $('#login'), app = $('#app'); if (!lg.classList.contains('hidden')) { lg.classList.add('out'); setTimeout(() => lg.classList.add('hidden'), 420); } app.classList.remove('hidden'); requestAnimationFrame(() => app.classList.add('in')); loadBuildInfo(); go('overview', true); }
async function loadBuildInfo() { try { const r = await fetch('/api/meta/build', { credentials: 'same-origin' }); if (!r.ok) throw new Error('build'); const b = await r.json(); const el = $('#buildVersion'); if (el) { el.textContent = 'v' + (b.version || '未知'); el.title = `${b.commit || 'unknown'} · ${b.build_time || 'time unknown'}${b.dirty ? ' · 包含未提交修改' : ''}`; } const details = $('#buildInfoDetails'); if (details) { details.textContent = `版本 ${b.version || '未知'} · 提交 ${b.commit || 'unknown'} · 构建时间 ${b.build_time || 'unknown'} · ${b.go_version || ''} · ${b.goos || ''}/${b.goarch || ''}${b.dirty ? ' · 工作树有未提交修改' : ''}`; } } catch (_) { const el = $('#buildVersion'); if (el) el.textContent = 'v未知'; const details = $('#buildInfoDetails'); if (details) details.textContent = '构建信息不可用'; } }
function showLogin() { const lg = $('#login'), app = $('#app'); app.classList.remove('in'); setTimeout(() => app.classList.add('hidden'), 360); lg.classList.remove('hidden', 'out'); }
async function login() { $('#loginErr').textContent = ''; try { await API.login($('#pw').value); showApp(); } catch (e) { $('#loginErr').textContent = '密码错误或登录失败'; } }
async function logout() { try { await API.logout(); } catch (e) {} showLogin(); }
$('#pw').addEventListener('keydown', (e) => { if (e.key === 'Enter') login(); });

const LOADERS = { overview: loadOverview, settings: loadSettings, keys: loadKeys, models: loadModels, nodes: loadNodes, subscriptions: loadSubscriptions, appearance: loadAppearance, logs: loadLogs };
const PAGE_CACHE = {};
let curPage = null;
function go(page, instant) {
  if ((curPage === 'settings' || curPage === 'models') && page !== curPage && window.hasUnsavedSettings) {
    showConfirm('您对设置进行了修改且未保存，离开将丢失这些更改。<br>确认离开吗？', () => {
      window.hasUnsavedSettings = false;
      go(page, instant);
    }, null, async () => {
      if (curPage === 'settings' && typeof saveSettings === 'function') {
        await saveSettings();
      } else if (curPage === 'models' && typeof saveModels === 'function') {
        await saveModels();
      }
      window.hasUnsavedSettings = false;
      go(page, instant);
    });
    return;
  }

  document.querySelectorAll('nav button').forEach(b => b.classList.toggle('active', b.dataset.page === page));
  const next = $('#page-' + page), cur = curPage && $('#page-' + curPage);
  if (curPage === 'nodes' && _testTimer) { clearInterval(_testTimer); _testTimer = null; }
  curPage = page;
  const enter = () => {
    next.classList.add('entering');
    next.classList.remove('hidden');
    requestAnimationFrame(() => requestAnimationFrame(() => {
      next.classList.remove('entering');
      if (!PAGE_CACHE[page]) {
        (LOADERS[page] || (() => {}))();
      }
    }));
  };
  if (cur && cur !== next && !instant) {
    cur.classList.add('leaving');
    setTimeout(() => {
      cur.classList.add('hidden');
      cur.classList.remove('leaving');
      if (PAGE_CACHE[page]) { next.innerHTML = PAGE_CACHE[page]; }
      enter();
    }, 150);
  } else {
    document.querySelectorAll('main section').forEach(s => { if (s !== next) s.classList.add('hidden'); });
    if (PAGE_CACHE[page]) { next.innerHTML = PAGE_CACHE[page]; }
    enter();
  }
}
function toggleMenu() {
  document.querySelector('aside').classList.toggle('open');
  document.getElementById('menuOverlay').classList.toggle('open');
}
function closeMenu() {
  document.querySelector('aside').classList.remove('open');
  document.getElementById('menuOverlay').classList.remove('open');
}
document.querySelectorAll('nav button').forEach(b => b.onclick = () => go(b.dataset.page));
document.querySelectorAll('nav button').forEach(b => b.addEventListener('click', closeMenu));

(async () => { try { const r = await API.checkAuth(); if (r.authenticated) { showApp(); } else { showLogin(); } } catch (e) { showLogin(); } })();
