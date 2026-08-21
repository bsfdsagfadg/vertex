function $(s) { return document.querySelector(s); }
function toast(msg) { const t = $('#toast'); t.textContent = msg; t.classList.add('show'); setTimeout(() => t.classList.remove('show'), 1900); }
function esc(s) { return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
const _tmap = { vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', ss: 'Shadowsocks', shadowsocks: 'Shadowsocks', hysteria2: 'Hysteria2', hy2: 'Hysteria2', tuic: 'TUIC' };
const DEFAULT_BG = "url('background.jpg')";

function showConfirm(msg, onOk, onCancel) {
  const m = $('#confirmModal');
  if (!m) return;
  $('#confirmModalText').textContent = msg;
  m.classList.remove('hidden');
  
  const okBtn = $('#confirmOkBtn');
  const cancelBtn = $('#confirmCancelBtn');
  
  const cleanup = () => {
    m.classList.add('hidden');
    okBtn.onclick = null;
    cancelBtn.onclick = null;
  };
  
  okBtn.onclick = () => { cleanup(); if(onOk) onOk(); };
  cancelBtn.onclick = () => { cleanup(); if(onCancel) onCancel(); };
}
