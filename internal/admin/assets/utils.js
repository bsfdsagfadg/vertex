// ==========================================
// Vertex AI Proxy Admin - Core Utilities & State
// ==========================================

const AppState = {
  // Navigation & Dirty State
  curPage: null,
  hasUnsavedSettings: false,
  pageCache: {},

  // Selection Sets
  selectedNodeURIs: new Set(),
  selectedProxyURIs: new Set(),

  // Pagination & Cached Lists
  curNodePage: 1,
  nodePageSize: 50,
  totalNodePages: 1,
  cachedNodesList: [],
  cachedProxyCandidates: [],
  cachedEntryProxyURIs: new Set(),
  curProxyPage: 1,
  proxyPageSize: 10,
  totalProxyPages: 1,

  // Settings & Models
  curSettings: {},
  currentAppearanceSettings: {},
  modelRows: [],
  modelAliasMap: {},
  modelGlobalSettings: { fake_stream_enabled: true, model_turn_guard_enabled: true },
  modelImportMode: 'models',

  // Active Timers
  timers: {
    test: null,
    nodeProgress: null,
    proxyProgress: null,
    logsRefresh: null,
    appearanceSave: null,
    subPollToken: 0,
  },

  clearTimer(name) {
    if (this.timers[name]) {
      clearInterval(this.timers[name]);
      this.timers[name] = null;
    }
  },

  setTimer(name, timer) {
    this.clearTimer(name);
    this.timers[name] = timer;
  },

  markDirty(dirty = true) {
    this.hasUnsavedSettings = dirty;
  },

  resetSelections() {
    this.selectedNodeURIs.clear();
    this.selectedProxyURIs.clear();
  },
};

// Global backward-compatibility accessors
window.AppState = AppState;
try {
  Object.defineProperty(window, 'hasUnsavedSettings', {
    get: () => AppState.hasUnsavedSettings,
    set: (v) => { AppState.hasUnsavedSettings = !!v; },
    configurable: true,
  });
  Object.defineProperty(window, 'selectedNodeURIs', {
    get: () => AppState.selectedNodeURIs,
    set: (v) => { AppState.selectedNodeURIs = v; },
    configurable: true,
  });
  Object.defineProperty(window, 'selectedProxyURIs', {
    get: () => AppState.selectedProxyURIs,
    set: (v) => { AppState.selectedProxyURIs = v; },
    configurable: true,
  });
} catch (e) {}

// DOM Selectors
function $(s) { return document.querySelector(s); }
function $$(s) { return document.querySelectorAll(s); }

// Toast notifications
function toast(msg, isError) {
  const t = $('#toast');
  if (!t) return;
  t.textContent = msg;
  if (isError) {
    t.classList.add('error');
  } else {
    t.classList.remove('error');
  }
  t.classList.add('show');
  setTimeout(() => t.classList.remove('show'), 1900);
}

// HTML string escaping
function esc(s) {
  if (s === null || s === undefined) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  }[c]));
}

// Safe DOM Element Builder
function el(tag, attrs = {}, children = []) {
  const elem = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'className' || k === 'class') {
      elem.className = v;
    } else if (k === 'style' && typeof v === 'object') {
      Object.assign(elem.style, v);
    } else if (k === 'textContent' || k === 'text') {
      elem.textContent = v;
    } else if (k.startsWith('on') && typeof v === 'function') {
      elem.addEventListener(k.slice(2).toLowerCase(), v);
    } else if (k.startsWith('data-')) {
      elem.setAttribute(k, v);
    } else if (typeof v === 'boolean') {
      if (v) elem.setAttribute(k, '');
    } else if (v !== undefined && v !== null) {
      elem.setAttribute(k, v);
    }
  }
  if (Array.isArray(children)) {
    children.forEach(c => {
      if (!c) return;
      if (typeof c === 'string' || typeof c === 'number') {
        elem.appendChild(document.createTextNode(String(c)));
      } else if (c instanceof Node) {
        elem.appendChild(c);
      }
    });
  } else if (typeof children === 'string' || typeof children === 'number') {
    elem.textContent = String(children);
  } else if (children instanceof Node) {
    elem.appendChild(children);
  }
  return elem;
}

const _tmap = {
  vless: 'VLESS',
  vmess: 'VMess',
  trojan: 'Trojan',
  ss: 'Shadowsocks',
  shadowsocks: 'Shadowsocks',
  hysteria2: 'Hysteria2',
  hy2: 'Hysteria2',
  tuic: 'TUIC',
};

const DEFAULT_BG = "url('background.jpg')";

