// ==========================================
// Vertex AI Proxy Admin - Appearance Module
// ==========================================

function applyBg(v) {
  document.documentElement.style.setProperty('--bg-img', v);
  ColorUtils.applyThemeColorFromBg(v);
}

async function initBg() {
  try {
    const res = await API.checkAuth();
    if (res) AppState.currentAppearanceSettings = res;

    // Apply font size
    if (res?.font_size) {
      document.documentElement.style.setProperty('--base-font-size', res.font_size);
      const fsEl = document.getElementById('currentFontSize');
      if (fsEl) fsEl.textContent = res.font_size;
    }
    if (res?.font_color_type) {
      const bAdapt = document.getElementById('btnColorAdaptive');
      const bSpec = document.getElementById('btnColorSpecified');
      if (bAdapt) bAdapt.classList.toggle('active', res.font_color_type === 'adaptive');
      if (bSpec) bSpec.classList.toggle('active', res.font_color_type === 'specified');
    }
    if (res?.font_color) {
      const fcBox = document.getElementById('fontColorBox');
      if (fcBox) fcBox.style.backgroundColor = res.font_color;
    }
    if (res?.background_image && !res.background_image.includes('url') && !res.background_image.includes('gradient')) {
      const bgBox = document.getElementById('bgColorBox');
      if (bgBox) bgBox.style.backgroundColor = res.background_image;
    }

    const bg = res?.background_image;
    if (bg) {
      applyBg(bg);
      return;
    }
  } catch (e) {}
  const s = localStorage.getItem('vproxy_bg');
  if (s) applyBg(s);
}
initBg();

let _saveTimeout = null;
async function saveAppearanceSettings(update) {
  Object.assign(AppState.currentAppearanceSettings, update);
  if (_saveTimeout) clearTimeout(_saveTimeout);
  _saveTimeout = setTimeout(async () => {
    try {
      await API.settings.put(AppState.currentAppearanceSettings);
    } catch (e) {
      console.error('Failed to save settings:', e);
    }
  }, 500);
}

function adjustFontSize(delta) {
  let size = parseInt(AppState.currentAppearanceSettings.font_size || '14', 10);
  size += delta;
  if (size < 12) size = 12;
  if (size > 24) size = 24;
  const newSize = size + 'px';
  AppState.currentAppearanceSettings.font_size = newSize;
  const fsEl = $('#currentFontSize');
  if (fsEl) fsEl.textContent = newSize;
  document.documentElement.style.setProperty('--base-font-size', newSize);
  saveAppearanceSettings({ font_size: newSize });
}

function setFontColorType(type) {
  AppState.currentAppearanceSettings.font_color_type = type;
  const bAdapt = $('#btnColorAdaptive');
  const bSpec = $('#btnColorSpecified');
  if (bAdapt) bAdapt.classList.toggle('active', type === 'adaptive');
  if (bSpec) bSpec.classList.toggle('active', type === 'specified');

  if (type === 'adaptive') {
    if (AppState.currentAppearanceSettings.background_image) {
      ColorUtils.applyThemeColorFromBg(AppState.currentAppearanceSettings.background_image);
    } else {
      const curBg = document.documentElement.style.getPropertyValue('--bg-img').trim();
      if (curBg) ColorUtils.applyThemeColorFromBg(curBg);
    }
  } else {
    if (AppState.currentAppearanceSettings.font_color) {
      document.documentElement.style.setProperty('--text-custom', AppState.currentAppearanceSettings.font_color);
      document.documentElement.style.setProperty('--text-dim-custom', AppState.currentAppearanceSettings.font_color + 'b3');
    }
  }
  saveAppearanceSettings({ font_color_type: type });
}

function setFontColor(color) {
  AppState.currentAppearanceSettings.font_color = color;
  AppState.currentAppearanceSettings.font_color_type = 'specified';
  document.documentElement.style.setProperty('--text-custom', color);
  document.documentElement.style.setProperty('--text-dim-custom', color + 'b3');
  const bAdapt = $('#btnColorAdaptive');
  const bSpec = $('#btnColorSpecified');
  if (bAdapt) bAdapt.classList.remove('active');
  if (bSpec) bSpec.classList.add('active');
  saveAppearanceSettings({ font_color: color, font_color_type: 'specified' });
}

async function setBgAndSync(v) {
  applyBg(v);
  localStorage.setItem('vproxy_bg', v);
  try {
    await API.settings.put({ background_image: v });
    toast('背景已更换');
    loadAppearance();
  } catch (e) {
    toast('同步背景失败', true);
  }
}

