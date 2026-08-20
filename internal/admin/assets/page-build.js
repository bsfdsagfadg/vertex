import { $, esc } from './utils.js';

let API;
export function configureBuildService(service) { API = service; }

export async function loadBuildInfo() {
  const info = await API.build.get();
  const fields = [
    ['版本', info.version],
    ['提交', info.commit],
    ['构建时间', info.build_time],
    ['源码状态', info.dirty ? '包含未提交修改' : '干净'],
    ['版本来源', info.source],
    ['Go 版本', info.go_version],
    ['目标平台', [info.goos, info.goarch].filter(Boolean).join('/')]
  ];
  const grid = $('#buildInfoGrid');
  grid.innerHTML = fields.map(([label, value]) => `
    <div class="field">
      <label>${esc(label)}</label>
      <div class="mono" style="overflow-wrap:anywhere">${esc(value ?? '—')}</div>
    </div>
  `).join('');
}

document.getElementById('page-build').addEventListener('click', event => {
  if (event.target.closest('[data-build-action="refresh"]')) loadBuildInfo();
});