// Modal dialog confirm helper
function showConfirm(msg, onOk, onCancel, onSave) {
  const m = $('#confirmModal');
  if (!m) return;
  const textEl = $('#confirmModalText');
  if (textEl) {
    textEl.innerHTML = esc(msg).replace(/\n/g, '<br>');
  }
  m.classList.remove('hidden');

  const okBtn = $('#confirmOkBtn');
  const cancelBtn = $('#confirmCancelBtn');
  const saveBtn = $('#confirmSaveBtn');

  if (onSave) {
    if (saveBtn) saveBtn.classList.remove('hidden');
  } else if (saveBtn) {
    saveBtn.classList.add('hidden');
  }

  const cleanup = () => {
    m.classList.add('hidden');
    if (okBtn) okBtn.onclick = null;
    if (cancelBtn) cancelBtn.onclick = null;
    if (saveBtn) saveBtn.onclick = null;
  };

  if (okBtn) okBtn.onclick = () => { cleanup(); if (onOk) onOk(); };
  if (cancelBtn) cancelBtn.onclick = () => { cleanup(); if (onCancel) onCancel(); };
  if (saveBtn) saveBtn.onclick = () => { cleanup(); if (onSave) onSave(); };
}

// ==========================================
// Standalone Color & Theme Utilities
// ==========================================