function applyBgUrl() {
  const u = $('#bgUrl')?.value.trim();
  if (!u) return;
  setBgAndSync(`url('${u}')`);
}

async function uploadBg(e) {
  const f = e.target.files?.[0];
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
  API.settings.put({ background_image: DEFAULT_BG }).catch(() => {});
  toast('已恢复默认');
}

async function loadAppearance() {
  try {
    const data = await API.settings.get();
    if (data) {
      AppState.currentAppearanceSettings = data.settings || data;
    }
  } catch (e) {}

  const presetsImg = [
    { name: '默认', val: "url('background.jpg')" },
  ];
  const presetsColor = [
    { name: '克莱因蓝', val: '#002fa7' },
    { name: '暗夜紫', val: '#2e1065' },
    { name: '酒红', val: '#4c0519' },
    { name: '深海绿', val: '#064e3b' },
    { name: '极暗蓝', val: '#0f172a' },
    { name: '琥珀黄', val: '#78350f' },
    { name: '苍穹蓝', val: '#1e3a8a' },
    { name: '焦糖', val: '#7c2d12' },
    { name: '松石绿', val: '#0f766e' },
    { name: '莫兰迪粉', val: '#831843' },
    { name: '纯白', val: '#ffffff' },
    { name: '纯黑', val: '#000000' },
    { name: '银灰', val: '#f3f4f6' },
    { name: '暗灰', val: '#3f3f46' },
  ];

  const curBg = document.documentElement.style.getPropertyValue('--bg-img').trim() || AppState.currentAppearanceSettings.background_image || '';

  const renderThumbs = (presets, containerId, canDelete = false) => {
    const container = $('#' + containerId);
    if (!container) return;
    container.replaceChildren();
    presets.forEach(p => {
      const isImg = p.val.startsWith('url');
      const style = isImg ? `background-image:${p.val}` : `background:${p.val}`;
      const activeClass = (p.val === curBg) ? ' active' : '';
      const thumb = el('div', {
        className: `thumb${activeClass}`,
        style,
        title: p.name,
        onclick: () => setBgAndSync(p.val),
      });
      if (canDelete || p.canDel) {
        const delBtn = el('div', {
          className: 'del-btn',
          title: '删除预设',
          text: '×',
          onclick: (e) => deletePreset(p.val, e),
        });
        thumb.appendChild(delBtn);
      }
      container.appendChild(thumb);
    });
  };

  if (AppState.currentAppearanceSettings.custom_bg_presets && Array.isArray(AppState.currentAppearanceSettings.custom_bg_presets)) {
    const customPresets = AppState.currentAppearanceSettings.custom_bg_presets.map((val, i) => ({ name: '自定义' + (i + 1), val }));
    renderThumbs(customPresets, 'presetsCustom', true);
    const customRow = $('#customPresetsRow');
    if (customRow) customRow.style.display = customPresets.length > 0 ? 'flex' : 'none';
  } else {
    const customRow = $('#customPresetsRow');
    if (customRow) customRow.style.display = 'none';
  }

  try {
    const res = await fetch('/api/admin/list-bgs');
    const data = await res.json();
    if (res.ok && data.ok && data.files) {
      data.files.forEach((f, i) => {
        presetsImg.push({ name: `自定义${i + 1}`, val: `url('/assets/${f}')`, canDel: true });
      });
    }
  } catch (e) {}

  if (curBg && !presetsImg.find(p => p.val === curBg) && !presetsColor.find(p => p.val === curBg)) {
    presetsImg.unshift({ name: '当前', val: curBg });
  }

  renderThumbs(presetsImg, 'presets');
  renderThumbs(presetsColor, 'presetsColor');

  // 初始化字体大小显示
  const fsEl = $('#currentFontSize');
  if (fsEl) fsEl.textContent = AppState.currentAppearanceSettings.font_size || '14px';

  // 初始化字体颜色模式 UI
  if (AppState.currentAppearanceSettings.font_color_type === 'specified' && AppState.currentAppearanceSettings.font_color) {
    const box = $('#fontColorBox');
    if (box) box.style.backgroundColor = AppState.currentAppearanceSettings.font_color;
  }

  renderGradientStops(false);
  setActiveColorTarget('font');

  AppState.pageCache['appearance'] = $('#page-appearance')?.innerHTML || '';
}

// ==========================================
// Advanced Color Palette Logic
// ==========================================

