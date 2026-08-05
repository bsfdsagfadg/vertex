// page-appearance-api.js — 节点/外观 后台 API 通信（数据操作）
let currentAppearanceSettings = {};
let _saveTimeout = null;
async function saveAppearanceSettings(update) {
  Object.assign(currentAppearanceSettings, update);
  if (_saveTimeout) clearTimeout(_saveTimeout);
  _saveTimeout = setTimeout(async () => {
    try {
      await API.settings.put(currentAppearanceSettings);
    } catch (e) {
      console.error('Failed to save settings:', e);
    }
  }, 500);
}
async function setBgAndSync(v) {
  applyBg(v);
  localStorage.setItem('vproxy_bg', v); // Fallback
  try {
    await API.settings.put({ background_image: v });
    toast('背景已更换');
    loadAppearance(); // Sync current presets
  } catch (e) {
    toast('同步背景失败', true);
  }
}
function applyBgUrl() { 
  const u = $('#bgUrl').value.trim(); 
  if (!u) return; 
  setBgAndSync(`url('${u}')`); 
}
async function uploadBg(e) { 
  const f = e.target.files[0]; 
  if (!f) return;
  if (f.size > 10 * 1024 * 1024) {
    toast('文件不能超过10MB', true);
    return;
  }
  const fd = new FormData();
  fd.append('file', f);
  try {
    const res = await fetch('/api/admin/upload-bg', { method: 'POST', body: fd });
    const data = await res.json();
    if (res.ok && data.ok) {
      setBgAndSync(data.url);
      loadAppearance();
    } else {
      toast(data.error?.message || '上传失败', true);
    }
  } catch (err) {
    toast('上传失败', true);
  }
}
function resetBg() { 
  localStorage.removeItem('vproxy_bg'); 
  applyBg(DEFAULT_BG);
  API.settings.put({ background_image: DEFAULT_BG }).catch(()=>{});
  toast('已恢复默认'); 
}
window.deletePreset = async function(val, event) {
  if (event) event.stopPropagation();
  if (!confirm('确定要删除这个预设吗？')) return;

  if (val.startsWith('url')) {
    const match = val.match(/\/assets\/(background.*?\.jpg)/);
    if (match && match[1]) {
      try {
        const res = await API.raw('/api/admin/delete-bg', { method: 'POST', body: JSON.stringify({ filename: match[1] }) });
        if (res.ok) {
          toast('图片已删除');
          loadAppearance();
        }
      } catch (e) {
        toast('删除失败: ' + e.message);
      }
    }
  } else {
    if (currentAppearanceSettings.custom_bg_presets) {
      currentAppearanceSettings.custom_bg_presets = currentAppearanceSettings.custom_bg_presets.filter(p => p !== val);
      try {
        await API.settings.put({ custom_bg_presets: currentAppearanceSettings.custom_bg_presets });
        toast('预设已删除');
        loadAppearance();
      } catch (e) {
        toast('删除失败: ' + e.message);
      }
    }
  }
};