const ColorUtils = {
  rgbToHsl(r, g, b) {
    r /= 255; g /= 255; b /= 255;
    const max = Math.max(r, g, b), min = Math.min(r, g, b);
    let h = 0, s = 0, l = (max + min) / 2;
    if (max !== min) {
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
  },

  hslToHex(h, s, l) {
    let r, g, b;
    if (s === 0) {
      r = g = b = l;
    } else {
      const hue2rgb = (p, q, t) => {
        if (t < 0) t += 1;
        if (t > 1) t -= 1;
        if (t < 1/6) return p + (q - p) * 6 * t;
        if (t < 1/2) return q;
        if (t < 2/3) return p + (q - p) * (2/3 - t) * 6;
        return p;
      };
      const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
      const p = 2 * l - q;
      r = hue2rgb(p, q, h + 1/3);
      g = hue2rgb(p, q, h);
      b = hue2rgb(p, q, h - 1/3);
    }
    const toHex = x => {
      const hex = Math.round(x * 255).toString(16);
      return hex.length === 1 ? '0' + hex : hex;
    };
    return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
  },

  hslToRgb(h, s, l) {
    let r, g, b;
    if (s === 0) {
      r = g = b = l;
    } else {
      const hue2rgb = (p, q, t) => {
        if (t < 0) t += 1;
        if (t > 1) t -= 1;
        if (t < 1/6) return p + (q - p) * 6 * t;
        if (t < 1/2) return q;
        if (t < 2/3) return p + (q - p) * (2/3 - t) * 6;
        return p;
      };
      const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
      const p = 2 * l - q;
      r = hue2rgb(p, q, h + 1/3);
      g = hue2rgb(p, q, h);
      b = hue2rgb(p, q, h - 1/3);
    }
    return [Math.round(r * 255), Math.round(g * 255), Math.round(b * 255)];
  },

  hexToRgb(hex) {
    hex = hex.replace(/^#/, '');
    if (hex.length === 3) hex = hex.split('').map(c => c + c).join('');
    const num = parseInt(hex, 16);
    if (isNaN(num)) return [0, 0, 0];
    return [num >> 16, (num >> 8) & 255, num & 255];
  },

  rgbToHexStr(r, g, b) {
    return '#' + [r, g, b].map(x => {
      const hex = Math.max(0, Math.min(255, Math.round(x))).toString(16);
      return hex.length === 1 ? '0' + hex : hex;
    }).join('').toUpperCase();
  },

  rgbToHsv(r, g, b) {
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
  },

  hsvToRgb(h, s, v) {
    let r, g, b;
    const i = Math.floor(h * 6);
    const f = h * 6 - i;
    const p = v * (1 - s);
    const q = v * (1 - f * s);
    const t = v * (1 - (1 - f) * s);
    switch (i % 6) {
      case 0: r = v; g = t; b = p; break;
      case 1: r = q; g = v; b = p; break;
      case 2: r = p; g = v; b = t; break;
      case 3: r = p; g = q; b = v; break;
      case 4: r = t; g = p; b = v; break;
      case 5: r = v; g = p; b = q; break;
      default: r = v; g = t; b = p; break;
    }
    return [Math.round(r * 255), Math.round(g * 255), Math.round(b * 255)];
  },

  applyThemeColorFromBg(bgValue) {
    const match = bgValue.match(/url\(['"]?(.*?)['"]?\)/);
    const src = match ? match[1] : null;
    const canvas = document.createElement('canvas');
    const ctx = canvas.getContext('2d', { willReadFrequently: true });
    canvas.width = 64; canvas.height = 64;
    if (src) {
      const img = new Image();
      if (src.startsWith('http') && !src.startsWith(location.origin)) {
        img.crossOrigin = 'Anonymous';
      }
      img.onload = () => {
        ctx.drawImage(img, 0, 0, 64, 64);
        ColorUtils.extractAndSetColors(ctx);
      };
      img.src = src;
    } else if (bgValue.includes('gradient')) {
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
      ColorUtils.extractAndSetColors(ctx);
    } else {
      ctx.fillStyle = bgValue;
      ctx.fillRect(0, 0, 64, 64);
      ColorUtils.extractAndSetColors(ctx);
    }
  },

  extractAndSetColors(ctx) {
    const data = ctx.getImageData(0, 0, 64, 64).data;
    let sumR = 0, sumG = 0, sumB = 0, totalWeight = 0;

    for (let i = 0; i < data.length; i += 16) {
      const r = data[i], g = data[i+1], b = data[i+2];
      const [h, s, l] = ColorUtils.rgbToHsl(r, g, b);
      let weight = s * (1 - Math.abs(2 * l - 1));
      weight = Math.max(weight, 0.02);
      sumR += r * weight; sumG += g * weight; sumB += b * weight;
      totalWeight += weight;
    }

    let r = 0, g = 0, b = 0;
    if (totalWeight > 0) {
      r = sumR / totalWeight; g = sumG / totalWeight; b = sumB / totalWeight;
    }

    let [h, s, l] = ColorUtils.rgbToHsl(r, g, b);
    if (s < 0.25) s = 0.5;
    if (l < 0.4) l = 0.6;
    if (l > 0.7) l = 0.55;

    const c1 = ColorUtils.hslToHex(h, s, l);
    const c2 = ColorUtils.hslToHex(h, s, Math.max(l - 0.18, 0.25));

    const toRgbString = (hex) => {
      const r = parseInt(hex.slice(1,3), 16);
      const g = parseInt(hex.slice(3,5), 16);
      const b = parseInt(hex.slice(5,7), 16);
      return `${r}, ${g}, ${b}`;
    };

    const rgb1 = toRgbString(c1);
    const rgb2 = toRgbString(c2);

    document.documentElement.style.setProperty('--gold', c1);
    document.documentElement.style.setProperty('--gold-rgb', rgb1);
    document.documentElement.style.setProperty('--gold-deep', c2);
    document.documentElement.style.setProperty('--gold-soft', `rgba(${rgb1}, 0.15)`);

    const appearanceSettings = AppState.currentAppearanceSettings || {};
    if (appearanceSettings.font_color_type !== 'specified') {
      const luminance = 0.299 * r + 0.587 * g + 0.114 * b;
      if (luminance > 140) {
        document.documentElement.style.setProperty('--text-custom', '#2c1d08');
        document.documentElement.style.setProperty('--text-dim-custom', 'rgba(44, 29, 8, 0.7)');
      } else {
        document.documentElement.style.setProperty('--text-custom', '#f6f1e9');
        document.documentElement.style.setProperty('--text-dim-custom', 'rgba(246, 241, 233, 0.7)');
      }
    } else if (appearanceSettings.font_color) {
      document.documentElement.style.setProperty('--text-custom', appearanceSettings.font_color);
      document.documentElement.style.setProperty('--text-dim-custom', appearanceSettings.font_color + 'b3');
    }

    document.documentElement.style.setProperty('--gold-shadow1', `rgba(${rgb1}, 0.3)`);
    document.documentElement.style.setProperty('--gold-shadow2', `rgba(${rgb1}, 0.42)`);

    const blueH = (h + 0.5) % 1;
    const cBlue = ColorUtils.hslToHex(blueH, s, l);
    const rgbBlue = toRgbString(cBlue);
    document.documentElement.style.setProperty('--blue-soft', `rgba(${rgbBlue}, 0.1)`);

    const glassHex = ColorUtils.hslToHex(h, Math.min(s, 0.2), 0.11);
    const veilHex = ColorUtils.hslToHex(h, Math.min(s, 0.25), 0.06);
    const veilDarkHex = ColorUtils.hslToHex(h, Math.min(s, 0.25), 0.04);
    const strokeHex = ColorUtils.hslToHex(h, Math.min(s, 0.4), 0.95);

    const glassRgb = toRgbString(glassHex);
    const veilRgb = toRgbString(veilHex);
    const veilDarkRgb = toRgbString(veilDarkHex);
    const strokeRgb = toRgbString(strokeHex);

    document.documentElement.style.setProperty('--glass', `rgba(${glassRgb}, 0.38)`);
    document.documentElement.style.setProperty('--glass-solid', `rgba(${glassRgb}, 0.68)`);
    document.documentElement.style.setProperty('--veil-light', `rgba(${veilRgb}, 0.42)`);
    document.documentElement.style.setProperty('--veil-dark', `rgba(${veilDarkRgb}, 0.62)`);
    document.documentElement.style.setProperty('--stroke', `rgba(${strokeRgb}, 0.14)`);
  },
};

// Expose color utility functions globally for convenience
function rgbToHsl(r, g, b) { return ColorUtils.rgbToHsl(r, g, b); }
function hslToHex(h, s, l) { return ColorUtils.hslToHex(h, s, l); }
function hslToRgb(h, s, l) { return ColorUtils.hslToRgb(h, s, l); }
function hexToRgb(hex) { return ColorUtils.hexToRgb(hex); }
function rgbToHexStr(r, g, b) { return ColorUtils.rgbToHexStr(r, g, b); }
function rgbToHsv(r, g, b) { return ColorUtils.rgbToHsv(r, g, b); }
function hsvToRgb(h, s, v) { return ColorUtils.hsvToRgb(h, s, v); }
function applyThemeColorFromBg(bgValue) { ColorUtils.applyThemeColorFromBg(bgValue); }
function extractAndSetColors(ctx) { ColorUtils.extractAndSetColors(ctx); }