let activeColorTarget = null;
let activeColorIndex = -1;
let currentPaletteHex = '#002fa7';
let currentHSV = [0, 1, 1];
let gradientColors = ['#002fa7', '#2e1065'];

function resetColorTarget() {
  if (activeColorTarget === 'bg') {
    currentPaletteHex = '#002fa7';
  } else if (activeColorTarget === 'font') {
    currentPaletteHex = '#FFFFFF';
  } else {
    currentPaletteHex = '#ffffff';
  }
  syncPaletteUI(currentPaletteHex);
  applyPaletteToTarget(currentPaletteHex);
}

function setActiveColorTarget(target, index = -1, hexColor = null, event = null) {
  if (activeColorTarget === 'bg' && target !== 'bg' && currentPaletteHex) {
    setBgAndSync(currentPaletteHex);
  }

  activeColorTarget = target;
  activeColorIndex = index;

  if (!hexColor) {
    if (target === 'font') {
      hexColor = AppState.currentAppearanceSettings.font_color || '#FFFFFF';
    } else if (target === 'gradient') {
      hexColor = gradientColors[index] || '#FFFFFF';
    } else {
      hexColor = '#002fa7';
    }
  }

  currentPaletteHex = hexColor;
  const palette = document.querySelector('.color-palette-container');
  if (palette && event) {
    palette.classList.add('show');
    if (event.target) {
      const paletteRect = palette.getBoundingClientRect();
      const rect = event.target.getBoundingClientRect();

      let left = rect.right + 24;
      let top = rect.top + (rect.height / 2) - (paletteRect.height / 2);

      if (left + paletteRect.width > window.innerWidth - 16) {
        left = rect.left - paletteRect.width - 24;
      }
      if (left < 16) left = 16;
      if (left + paletteRect.width > window.innerWidth - 16) {
        left = window.innerWidth - paletteRect.width - 16;
      }

      if (top + paletteRect.height > window.innerHeight - 16) {
        top = window.innerHeight - paletteRect.height - 16;
      }
      if (top < 16) top = 16;

      palette.style.top = top + 'px';
      palette.style.left = left + 'px';
      palette.style.bottom = 'auto';
      palette.style.right = 'auto';
    }
  }

  if (target === 'font') {
    $('#fontColorBox')?.classList.add('active');
    document.querySelectorAll('.gradient-stop-box').forEach(e => e.classList.remove('active'));
  } else if (target === 'gradient') {
    $('#fontColorBox')?.classList.remove('active');
    document.querySelectorAll('.gradient-stop-box').forEach((e, i) => {
      e.classList.toggle('active', i === index);
    });
  }

  syncPaletteUI(hexColor);
}

function applyPaletteToTarget(hex) {
  currentPaletteHex = hex;
  const preview = $('#palettePreview');
  if (preview) preview.style.backgroundColor = hex;

  if (activeColorTarget === 'font') {
    const box = $('#fontColorBox');
    if (box) box.style.backgroundColor = hex;
    setFontColor(hex);
  } else if (activeColorTarget === 'gradient') {
    gradientColors[activeColorIndex] = hex;
    renderGradientStops(false);
  } else if (activeColorTarget === 'bg') {
    const bgBox = $('#bgColorBox');
    if (bgBox) bgBox.style.backgroundColor = hex;
    applyBg(hex);
  }
}

function syncPaletteUI(hex) {
  const preview = $('#palettePreview');
  if (preview) preview.style.backgroundColor = hex;
  const hexInput = $('#hex-input');
  if (hexInput) hexInput.value = hex.toUpperCase();

  const [r, g, b] = ColorUtils.hexToRgb(hex);
  const rIn = $('#rgb-r'); if (rIn) rIn.value = r;
  const gIn = $('#rgb-g'); if (gIn) gIn.value = g;
  const bIn = $('#rgb-b'); if (bIn) bIn.value = b;

  const [h, s, v] = ColorUtils.rgbToHsv(r, g, b);
  currentHSV = [h, s, v];

  const hIn = $('#hsb-h'); if (hIn) hIn.value = Math.round(h * 360);
  const sIn = $('#hsb-s'); if (sIn) sIn.value = Math.round(s * 100);
  const vIn = $('#hsb-b'); if (vIn) vIn.value = Math.round(v * 100);

  updatePickersUI();
}

