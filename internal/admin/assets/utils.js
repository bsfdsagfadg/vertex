export function $(s) { return document.querySelector(s); }
export function toast(msg) { const t = $('#toast'); t.textContent = msg; t.classList.add('show'); setTimeout(() => t.classList.remove('show'), 1900); }
export function esc(s) { return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
export const typeLabels = { vless: 'VLESS', vmess: 'VMess', trojan: 'Trojan', ss: 'Shadowsocks', shadowsocks: 'Shadowsocks', hysteria2: 'Hysteria2', hy2: 'Hysteria2', tuic: 'TUIC' };
export const DEFAULT_BG = "url('background.jpg')";

export function showConfirm(msg, onOk, onCancel, onSave) {
  const m = $('#confirmModal');
  if (!m) return;
  $('#confirmModalText').textContent = msg;
  m.classList.remove('hidden');
  
  const okBtn = $('#confirmOkBtn');
  const cancelBtn = $('#confirmCancelBtn');
  const saveBtn = $('#confirmSaveBtn');
  
  if (onSave) {
    saveBtn.classList.remove('hidden');
  } else if (saveBtn) {
    saveBtn.classList.add('hidden');
  }

  const cleanup = () => {
    m.classList.add('hidden');
    okBtn.onclick = null;
    cancelBtn.onclick = null;
    if (saveBtn) saveBtn.onclick = null;
  };
  
  okBtn.onclick = () => { cleanup(); if(onOk) onOk(); };
  cancelBtn.onclick = () => { cleanup(); if(onCancel) onCancel(); };
  if (saveBtn) {
    saveBtn.onclick = () => { cleanup(); if(onSave) onSave(); };
  }
}
