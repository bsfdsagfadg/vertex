import { $, DEFAULT_BG, esc, toast } from './utils.js';

let API;
export function configureAppearanceService(service) { API = service; }

let currentAppearanceSettings = {};

export function teardownAppearance() {
  document.querySelector('.color-palette-container')?.classList.remove('show');
  document.querySelectorAll('.color-preview-box, .gradient-stop-box').forEach(el => el.classList.remove('active'));
  activeColorTarget = null;
}

function applyBg(v) { document.documentElement.style.setProperty('--bg-img', v); applyThemeColorFromBg(v); }

async function initBg() {
  try {
    const res = await API.session();
    if (res) currentAppearanceSettings = res;
    
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
export function initAppearance() { return initBg(); }

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

function adjustFontSize(delta) {
  let size = parseInt(currentAppearanceSettings.font_size || '14');
  size += delta;
  if (size < 12) size = 12;
  if (size > 24) size = 24;
  const newSize = size + 'px';
  currentAppearanceSettings.font_size = newSize;
  const fsEl = $('#currentFontSize');
  if (fsEl) fsEl.textContent = newSize;
  document.documentElement.style.setProperty('--base-font-size', newSize);
  saveAppearanceSettings({ font_size: newSize });
}

function setFontColorType(type) {
  currentAppearanceSettings.font_color_type = type;
  if (type === 'adaptive') {
    if (currentAppearanceSettings.background_image) {
      applyThemeColorFromBg(currentAppearanceSettings.background_image);
    } else {
      const curBg = document.documentElement.style.getPropertyValue('--bg-img').trim();
      if (curBg) applyThemeColorFromBg(curBg);
    }
  } else {
    if (currentAppearanceSettings.font_color) {
      document.documentElement.style.setProperty('--text-custom', currentAppearanceSettings.font_color);
      document.documentElement.style.setProperty('--text-dim-custom', currentAppearanceSettings.font_color + 'b3');
    }
  }
  saveAppearanceSettings({ font_color_type: type });
}

function setFontColor(color) {
  currentAppearanceSettings.font_color = color;
  currentAppearanceSettings.font_color_type = 'specified';
  document.documentElement.style.setProperty('--text-custom', color);
  document.documentElement.style.setProperty('--text-dim-custom', color + 'b3');
  saveAppearanceSettings({ font_color: color, font_color_type: 'specified' });
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
    const data = await API.appearance.upload(fd);
    if (data.ok) {
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

export async function loadAppearance() {
  try {
    const data = await API.settings.get();
    if (data) {
      currentAppearanceSettings = data.settings || data;
    }
  } catch (e) {}

  const presetsImg = [
    { name: '默认', val: "url('background.jpg')" }
  ];
  let presetsColor = [
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

  const curBg = document.documentElement.style.getPropertyValue('--bg-img').trim() || currentAppearanceSettings.background_image || '';
  
  const renderThumbs = (presets, containerId, canDelete = false) => {
    const el = $('#' + containerId);
    if (!el) return;
    el.innerHTML = presets.map(p => {
      const isImg = p.val.startsWith('url');
      const style = isImg ? `background-image:${p.val}` : `background:${p.val}`;
      const activeClass = (p.val === curBg) ? ' active' : '';
      const delBtn = canDelete ? `<button type="button" class="del-btn" data-appearance-action="delete-preset" data-bg="${esc(p.val)}" title="删除预设">×</button>` : '';
      return `<div class="thumb${activeClass}" style="${style}" data-appearance-action="set-bg" data-bg="${esc(p.val)}" title="${esc(p.name)}">${delBtn}</div>`;
    }).join('');
  };

  if (currentAppearanceSettings.custom_bg_presets && Array.isArray(currentAppearanceSettings.custom_bg_presets)) {
    const customPresets = currentAppearanceSettings.custom_bg_presets.map((val, i) => ({ name: '自定义' + (i + 1), val }));
    renderThumbs(customPresets, 'presetsCustom', true);
    const customRow = $('#customPresetsRow');
    if (customRow) customRow.style.display = customPresets.length > 0 ? 'flex' : 'none';
  } else {
    const customRow = $('#customPresetsRow');
    if (customRow) customRow.style.display = 'none';
  }
  
  try {
    const data = await API.appearance.listBackgrounds();
    if (data.ok && data.files) {
      data.files.forEach((f, i) => {
        presetsImg.push({ name: `自定义${i+1}`, val: `url('/assets/${f}')`, canDel: true });
      });
    }
  } catch (e) {}

  if (curBg && !presetsImg.find(p => p.val === curBg) && !presetsColor.find(p => p.val === curBg)) {
    presetsImg.unshift({ name: '当前', val: curBg });
  }
  
  const elImg = $('#presets');
  if (elImg) {
    elImg.innerHTML = presetsImg.map(p => {
      const isImg = p.val.startsWith('url');
      const style = isImg ? `background-image:${p.val}` : `background:${p.val}`;
      const activeClass = (p.val === curBg) ? ' active' : '';
      const delBtn = p.canDel ? `<button type="button" class="del-btn" data-appearance-action="delete-preset" data-bg="${esc(p.val)}" title="删除图片">×</button>` : '';
      return `<div class="thumb${activeClass}" style="${style}" data-appearance-action="set-bg" data-bg="${esc(p.val)}" title="${esc(p.name)}">${delBtn}</div>`;
    }).join('');
  }
  renderThumbs(presetsColor, 'presetsColor');

  // 初始化字体大小显示
  const fsEl = $('#currentFontSize');
  if (fsEl) fsEl.textContent = currentAppearanceSettings.font_size || '14px';
  
  // 初始化字体颜色模式 UI
  if (currentAppearanceSettings.font_color_type === 'specified' && currentAppearanceSettings.font_color) {
    const box = $('#fontColorBox');
    if (box) box.style.backgroundColor = currentAppearanceSettings.font_color;
  }
  
  renderGradientStops(false);
  setActiveColorTarget('font');
  
}

function applyThemeColorFromBg(bgValue) {
  const match = bgValue.match(/url\(['"]?(.*?)['"]?\)/);
  const src = match ? match[1] : null;
  const canvas = document.createElement("canvas");
  const ctx = canvas.getContext("2d", { willReadFrequently: true });
  canvas.width = 64; canvas.height = 64;
  if (src) {
    const img = new Image();
    if (src.startsWith('http') && !src.startsWith(location.origin)) {
      img.crossOrigin = "Anonymous";
    }
    img.onload = () => { ctx.drawImage(img, 0, 0, 64, 64); extractAndSetColors(ctx); };
    img.src = src;
  } else if (bgValue.includes('gradient')) {
    // extract hex/rgb from gradient string to average them
    const matches = bgValue.match(/#[0-9a-fA-F]{3,6}/g);
    let avgR = 0, avgG = 0, avgB = 0, count = 0;
    if (matches) {
      matches.forEach(m => {
        if (m.length === 4) m = '#' + m[1]+m[1]+m[2]+m[2]+m[3]+m[3];
        avgR += parseInt(m.slice(1,3), 16);
        avgG += parseInt(m.slice(3,5), 16);
        avgB += parseInt(m.slice(5,7), 16);
        count++;
      });
      if (count > 0) {
        ctx.fillStyle = `rgb(${Math.round(avgR/count)}, ${Math.round(avgG/count)}, ${Math.round(avgB/count)})`;
      }
    }
    ctx.fillRect(0, 0, 64, 64);
    extractAndSetColors(ctx);
  } else {
    ctx.fillStyle = bgValue;
    ctx.fillRect(0, 0, 64, 64);
    extractAndSetColors(ctx);
  }
}

function extractAndSetColors(ctx) {
  const data = ctx.getImageData(0, 0, 64, 64).data;
  let sumR = 0, sumG = 0, sumB = 0, totalWeight = 0;
  
  for (let i = 0; i < data.length; i += 16) {
    let r = data[i], g = data[i+1], b = data[i+2];
    let [h, s, l] = rgbToHsl(r, g, b);
    let weight = s * (1 - Math.abs(2 * l - 1));
    weight = Math.max(weight, 0.02);
    sumR += r * weight; sumG += g * weight; sumB += b * weight;
    totalWeight += weight;
  }
  
  let r = 0, g = 0, b = 0;
  if (totalWeight > 0) {
    r = sumR / totalWeight; g = sumG / totalWeight; b = sumB / totalWeight;
  }
  
  let [h, s, l] = rgbToHsl(r, g, b);
  if (s < 0.25) s = 0.5; 
  if (l < 0.4) l = 0.6; 
  if (l > 0.7) l = 0.55; 
  
  const c1 = hslToHex(h, s, l);
  const c2 = hslToHex(h, s, Math.max(l - 0.18, 0.25));
  
  const toRgbString = (hex) => {
    let r = parseInt(hex.slice(1,3), 16);
    let g = parseInt(hex.slice(3,5), 16);
    let b = parseInt(hex.slice(5,7), 16);
    return `${r}, ${g}, ${b}`;
  };
  
  const rgb1 = toRgbString(c1);
  const rgb2 = toRgbString(c2);
  
  document.documentElement.style.setProperty("--gold", c1);
  document.documentElement.style.setProperty("--gold-rgb", rgb1);
  document.documentElement.style.setProperty("--gold-deep", c2);
  document.documentElement.style.setProperty("--gold-soft", `rgba(${rgb1}, 0.15)`);
  
  if (currentAppearanceSettings.font_color_type !== 'specified') {
    const luminance = 0.299 * r + 0.587 * g + 0.114 * b;
    if (luminance > 140) {
      document.documentElement.style.setProperty('--text-custom', '#2c1d08');
      document.documentElement.style.setProperty('--text-dim-custom', 'rgba(44, 29, 8, 0.7)');
    } else {
      document.documentElement.style.setProperty('--text-custom', '#f6f1e9');
      document.documentElement.style.setProperty('--text-dim-custom', 'rgba(246, 241, 233, 0.7)');
    }
  } else if (currentAppearanceSettings.font_color) {
    document.documentElement.style.setProperty('--text-custom', currentAppearanceSettings.font_color);
    document.documentElement.style.setProperty('--text-dim-custom', currentAppearanceSettings.font_color + 'b3');
  }

  document.documentElement.style.setProperty("--gold-shadow1", `rgba(${rgb1}, 0.3)`);
  document.documentElement.style.setProperty("--gold-shadow2", `rgba(${rgb1}, 0.42)`);
  
  // Create a complementary blueish color for the second veil based on the dominant hue
  let blueH = (h + 0.5) % 1;
  const cBlue = hslToHex(blueH, s, l);
  const rgbBlue = toRgbString(cBlue);
  document.documentElement.style.setProperty("--blue-soft", `rgba(${rgbBlue}, 0.1)`);
  
  // Background panels adaptive colors (dark theme)
  const glassHex = hslToHex(h, Math.min(s, 0.2), 0.11);
  const veilHex = hslToHex(h, Math.min(s, 0.25), 0.06);
  const veilDarkHex = hslToHex(h, Math.min(s, 0.25), 0.04);
  const strokeHex = hslToHex(h, Math.min(s, 0.4), 0.95);
  
  const glassRgb = toRgbString(glassHex);
  const veilRgb = toRgbString(veilHex);
  const veilDarkRgb = toRgbString(veilDarkHex);
  const strokeRgb = toRgbString(strokeHex);
  
  document.documentElement.style.setProperty("--glass", `rgba(${glassRgb}, 0.38)`);
  document.documentElement.style.setProperty("--glass-solid", `rgba(${glassRgb}, 0.68)`);
  document.documentElement.style.setProperty("--veil-light", `rgba(${veilRgb}, 0.42)`);
  document.documentElement.style.setProperty("--veil-dark", `rgba(${veilDarkRgb}, 0.62)`);
  document.documentElement.style.setProperty("--stroke", `rgba(${strokeRgb}, 0.14)`);
}

function rgbToHsl(r, g, b) {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  let h, s, l = (max + min) / 2;
  if (max === min) { h = s = 0; }
  else {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    switch (max) {
      case r: h = (g - b) / d + (g < b ? 6 : 0); break;
      case g: h = (b - r) / d + 2; break;
      case b: h = (r - g) / d + 4; break;
    }
    h /= 6;
  }
  return [h, s, l];
}

function hslToHex(h, s, l) {
  let r, g, b;
  if (s === 0) { r = g = b = l; }
  else {
    const hue2rgb = (p, q, t) => {
      if(t < 0) t += 1; if(t > 1) t -= 1;
      if(t < 1/6) return p + (q - p) * 6 * t;
      if(t < 1/2) return q;
      if(t < 2/3) return p + (q - p) * (2/3 - t) * 6;
      return p;
    };
    const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
    const p = 2 * l - q;
    r = hue2rgb(p, q, h + 1/3); g = hue2rgb(p, q, h); b = hue2rgb(p, q, h - 1/3);
  }
  const toHex = x => {
    const hex = Math.round(x * 255).toString(16);
    return hex.length === 1 ? "0" + hex : hex;
  };
  return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
}



// ==========================================
// Advanced Color Palette Logic
// ==========================================

let activeColorTarget = null; // 'font', 'gradient-0', etc.
let activeColorIndex = -1; // for gradients
let currentPaletteHex = '#002fa7';

function hslToRgb(h, s, l) {
  let r, g, b;
  if(s === 0) {
    r = g = b = l; // achromatic
  } else {
    const hue2rgb = (p, q, t) => {
      if(t < 0) t += 1;
      if(t > 1) t -= 1;
      if(t < 1/6) return p + (q - p) * 6 * t;
      if(t < 1/2) return q;
      if(t < 2/3) return p + (q - p) * (2/3 - t) * 6;
      return p;
    };
    const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
    const p = 2 * l - q;
    r = hue2rgb(p, q, h + 1/3);
    g = hue2rgb(p, q, h);
    b = hue2rgb(p, q, h - 1/3);
  }
  return [Math.round(r * 255), Math.round(g * 255), Math.round(b * 255)];
}

function hexToRgb(hex) {
  hex = hex.replace(/^#/, '');
  if (hex.length === 3) hex = hex.split('').map(c => c+c).join('');
  const num = parseInt(hex, 16);
  if (isNaN(num)) return [0,0,0];
  return [num >> 16, (num >> 8) & 255, num & 255];
}

function rgbToHexStr(r, g, b) {
  return '#' + [r, g, b].map(x => {
    const hex = x.toString(16);
    return hex.length === 1 ? '0' + hex : hex;
  }).join('').toUpperCase();
}

function rgbToHsv(r, g, b) {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const v = max;
  const d = max - min;
  const s = max === 0 ? 0 : d / max;
  let h = 0;
  if (max !== min) {
    switch (max) {
      case r: h = (g - b) / d + (g < b ? 6 : 0); break;
      case g: h = (b - r) / d + 2; break;
      case b: h = (r - g) / d + 4; break;
    }
    h /= 6;
  }
  return [h, s, v];
}

function hsvToRgb(h, s, v) {
  let r, g, b;
  const i = Math.floor(h * 6);
  const f = h * 6 - i;
  const p = v * (1 - s);
  const q = v * (1 - f * s);
  const t = v * (1 - (1 - f) * s);
  switch (i % 6) {
    case 0: r = v, g = t, b = p; break;
    case 1: r = q, g = v, b = p; break;
    case 2: r = p, g = v, b = t; break;
    case 3: r = p, g = q, b = v; break;
    case 4: r = t, g = p, b = v; break;
    case 5: r = v, g = p, b = q; break;
  }
  return [Math.round(r * 255), Math.round(g * 255), Math.round(b * 255)];
}

let currentHSV = [0, 1, 1]; // h, s, v [0-1]

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
      hexColor = currentAppearanceSettings.font_color || '#FFFFFF';
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
      
      // Horizontal constraints
      if (left + paletteRect.width > window.innerWidth - 16) {
        left = rect.left - paletteRect.width - 24; // flip to left
      }
      if (left < 16) left = 16;
      if (left + paletteRect.width > window.innerWidth - 16) {
        left = window.innerWidth - paletteRect.width - 16;
      }
      
      // Vertical constraints
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
    $('#fontColorBox').classList.add('active');
    document.querySelectorAll('.gradient-stop-box').forEach(el => el.classList.remove('active'));
  } else if (target === 'gradient') {
    $('#fontColorBox').classList.remove('active');
    document.querySelectorAll('.gradient-stop-box').forEach((el, i) => {
      el.classList.toggle('active', i === index);
    });
  }
  
  syncPaletteUI(hexColor);
}

function applyPaletteToTarget(hex) {
  currentPaletteHex = hex;
  $('#palettePreview').style.backgroundColor = hex;
  
  if (activeColorTarget === 'font') {
    $('#fontColorBox').style.backgroundColor = hex;
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
  $('#palettePreview').style.backgroundColor = hex;
  $('#hex-input').value = hex.toUpperCase();
  
  const [r, g, b] = hexToRgb(hex);
  $('#rgb-r').value = r; $('#rgb-g').value = g; $('#rgb-b').value = b;
  
  const [h, s, v] = rgbToHsv(r, g, b);
  currentHSV = [h, s, v];
  
  const hDeg = Math.round(h * 360);
  const sPct = Math.round(s * 100);
  const vPct = Math.round(v * 100);
  
  $('#hsb-h').value = hDeg; $('#hsb-s').value = sPct; $('#hsb-b').value = vPct;
  
  updatePickersUI();
}

function updatePickersUI() {
  const [h, s, v] = currentHSV;
  const [baseR, baseG, baseB] = hsvToRgb(h, 1, 1);
  $('#svPanel').style.backgroundColor = rgbToHexStr(baseR, baseG, baseB);
  
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
  let r = parseInt($('#rgb-r').value) || 0;
  let g = parseInt($('#rgb-g').value) || 0;
  let b = parseInt($('#rgb-b').value) || 0;
  if(r<0) r=0; if(r>255) r=255;
  if(g<0) g=0; if(g>255) g=255;
  if(b<0) b=0; if(b>255) b=255;
  const hex = rgbToHexStr(r, g, b);
  syncPaletteUI(hex);
  applyPaletteToTarget(hex);
}

function updateFromHSB() {
  let h = parseInt($('#hsb-h').value) || 0;
  let s = parseInt($('#hsb-s').value) || 0;
  let v = parseInt($('#hsb-b').value) || 0;
  if(h<0) h=0; if(h>360) h=360;
  if(s<0) s=0; if(s>100) s=100;
  if(v<0) v=0; if(v>100) v=100;
  const [r, g, b] = hsvToRgb(h/360, s/100, v/100);
  const hex = rgbToHexStr(r, g, b);
  syncPaletteUI(hex);
  applyPaletteToTarget(hex);
}

function updateFromHEX() {
  let hex = $('#hex-input').value.trim();
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
    const dropper = new EyeDropper();
    const result = await dropper.open();
    syncPaletteUI(result.sRGBHex);
    applyPaletteToTarget(result.sRGBHex);
  } catch (e) {
  }
}

// Panel Interactions
const svPanel = $('#svPanel');
const hueSlider = $('#hueSlider');
const palette = $('#colorPaletteContainer');

const getClientXY = (e) => {
  if (e.touches && e.touches.length > 0) return { x: e.touches[0].clientX, y: e.touches[0].clientY };
  if (e.changedTouches && e.changedTouches.length > 0) return { x: e.changedTouches[0].clientX, y: e.changedTouches[0].clientY };
  return { x: e.clientX, y: e.clientY };
};

if (svPanel) {
  let isDraggingSV = false;
  const updateSV = (e) => {
    const rect = svPanel.getBoundingClientRect();
    const pos = getClientXY(e);
    let x = Math.max(0, Math.min(1, (pos.x - rect.left) / rect.width));
    let y = Math.max(0, Math.min(1, (pos.y - rect.top) / rect.height));
    currentHSV[1] = x;
    currentHSV[2] = 1 - y;
    const [r, g, b] = hsvToRgb(currentHSV[0], currentHSV[1], currentHSV[2]);
    const hex = rgbToHexStr(r, g, b);
    syncPaletteUI(hex);
    applyPaletteToTarget(hex);
  };
  const startSV = (e) => { isDraggingSV = true; updateSV(e); if(e.cancelable !== false) e.preventDefault(); };
  const moveSV = (e) => { if (isDraggingSV) { updateSV(e); if(e.cancelable !== false) e.preventDefault(); } };
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
    let x = Math.max(0, Math.min(1, (pos.x - rect.left) / rect.width));
    currentHSV[0] = x;
    const [r, g, b] = hsvToRgb(currentHSV[0], currentHSV[1], currentHSV[2]);
    const hex = rgbToHexStr(r, g, b);
    syncPaletteUI(hex);
    applyPaletteToTarget(hex);
  };
  const startHue = (e) => { isDraggingHue = true; updateHue(e); if(e.cancelable !== false) e.preventDefault(); };
  const moveHue = (e) => { if (isDraggingHue) { updateHue(e); if(e.cancelable !== false) e.preventDefault(); } };
  const endHue = () => { isDraggingHue = false; };
  
  hueSlider.addEventListener('mousedown', startHue);
  hueSlider.addEventListener('touchstart', startHue, { passive: false });
  document.addEventListener('mousemove', moveHue);
  document.addEventListener('touchmove', moveHue, { passive: false });
  document.addEventListener('mouseup', endHue);
  document.addEventListener('touchend', endHue);
}

if (palette) {
  let isDraggingContainer = false, dragOffsetX = 0, dragOffsetY = 0;
  const startDrag = (e) => {
    if (e.target.closest('button, input, .sv-panel, .hue-slider, .palette-preview, .input-row')) {
      if (!e.target.closest('.drag-icon')) return;
    }
    isDraggingContainer = true;
    const pos = getClientXY(e);
    dragOffsetX = pos.x - palette.offsetLeft;
    dragOffsetY = pos.y - palette.offsetTop;
    if(e.cancelable !== false) e.preventDefault();
  };
  const doDrag = (e) => {
    if (!isDraggingContainer) return;
    const pos = getClientXY(e);
    palette.style.left = (pos.x - dragOffsetX) + 'px';
    palette.style.top = (pos.y - dragOffsetY) + 'px';
    palette.style.bottom = 'auto';
    palette.style.right = 'auto';
    if(e.cancelable !== false) e.preventDefault();
  };
  const stopDrag = () => {
    isDraggingContainer = false;
  };
  
  palette.addEventListener('mousedown', startDrag);
  palette.addEventListener('touchstart', startDrag, { passive: false });
  document.addEventListener('mousemove', doDrag);
  document.addEventListener('touchmove', doDrag, { passive: false });
  document.addEventListener('mouseup', stopDrag);
  document.addEventListener('touchend', stopDrag);
}

function renderGradientStops(stealFocus = true) {
  const container = $('#gradientStops');
  if (!container) return;
  container.innerHTML = gradientColors.map((c, i) => `
    <div class="gradient-stop-box ${activeColorTarget === 'gradient' && activeColorIndex === i ? 'active' : ''}" 
         style="background-color: ${c}" 
         data-appearance-action="edit-gradient" data-index="${i}" data-color="${c}"
         title="点击编辑色标">
      ${gradientColors.length > 2 ? `<button type="button" class="del-btn" data-appearance-action="remove-gradient" data-index="${i}">×</button>` : ''}
    </div>
  `).join('');
  
  if (stealFocus && gradientColors.length > 0) {
    setActiveColorTarget('gradient', gradientColors.length - 1, gradientColors[gradientColors.length - 1]);
  }
}

function removeGradientStop(e, index) {
  e.stopPropagation();
  if (gradientColors.length <= 2) return;
  gradientColors.splice(index, 1);
  if (activeColorTarget === 'gradient' && activeColorIndex === index) {
    activeColorTarget = null; // deselect
  } else if (activeColorIndex > index) {
    activeColorIndex--;
  }
  renderGradientStops(false);
}

let gradientColors = ['#002fa7', '#2e1065'];
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
  
  let customPresets = currentAppearanceSettings.custom_bg_presets || [];
  if (!customPresets.includes(val)) {
    customPresets.unshift(val); // Add to beginning
  }
  
  try {
    await API.settings.put({ custom_bg_presets: customPresets });
    toast('已保存为自定义预设');
    loadAppearance(); // Refresh presets display
  } catch (e) {
    toast('保存预设失败', true);
  }
}

document.addEventListener('click', (e) => {
  const palette = document.querySelector('.color-palette-container');
  if (!palette) return;
  if (!palette.classList.contains('show')) return;
  
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

async function deletePreset(val, event) {
  if (event) event.stopPropagation();
  if (!confirm('确定要删除这个预设吗？')) return;

  if (val.startsWith('url')) {
    const match = val.match(/\/assets\/(background.*?\.jpg)/);
    if (match && match[1]) {
      try {
        const res = await API.appearance.deleteBackground(match[1]);
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
}

document.getElementById('page-appearance').addEventListener('click', event => {
  const target = event.target.closest('[data-appearance-action]');
  if (!target) return;
  const action = target.dataset.appearanceAction;
  if (action === 'apply-url') applyBgUrl();
  else if (action === 'choose-file') document.getElementById('bgFile').click();
  else if (action === 'reset-bg') resetBg();
  else if (action === 'edit-bg') setActiveColorTarget('bg', -1, null, event);
  else if (action === 'save-color') saveCustomPreset('color');
  else if (action === 'add-gradient') addGradientStop();
  else if (action === 'apply-gradient') applyGradient();
  else if (action === 'save-gradient') saveCustomPreset('gradient');
  else if (action === 'font-smaller') adjustFontSize(-1);
  else if (action === 'font-larger') adjustFontSize(1);
  else if (action === 'font-adaptive') setFontColorType('adaptive');
  else if (action === 'font-specified') setFontColorType('specified');
  else if (action === 'edit-font') setActiveColorTarget('font', -1, null, event);
  else if (action === 'eyedropper') activateEyeDropper();
  else if (action === 'reset-color') resetColorTarget();
  else if (action === 'set-bg') setBgAndSync(target.dataset.bg);
  else if (action === 'delete-preset') deletePreset(target.dataset.bg, event);
  else if (action === 'edit-gradient') setActiveColorTarget('gradient', Number(target.dataset.index), target.dataset.color, event);
  else if (action === 'remove-gradient') removeGradientStop(event, Number(target.dataset.index));
});

document.getElementById('page-appearance').addEventListener('input', event => {
  if (event.target.id === 'hex-input') updateFromHEX();
  else if (event.target.id.startsWith('rgb-')) updateFromRGB();
  else if (event.target.id.startsWith('hsb-')) updateFromHSB();
});

document.getElementById('bgFile').addEventListener('change', uploadBg);