function updatePickersUI() {
  const [h, s, v] = currentHSV;
  const [baseR, baseG, baseB] = ColorUtils.hsvToRgb(h, 1, 1);
  const svPanel = $('#svPanel');
  if (svPanel) svPanel.style.backgroundColor = ColorUtils.rgbToHexStr(baseR, baseG, baseB);

  const svCursor = $('#svCursor');
  if (svCursor) {
    svCursor.style.left = (s * 100) + '%';
    svCursor.style.top = ((1 - v) * 100) + '%';
  }

  const hueCursor = $('#hueCursor');
  if (hueCursor) {
    hueCursor.style.left = (h * 100) + '%';
  }
}

function updateFromRGB() {
  let r = parseInt($('#rgb-r')?.value || '0', 10);
  let g = parseInt($('#rgb-g')?.value || '0', 10);
  let b = parseInt($('#rgb-b')?.value || '0', 10);
  r = Math.max(0, Math.min(255, r));
  g = Math.max(0, Math.min(255, g));
  b = Math.max(0, Math.min(255, b));
  const hex = ColorUtils.rgbToHexStr(r, g, b);
  syncPaletteUI(hex);
  applyPaletteToTarget(hex);
}

function updateFromHSB() {
  let h = parseInt($('#hsb-h')?.value || '0', 10);
  let s = parseInt($('#hsb-s')?.value || '0', 10);
  let v = parseInt($('#hsb-b')?.value || '0', 10);
  h = Math.max(0, Math.min(360, h));
  s = Math.max(0, Math.min(100, s));
  v = Math.max(0, Math.min(100, v));
  const [r, g, b] = ColorUtils.hsvToRgb(h / 360, s / 100, v / 100);
  const hex = ColorUtils.rgbToHexStr(r, g, b);
  syncPaletteUI(hex);
  applyPaletteToTarget(hex);
}

function updateFromHEX() {
  const hex = $('#hex-input')?.value.trim();
  if (/^#[0-9A-Fa-f]{6}$/.test(hex)) {
    syncPaletteUI(hex);
    applyPaletteToTarget(hex);
  }
}

async function activateEyeDropper() {
  if (!window.EyeDropper) {
    toast('您的浏览器不支持吸管工具', true);
    return;
  }
  try {
    const dropper = new window.EyeDropper();
    const result = await dropper.open();
    syncPaletteUI(result.sRGBHex);
    applyPaletteToTarget(result.sRGBHex);
  } catch (e) {}
}

function renderGradientStops(stealFocus = true) {
  const container = $('#gradientStops');
  if (!container) return;
  container.replaceChildren();
  gradientColors.forEach((c, i) => {
    const stopBox = el('div', {
      className: `gradient-stop-box ${activeColorTarget === 'gradient' && activeColorIndex === i ? 'active' : ''}`,
      style: { backgroundColor: c },
      title: '点击编辑色标',
      onclick: (e) => setActiveColorTarget('gradient', i, c, e),
    });
    if (gradientColors.length > 2) {
      stopBox.appendChild(el('button', {
        className: 'del-btn',
        text: '×',
        onclick: (e) => removeGradientStop(e, i),
      }));
    }
    container.appendChild(stopBox);
  });

  if (stealFocus && gradientColors.length > 0) {
    setActiveColorTarget('gradient', gradientColors.length - 1, gradientColors[gradientColors.length - 1]);
  }
}

function removeGradientStop(e, index) {
  e.stopPropagation();
  if (gradientColors.length <= 2) return;
  gradientColors.splice(index, 1);
  if (activeColorTarget === 'gradient' && activeColorIndex === index) {
    activeColorTarget = null;
  } else if (activeColorIndex > index) {
    activeColorIndex--;
  }
  renderGradientStops(false);
}

function addGradientStop() {
  gradientColors.push('#ffffff');
  renderGradientStops(true);
}

function applyGradient() {
  if (gradientColors.length < 2) return;
  const gradStr = `linear-gradient(135deg, ${gradientColors.join(', ')})`;
  setBgAndSync(gradStr);
}

async function saveCustomPreset(type) {
  let val = '';
  if (type === 'color') {
    val = currentPaletteHex || '#002fa7';
  } else if (type === 'gradient') {
    if (gradientColors.length < 2) return;
    val = `linear-gradient(135deg, ${gradientColors.join(', ')})`;
  }
  if (!val) return;

  const customPresets = AppState.currentAppearanceSettings.custom_bg_presets || [];
  if (!customPresets.includes(val)) {
    customPresets.unshift(val);
  }

  try {
    await API.settings.put({ custom_bg_presets: customPresets });
    toast('已保存为自定义预设');
    loadAppearance();
  } catch (e) {
    toast('保存预设失败', true);
  }
}

