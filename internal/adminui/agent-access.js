'use strict';

// 自带 Agent（BYO）接入面板：任何账号都可以创建自己的采集端身份。
// 服务端托管的内置采集端、模型密钥与一键部署只保留在管理员视图。
const agentAccessView = (() => {
  const ADMIN_BASE = '/admin/agent';
  const WORKSPACE_BASE = '/workspace/agent';
  let agents = [];
  let status = null;
  let requestSerial = 0;

  const base = () => state.role === 'admin' ? ADMIN_BASE : WORKSPACE_BASE;
  const canManage = () => status?.enabled !== false;
  const trim = value => String(value ?? '').trim();
  const clip = (value, maximum) => {
    const text = trim(value);
    return text.length > maximum ? text.slice(0, maximum) + '…' : text;
  };
  const slugPattern = /^[a-z0-9][a-z0-9-]{0,63}$/;
  const PACKAGE_MODEL_PRESETS = {
    deepseek: { base_url: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
    openai: { base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
    qwen: { base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1', model: 'qwen-plus' },
    custom: { base_url: '', model: '' },
  };

  function permissionTags(agent) {
    const values = Array.isArray(agent?.permissions) ? agent.permissions : [];
    const labels = values.map(permission => {
      if (permission === 'task.claim') return '<span class="tag">任务领取</span>';
      if (permission === 'ingest') return '<span class="tag">资料入库</span>';
      return `<span class="tag">${esc(permission)}</span>`;
    });
    return labels.join('') || '<span class="tag">未授予权限</span>';
  }

  function statusBadge(agent) {
    return agent.enabled
      ? '<span class="task-badge ok">已启用</span>'
      : '<span class="task-badge muted">已停用</span>';
  }

  function lastSeen(agent) {
    if (!agent.last_seen_at) return '尚未连接';
    const device = [agent.hostname, agent.version].filter(Boolean).join(' · ');
    return dateText(agent.last_seen_at) + (device ? ` · ${device}` : '');
  }

  function rows() {
    if (!agents.length) {
      return `<div style="padding:0 16px 16px">${empty('还没有接入身份', '创建一个身份，把服务端地址和密钥填入你自己运行的 agent.env。')}</div>`;
    }
    return `<table class="task-table">
      <thead><tr><th>采集端</th><th>权限</th><th>状态</th><th>最近连接</th><th>操作</th></tr></thead>
      <tbody>${agents.map(agent => `<tr>
        <td data-label="采集端"><strong>${esc(agent.name)}</strong><small>${esc(agent.slug)}${agent.description ? ' · ' + esc(clip(agent.description, 90)) : ''}</small></td>
        <td data-label="权限"><div class="client-permission-tags">${permissionTags(agent)}</div></td>
        <td data-label="状态">${statusBadge(agent)}</td>
        <td data-label="最近连接"><small>${esc(lastSeen(agent))}</small></td>
        <td class="task-actions">
          <button class="text-btn" data-agent-action="connection" data-id="${esc(agent.id)}">接入配置</button>
          <button class="text-btn" data-agent-action="view-key" data-id="${esc(agent.id)}">查看密钥</button>
          <button class="text-btn" data-agent-action="download-zip" data-id="${esc(agent.id)}">下载agent程序</button>
          <button class="text-btn" data-agent-action="edit" data-id="${esc(agent.id)}">编辑</button>
          <button class="text-btn" data-agent-action="rotate" data-id="${esc(agent.id)}">轮换密钥</button>
          <button class="text-btn danger" data-agent-action="delete" data-id="${esc(agent.id)}">删除</button>
          <button class="switch ${agent.enabled ? 'on' : ''}" role="switch" aria-checked="${agent.enabled}" aria-label="${esc(agent.name)}启用状态" data-agent-action="toggle" data-id="${esc(agent.id)}"></button>
        </td>
      </tr>`).join('')}</tbody>
    </table>`;
  }

  function section() {
    return `<section class="panel task-list" id="agent-identity-panel">
      <div class="task-toolbar" style="padding:14px 16px">
        <div><strong style="font-size:14px">采集端身份</strong><small style="display:block;color:var(--muted);margin-top:3px">每个身份一条专属密钥；完整密钥只在创建或轮换时显示一次。</small></div>
        <div class="task-toolbar-actions">
          <span>共 <b id="agent-identity-count">${agents.length}</b> 个采集端</span>
          <button class="btn primary small-btn" data-agent-action="create" ${canManage() ? '' : 'disabled'}>${icon('plus')}新建接入</button>
        </div>
      </div>
      <div id="agent-identity-rows">${rows()}</div>
    </section>`;
  }

  function renderRows() {
    const root = $('#agent-identity-rows');
    const count = $('#agent-identity-count');
    const create = $('#agent-identity-panel [data-agent-action="create"]');
    if (root) root.innerHTML = rows();
    if (count) count.textContent = String(agents.length);
    if (create) create.disabled = !canManage();
  }

  async function load() {
    if (!state.authenticated || route !== 'agent-access') return;
    const seq = ++requestSerial;
    try {
      const [statusData, list] = await Promise.all([
        api(base() + '/status'),
        api(base() + '/agents'),
      ]);
      if (seq !== requestSerial || !state.authenticated || route !== 'agent-access') return;
      status = statusData || {};
      agents = Array.isArray(list) ? list : [];
      renderRows();
    } catch (error) {
      if (seq !== requestSerial) return;
      const root = $('#agent-identity-rows');
      if (root) root.innerHTML = `<p class="task-hint" style="padding:0 16px 16px">读取失败：${esc(error.message)}</p>`;
      showError(error);
    }
  }

  function environmentTemplate(key, model = {}, maskSecrets = false) {
    const preset = PACKAGE_MODEL_PRESETS.deepseek;
    const custom = model.provider === 'custom';
    const baseURL = trim(model.base_url) || (custom ? '' : preset.base_url);
    const modelName = trim(model.model) || (custom ? '' : preset.model);
    const apiKey = trim(model.api_key);
    const agentKey = maskSecrets && key ? (key.startsWith('wzag_') ? 'wzag_********' : '********') : (key || '请粘贴创建或轮换时得到的密钥');
    const visibleAPIKey = maskSecrets && apiKey ? '********' : apiKey;
    return [
      '# 由温知生成：只写入你自己的采集端，不要提交到版本库或发给他人。',
      `WZSTUDY_SERVER_URL=${location.origin}`,
      `WZSTUDY_AGENT_KEY=${agentKey}`,
      `AGENT_MODEL_BASE_URL=${baseURL}`,
      `AGENT_MODEL_API_KEY=${visibleAPIKey}`,
      `AGENT_MODEL=${modelName}`,
      'AGENT_SKILLS_DIR=skills',
    ].join('\n');
  }

  function packageFields() {
    const provider = $('#package-provider')?.value || 'deepseek';
    const preset = PACKAGE_MODEL_PRESETS[provider] || PACKAGE_MODEL_PRESETS.deepseek;
    return {
      provider,
      model: trim($('#package-model')?.value) || preset.model,
      base_url: trim($('#package-base-url')?.value) || preset.base_url,
      api_key: trim($('#package-api-key')?.value),
    };
  }

  function refreshPackagePreview() {
    const form = $('#agent-key-form');
    const textarea = $('#agent-env-template');
    if (!form || !textarea) return;
    textarea.value = environmentTemplate($('#agent-key-value')?.value || '', packageFields(), true);
  }

  function applyPackagePreset(select) {
    const provider = select?.value || 'deepseek';
    const preset = PACKAGE_MODEL_PRESETS[provider] || PACKAGE_MODEL_PRESETS.deepseek;
    const model = $('#package-model');
    const base = $('#package-base-url');
    const field = $('#package-base-url-field');
    if (model && preset.model) model.value = preset.model;
    if (base) base.value = preset.base_url;
    if (field) field.style.display = provider === 'custom' ? '' : 'none';
    refreshPackagePreview();
  }

  function keyModal(agent, key, rotated, existing = false, packageMode = false, profile = {}) {
    const providers = [['deepseek', 'DeepSeek'], ['openai', 'OpenAI'], ['qwen', '通义千问'], ['custom', 'OpenAI 兼容']];
    const provider = providers.some(([value]) => value === profile.provider) ? profile.provider : 'deepseek';
    const preset = PACKAGE_MODEL_PRESETS[provider] || PACKAGE_MODEL_PRESETS.deepseek;
    const modelName = trim(profile.model) || preset.model;
    const baseURL = trim(profile.base_url) || preset.base_url;
    const template = environmentTemplate(key, { provider, model: modelName, base_url: baseURL }, true);
    const title = packageMode ? '下载agent程序' : existing ? '接入密钥' : rotated ? '新的接入密钥' : '接入身份已创建';
    const subtitle = packageMode
      ? '使用已保存的 Agent 密钥和你填写的模型配置生成可运行压缩包。'
      : existing
        ? '密钥保存在你的工作区，可随时查看；也可以直接重新下载agent程序。'
        : '密钥已生成；可以直接下载包含程序与配置的 agent 程序包。';
    openModal(title, subtitle,
      `<div class="task-form" id="agent-key-form" data-agent-id="${esc(agent.id)}" data-agent-slug="${esc(agent.slug)}">
        <label class="task-field">采集端<input readonly value="${esc(agent.name)}（${esc(agent.slug)}）"></label>
        <label class="task-field">服务端地址<input readonly value="${esc(location.origin)}"></label>
        <label class="task-field">Agent 密钥<input id="agent-key-value" readonly value="${esc(key)}"></label>
        <div class="task-template-card">
          <div class="task-template-title">${icon('download')}<div><strong>直接打包 agent 程序</strong><small>包含采集端程序、agent.env、启动脚本和 skills。</small></div></div>
          <div class="task-form-grid">
            <label class="task-field">模型供应商<select id="package-provider">${providers.map(([value, label]) => `<option value="${value}" ${value === provider ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
            <label class="task-field">模型名称<input id="package-model" maxlength="120" value="${esc(modelName)}" autocomplete="off"></label>
            <label class="task-field">API Key<input id="package-api-key" type="password" maxlength="500" autocomplete="off" placeholder="未保存，需要重新填写"></label>
            <label class="task-field" id="package-base-url-field" style="display:${provider === 'custom' ? '' : 'none'}">Base URL<input id="package-base-url" maxlength="500" value="${esc(provider === 'custom' ? baseURL : '')}" autocomplete="off" placeholder="https://example.com/v1"></label>
          </div>
          <p class="task-hint">${icon('shield')}模型 API Key 只写入压缩包，不保存到服务端，需要重新填写；模型供应商、名称和 Base URL 会记住。Agent 密钥会保存在当前工作区。</p>
        </div>
        <label class="task-field">agent.env 预览（密钥已打码）<textarea id="agent-env-template" rows="8" readonly>${esc(template)}</textarea></label>
        <p class="task-hint">${icon('shield')}预览中的 Agent 密钥和模型 API Key 已打码；下载的 agent 程序包和「复制完整 agent.env」使用真实值。</p>
      </div>`,
      `<button class="btn" data-action="close-modal">${existing ? '关闭' : '我已保存'}</button><button class="btn" data-agent-action="copy-key">复制密钥</button><button class="btn" data-agent-action="copy-env">复制完整 agent.env</button><button class="btn primary" data-agent-action="package-download" data-agent-id="${esc(agent.id)}">下载agent程序</button>`,
      { icon: 'download', className: 'task-dialog' });
  }

  function createModal() {
    if (!canManage()) {
      toast('Agent 模块当前未启用，请联系管理员。', true);
      return;
    }
    openModal('新建 Agent 接入', '为你的采集端创建独立身份；密钥只属于这个身份。',
      `<form class="task-form" id="agent-create-form" novalidate>
        <label class="task-field">名称<span class="task-required">*</span><input name="name" maxlength="120" required placeholder="例如：我的笔记本采集端"></label>
        <label class="task-field">标识<input name="slug" maxlength="64" placeholder="留空自动生成；仅小写字母、数字和连字符"></label>
        <label class="task-field">说明<textarea name="description" rows="2" maxlength="4000" placeholder="运行设备、用途或模型供应商"></textarea></label>
        <p class="task-hint">${icon('shield')}该身份只能领取你派发的任务，并写入你工作区的来源；不能读取资料库或其他账户的数据。</p>
      </form>`,
      '<button class="btn" data-action="close-modal">取消</button><button class="btn primary" data-agent-action="create-submit">创建并生成配置</button>',
      { icon: 'plus', className: 'task-dialog' });
  }

  async function submitCreate(form) {
    if (!form) return;
    const data = new FormData(form);
    const name = trim(data.get('name'));
    let slug = trim(data.get('slug')).toLowerCase();
    if (!name) {
      toast('请填写采集端名称。', true);
      form.elements.namedItem('name')?.focus();
      return;
    }
    if (!slug) slug = 'agent-' + Date.now().toString(36);
    if (!slugPattern.test(slug)) {
      toast('标识只能使用小写字母、数字和连字符，且必须以字母或数字开头。', true);
      form.elements.namedItem('slug')?.focus();
      return;
    }
    const button = $('#modal-root [data-agent-action="create-submit"]');
    if (button) button.disabled = true;
    try {
      const result = await api(base() + '/agents', {
        method: 'POST',
        body: { name, slug, description: trim(data.get('description')) },
      });
      await load();
      keyModal(result.agent, result.access_key, false);
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  function editModal(agent) {
    openModal('编辑接入身份', '标识创建后不可修改；名称和说明可以随时调整。',
      `<form class="task-form" id="agent-edit-form" data-id="${esc(agent.id)}" novalidate>
        <label class="task-field">名称<span class="task-required">*</span><input name="name" maxlength="120" required value="${esc(agent.name)}"></label>
        <label class="task-field">标识<input readonly value="${esc(agent.slug)}"></label>
        <label class="task-field">说明<textarea name="description" rows="3" maxlength="4000">${esc(agent.description || '')}</textarea></label>
        <label class="task-field">状态<select name="enabled"><option value="true" ${agent.enabled ? 'selected' : ''}>已启用</option><option value="false" ${agent.enabled ? '' : 'selected'}>已停用</option></select></label>
      </form>`,
      '<button class="btn" data-action="close-modal">取消</button><button class="btn primary" data-agent-action="edit-submit">保存</button>',
      { icon: 'edit', className: 'task-dialog' });
  }

  async function submitEdit(form) {
    if (!form) return;
    const agent = agents.find(item => item.id === form.dataset.id);
    if (!agent) return;
    const data = new FormData(form);
    const name = trim(data.get('name'));
    if (!name) {
      toast('请填写采集端名称。', true);
      return;
    }
    const button = $('#modal-root [data-agent-action="edit-submit"]');
    if (button) button.disabled = true;
    try {
      await api(base() + '/agents/' + encodeURIComponent(agent.id), {
        method: 'PUT',
        body: {
          revision: agent.revision,
          slug: agent.slug,
          name,
          description: trim(data.get('description')),
          enabled: String(data.get('enabled')) !== 'false',
        },
      });
      closeModal();
      toast('接入身份已更新。');
      await load();
    } catch (error) {
      toast(error.status === 409 ? '该身份已被其他操作更新，请刷新后重试。' : error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  async function rotate(agent) {
    if (!agent) return;
    if (!window.confirm(`为「${agent.name}」生成新密钥？旧密钥会立即失效。`)) return;
    try {
      const result = await api(base() + '/agents/' + encodeURIComponent(agent.id) + '/key', {
        method: 'POST',
        body: { revision: agent.revision },
      });
      await load();
      keyModal(result.agent, result.access_key, true);
    } catch (error) {
      toast(error.status === 409 ? '该身份已被其他操作更新，请刷新后重试。' : error.message, true);
    }
  }

  async function removeAgent(agent) {
    if (!agent) return;
    if (!window.confirm(`删除采集端「${agent.name}」？删除后该身份立即失效，正在使用它的采集端将无法领取任务；已指派给它的未结束任务需要先处理。`)) return;
    try {
      await api(base() + '/agents/' + encodeURIComponent(agent.id) + '/delete', {
        method: 'POST',
        body: { revision: agent.revision },
      });
      toast('接入身份已删除。');
      await load();
    } catch (error) {
      toast(error.status === 409 ? (error.message || '该采集端还有未结束的任务，请先处理后再删除。') : error.message, true);
    }
  }

  async function toggle(agent) {
    if (!agent) return;
    try {
      await api(base() + '/agents/' + encodeURIComponent(agent.id), {
        method: 'PUT',
        body: {
          revision: agent.revision,
          slug: agent.slug,
          name: agent.name,
          description: agent.description || '',
          enabled: !agent.enabled,
        },
      });
      toast(agent.enabled ? '接入身份已停用，密钥立即失效。' : '接入身份已启用。');
      await load();
    } catch (error) {
      toast(error.status === 409 ? '该身份已被其他操作更新，请刷新后重试。' : error.message, true);
    }
  }

  async function openSecretModal(agent, packageMode = false) {
    if (!agent) return;
    try {
      const result = await api(base() + '/agents/' + encodeURIComponent(agent.id) + '/key');
      keyModal(agent, result.access_key, false, true, packageMode, result.package || {});
    } catch (error) {
      toast(error.status === 409 ? '该身份创建于旧版本，无法恢复明文密钥；请先轮换密钥。' : error.message, true);
    }
  }

  function viewKey(agent) {
    return openSecretModal(agent, false);
  }

  function downloadZip(agent) {
    return openSecretModal(agent, true);
  }

  function connectionModal(agent) {
    if (!agent) return;
    const template = environmentTemplate('', {}, true);
    openModal('接入配置', '把下面的内容写入采集端目录中的 agent.env，再运行启动脚本。',
      `<div class="task-form" id="agent-connection-form">
        <label class="task-field">采集端<input readonly value="${esc(agent.name)}（${esc(agent.slug)}）"></label>
        <label class="task-field">服务端地址<input readonly value="${esc(location.origin)}"></label>
        <label class="task-field">状态<input readonly value="${agent.enabled ? '已启用' : '已停用'}${agent.last_seen_at ? ' · 最近连接 ' + dateText(agent.last_seen_at) : ' · 尚未连接'}"></label>
        <label class="task-field">agent.env 模板<textarea id="agent-env-template" rows="9" readonly>${esc(template)}</textarea></label>
        <p class="task-hint">${icon('shield')}密钥不会再次显示。如果遗失，请使用「轮换密钥」生成新密钥；轮换后可直接下载agent程序。</p>
      </div>`,
      `<button class="btn" data-action="close-modal">关闭</button><button class="btn" data-agent-action="view-key" data-id="${esc(agent.id)}">查看密钥</button><button class="btn primary" data-agent-action="download-zip" data-id="${esc(agent.id)}">下载agent程序</button><button class="btn" data-agent-action="rotate-current" data-id="${esc(agent.id)}">轮换密钥</button>`,
      { icon: 'code', className: 'task-dialog' });
  }

  async function downloadPackage(button) {
    const form = $('#agent-key-form');
    const agentID = form?.dataset.agentId;
    const key = trim($('#agent-key-value')?.value);
    if (!form || !agentID || !key) {
      toast('密钥不可用，请重新创建或轮换后下载。', true);
      return;
    }
    const model = packageFields();
    if (!model.model) {
      toast('请填写模型名称。', true);
      return;
    }
    if (model.provider === 'custom' && !model.base_url) {
      toast('OpenAI 兼容服务需要填写 Base URL。', true);
      return;
    }
    if (button) button.disabled = true;
    try {
      const response = await fetch('/api/v1' + base() + '/agents/' + encodeURIComponent(agentID) + '/package', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          ...(state.user ? { 'X-WzStudy-User': state.user.id } : {}),
        },
        body: JSON.stringify({ access_key: key, model }),
      });
      if (!response.ok) {
        let message = '生成压缩包失败。';
        try {
          const json = await response.json();
          message = json.error?.message || message;
        } catch {}
        throw new APIError(message, response.status);
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = `wzstudy-agent-${form.dataset.agentSlug || 'agent'}.zip`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
      toast('agent程序包已生成：包含采集端程序、agent.env 和启动脚本。模型密钥只存在于压缩包中。');
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  async function downloadBinary() {
    try {
      const response = await fetch('/api/v1' + base() + '/binary', { credentials: 'same-origin' });
      if (!response.ok) {
        let message = '当前服务端未内置采集端程序，请联系管理员获取分发包。';
        try {
          const json = await response.json();
          message = json.error?.message || message;
        } catch {}
        throw new APIError(message, response.status);
      }
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = 'wzstudy-agent.exe';
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function copyValue(value, message, field) {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      toast(message);
      return;
    } catch {}
    if (field) {
      field.focus();
      field.select();
    }
    try {
      document.execCommand('copy');
      toast(message);
    } catch {
      toast('无法自动复制，请手动选择文本。', true);
    }
  }

  async function copyEnvironment() {
    if ($('#agent-key-form')) {
      const key = $('#agent-key-value')?.value || '';
      await copyValue(environmentTemplate(key, packageFields(), false), '完整 agent.env 已复制到剪贴板（含真实密钥）。', null);
      return;
    }
    const field = $('#agent-env-template');
    await copyValue(field?.value || '', 'agent.env 已复制到剪贴板。', field);
  }

  async function copyAgentKey() {
    const field = $('#agent-key-value');
    await copyValue(field?.value || '', 'Agent 密钥已复制。', field);
  }

  function bindEvents() {
    document.addEventListener('submit', event => {
      const form = event.target;
      if (form.id === 'agent-create-form') {
        event.preventDefault();
        submitCreate(form).catch(error => toast(error.message, true));
      } else if (form.id === 'agent-edit-form') {
        event.preventDefault();
        submitEdit(form).catch(error => toast(error.message, true));
      }
    });
    document.addEventListener('input', event => {
      if (event.target.closest('#agent-key-form')) refreshPackagePreview();
    });
    document.addEventListener('change', event => {
      if (event.target.id === 'package-provider') {
        applyPackagePreset(event.target);
        return;
      }
      if (event.target.closest('#agent-key-form')) refreshPackagePreview();
    });
    document.addEventListener('click', event => {
      const button = event.target.closest('[data-agent-action]');
      if (!button) return;
      const action = button.dataset.agentAction;
      const agent = agents.find(item => item.id === button.dataset.id);
      if (action === 'create') createModal();
      else if (action === 'download-binary') downloadBinary();
      else if (action === 'package-download') downloadPackage(button);
      else if (action === 'create-submit') submitCreate($('#agent-create-form'));
      else if (action === 'edit') editModal(agent);
      else if (action === 'edit-submit') submitEdit($('#agent-edit-form'));
      else if (action === 'view-key') viewKey(agent);
      else if (action === 'download-zip') downloadZip(agent);
      else if (action === 'rotate') rotate(agent);
      else if (action === 'rotate-current') rotate(agent);
      else if (action === 'delete') removeAgent(agent);
      else if (action === 'toggle') toggle(agent);
      else if (action === 'connection') connectionModal(agent);
      else if (action === 'copy-key') copyAgentKey();
      else if (action === 'copy-env') copyEnvironment();
    });
  }

  function reset() {
    requestSerial++;
    agents = [];
    status = null;
  }

  bindEvents();

  return { section, load, reset };
})();