async function deletePreset(val, event) {
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
        toast('删除失败: ' + e.message, true);
      }
    }
  } else {
    if (AppState.currentAppearanceSettings.custom_bg_presets) {
      AppState.currentAppearanceSettings.custom_bg_presets = AppState.currentAppearanceSettings.custom_bg_presets.filter(p => p !== val);
      try {
        await API.settings.put({ custom_bg_presets: AppState.currentAppearanceSettings.custom_bg_presets });
        toast('预设已删除');
        loadAppearance();
      } catch (e) {
        toast('删除失败: ' + e.message, true);
      }
    }
  }
}

// Global palette drag and dismissal listeners
const getClientXY = (e) => {
  if (e.touches && e.touches.length > 0) return { x: e.touches[0].clientX, y: e.touches[0].clientY };
  if (e.changedTouches && e.changedTouches.length > 0) return { x: e.changedTouches[0].clientX, y: e.changedTouches[0].clientY };
  return { x: e.clientX, y: e.clientY };
};

const svPanel = $('#svPanel');
const hueSlider = $('#hueSlider');

if (svPanel) {
  let isDraggingSV = false;
  const updateSV = (e) => {
    const rect = svPanel.getBoundingClientRect();
    const pos = getClientXY(e);
    const x = Math.max(0, Math.min(1, (pos.x - rect.left) / rect.width));
    const y = Math.max(0, Math.min(1, (pos.y - rect.top) / rect.height));
    currentHSV[1] = x;
    currentHSV[2] = 1 - y;
    const [r, g, b] = ColorUtils.hsvToRgb(currentHSV[0], currentHSV[1], currentHSV[2]);
    const hex = ColorUtils.rgbToHexStr(r, g, b);
    syncPaletteUI(hex);
    applyPaletteToTarget(hex);
  };
  const startSV = (e) => { isDraggingSV = true; updateSV(e); if (e.cancelable !== false) e.preventDefault(); };
  const moveSV = (e) => { if (isDraggingSV) { updateSV(e); if (e.cancelable !== false) e.preventDefault(); } };
  const endSV = () => { isDraggingSV = false; };

  svPanel.addEventListener('mousedown', startSV);
  svPanel.addEventListener('touchstart', startSV, { passive: false });
  document.addEventListener('mousemove', moveSV);
  document.addEventListener('touchmove', moveSV, { passive: false });
  document.addEventListener('mouseup', endSV);
  document.addEventListener('touchend', endSV);
}

if (hueSlider) {
  let isDraggingHue = false;
  const updateHue = (e) => {
    const rect = hueSlider.getBoundingClientRect();
    const pos = getClientXY(e);
    const x = Math.max(0, Math.min(1, (pos.x - rect.left) / rect.width));
    currentHSV[0] = x;
    const [r, g, b] = ColorUtils.hsvToRgb(currentHSV[0], currentHSV[1], currentHSV[2]);
    const hex = ColorUtils.rgbToHexStr(r, g, b);
    syncPaletteUI(hex);
    applyPaletteToTarget(hex);
  };
  const startHue = (e) => { isDraggingHue = true; updateHue(e); if (e.cancelable !== false) e.preventDefault(); };
  const moveHue = (e) => { if (isDraggingHue) { updateHue(e); if (e.cancelable !== false) e.preventDefault(); } };
  const endHue = () => { isDraggingHue = false; };

  hueSlider.addEventListener('mousedown', startHue);
  hueSlider.addEventListener('touchstart', startHue, { passive: false });
  document.addEventListener('mousemove', moveHue);
  document.addEventListener('touchmove', moveHue, { passive: false });
  document.addEventListener('mouseup', endHue);
  document.addEventListener('touchend', endHue);
}

document.addEventListener('click', (e) => {
  const palette = document.querySelector('.color-palette-container');
  if (!palette || !palette.classList.contains('show')) return;
  if (palette.contains(e.target) || e.target.closest('.color-preview-box') || e.target.closest('.gradient-stop-box')) {
    return;
  }
  palette.classList.remove('show');
  if (activeColorTarget === 'bg' && currentPaletteHex) {
    setBgAndSync(currentPaletteHex);
  }
  document.querySelectorAll('.color-preview-box, .gradient-stop-box').forEach(el => el.classList.remove('active'));
  activeColorTarget = null;
});
