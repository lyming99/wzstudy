'use strict';

// 收集任务板块：派发采集目标、跟踪领取与入库、取消与重试。
// 所有账号可见；管理员调用 /api/v1/admin/agent，普通账户调用
// /api/v1/workspace/agent，服务端按当前工作区隔离任务与事件。
const tasksView = (() => {
  let requestSerial = 0;
  let detailSerial = 0;
  let filter = 'all';
  let snapshot = null;
  let sources = [];
  let agents = [];
  let releases = [];
  let templates = [];

  // 管理员使用管理接口；普通账户使用同一套工作区级接口，服务端仍按
  // 当前用户会话隔离任务、事件、模板与采集端身份。
  const ADMIN_BASE = '/admin/agent';
  const WORKSPACE_BASE = '/workspace/agent';
  const agentBase = () => state.role === 'admin' ? ADMIN_BASE : WORKSPACE_BASE;
  const isAdmin = () => state.role === 'admin';

  const STATUS = {
    pending: { label: '待领取', tone: 'warn' },
    claimed: { label: '已领取', tone: 'info' },
    running: { label: '执行中', tone: 'info' },
    succeeded: { label: '已完成', tone: 'ok' },
    failed: { label: '失败', tone: 'error' },
    cancelled: { label: '已取消', tone: 'muted' },
    expired: { label: '已过期', tone: 'error' },
  };
  const FILTERS = [
    ['all', '全部'],
    ['active', '进行中'],
    ['succeeded', '已完成'],
    ['attention', '异常'],
  ];
  const EVENT_LABELS = {
    round_start: '轮次',
    assistant: '模型输出',
    tool_call: '调用工具',
    tool_result: '工具结果',
    notice: '提示',
    finish: '结束',
  };

  const date = value => {
    if (!value) return '—';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return '—';
    return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(parsed);
  };
  const clip = (value, maximum) => {
    const text = String(value || '').trim();
    return text.length > maximum ? text.slice(0, maximum) + '…' : text;
  };
  const getName = (list, id, fallback) => list.find(item => item.id === id)?.name || (id ? id.slice(0, 8) : fallback);
  const sourceName = id => getName(sources, id, '未知来源');
  const agentName = id => getName(agents, id, '任意采集端');
  const ingested = task => Number(task.stats?.items_created || 0) + Number(task.stats?.items_updated || 0) + Number(task.stats?.items_unchanged || 0);
  const quota = task => Number(task.constraints?.max_items || 0);

  function badge(status) {
    const value = STATUS[status] || { label: status || '未知', tone: 'muted' };
    return `<span class="task-badge ${value.tone}">${value.label}</span>`;
  }

  function metrics(status) {
    const counts = status?.tasks || {};
    const cards = [
      ['待领取', counts.pending || 0, 'clock', 'warn'],
      ['执行中', Number(counts.claimed || 0) + Number(counts.running || 0), 'refresh', 'info'],
      ['已完成', counts.succeeded || 0, 'check', 'ok'],
      ['失败 / 过期', Number(counts.failed || 0) + Number(counts.expired || 0), 'info', 'error'],
    ];
    return `<div class="task-metrics">${cards.map(([label, value, symbol, tone]) => `
      <article class="task-metric">
        <span class="task-metric-icon ${tone}">${icon(symbol)}</span>
        <span class="task-metric-label">${label}</span>
        <strong>${Number(value).toLocaleString('zh-CN')}</strong>
      </article>`).join('')}</div>`;
  }

  function matches(task, value) {
    if (value === 'all') return true;
    if (value === 'active') return ['pending', 'claimed', 'running'].includes(task.status);
    if (value === 'attention') return ['failed', 'expired', 'cancelled'].includes(task.status);
    return task.status === value;
  }

  function row(task) {
    const cancelable = ['pending', 'claimed', 'running'].includes(task.status);
    const retryable = ['failed', 'cancelled', 'expired'].includes(task.status);
    const deletable = ['succeeded', 'failed', 'cancelled', 'expired'].includes(task.status);
    const detail = task.result?.error?.message || task.cancel_reason || '';
    return `<tr>
      <td data-label="状态">${badge(task.status)}</td>
      <td data-label="任务">
        <strong>${esc(task.title)}</strong>
        <small>${esc(clip(task.goal, 110))}</small>
        ${detail ? `<small class="task-error-line">${esc(clip(detail, 90))}</small>` : ''}
      </td>
      <td data-label="来源">${esc(sourceName(task.source_id))}</td>
      <td data-label="进度">${ingested(task)} / ${quota(task)}${task.attempts ? ` · 第 ${task.attempts} 次` : ''}</td>
      <td data-label="采集端">${esc(agentName(task.agent_id))}</td>
      <td data-label="更新">${date(task.updated_at)}</td>
      <td class="task-actions">
        <button class="text-btn" data-task-action="detail" data-id="${esc(task.id)}">详情</button>
        ${cancelable ? `<button class="text-btn danger" data-task-action="cancel" data-id="${esc(task.id)}">取消</button>` : ''}
        ${retryable ? `<button class="text-btn" data-task-action="retry" data-id="${esc(task.id)}">重试</button>` : ''}
        ${deletable ? `<button class="text-btn danger" data-task-action="delete" data-id="${esc(task.id)}">删除</button>` : ''}
      </td>
    </tr>`;
  }

  function render() {
    const root = $('#main-content');
    if (!root || !snapshot) return;
    const status = snapshot.status || {};
    const tasks = snapshot.tasks || [];
    const visible = tasks.filter(task => matches(task, filter));
    const enabled = status.enabled !== false;
    const releaseAction = isAdmin() ? `<button class="btn" data-task-action="releases">${icon('download')}程序发布</button>` : '';
    root.innerHTML = `<section class="task-page" data-task-page>
      ${heading('收集任务', '把采集目标派发给采集端；采集端领取后抓取、整理并写入任务绑定的来源。', `${releaseAction}<button class="btn primary" data-task-action="dispatch" ${enabled ? '' : 'disabled'}>${icon('plus')}派发任务</button>`, enabled ? '已启用' : '未启用')}
      ${enabled ? '' : `<div class="task-alert">${icon('info')}<span>采集模块当前未启用。请先在服务端设置 <code>AGENT_ENABLED=true</code> 并重启，然后刷新此页面。</span></div>`}
      ${metrics(status)}
      <div class="task-toolbar">
        <nav class="section-tabs" aria-label="任务筛选">${FILTERS.map(([key, label]) => `<button data-task-filter="${key}" class="${filter === key ? 'active' : ''}" ${filter === key ? 'aria-current="page"' : ''}>${label}</button>`).join('')}</nav>
        <div class="task-toolbar-actions">
          <span>共 ${Number(snapshot.total || 0).toLocaleString('zh-CN')} 个任务 · 采集端 ${Number(agents.length).toLocaleString('zh-CN')} 个</span>
          <button class="btn small-btn" data-task-action="reload">${icon('refresh')}刷新</button>
        </div>
      </div>
      <div class="panel task-list">
        ${visible.length ? `<table class="task-table">
          <thead><tr><th>状态</th><th>任务</th><th>来源</th><th>入库 / 配额</th><th>采集端</th><th>更新</th><th>操作</th></tr></thead>
          <tbody>${visible.map(row).join('')}</tbody>
        </table>` : empty('没有符合条件的任务', filter === 'all' ? '点击“派发任务”创建第一个采集目标。' : '切换筛选或刷新后再看。')}
      </div>
      <p class="task-boundary">${icon('shield')}任务只能写入创建时绑定的来源；配额、允许域名与字段长度由服务端二次校验。采集端只领取任务，不能读取资料库。</p>
    </section>`;
  }

  async function load() {
    if (!state.authenticated) return;
    const seq = ++requestSerial;
    const root = $('#main-content');
    if (root && !snapshot) {
      root.innerHTML = heading('收集任务', '正在读取任务与采集端状态…', '') + `<div class="panel task-list">${empty('正在读取…', '请稍候。')}</div>`;
    }
    if (root) root.setAttribute('aria-busy', 'true');
    try {
      const [status, list, sourceList, agentList, templateList] = await Promise.all([
        api(`${agentBase()}/status`),
        api(`${agentBase()}/tasks?limit=50`),
        api('/workspace/sources'),
        api(`${agentBase()}/agents`),
        api(`${agentBase()}/task-templates`),
      ]);
      if (seq !== requestSerial || !state.authenticated) return;
      snapshot = { status, tasks: list.tasks || [], total: list.total || 0 };
      sources = Array.isArray(sourceList) ? sourceList : [];
      agents = Array.isArray(agentList) ? agentList : [];
      templates = Array.isArray(templateList) ? templateList : [];
      render();
    } catch (error) {
      if (seq !== requestSerial) return;
      if (root) root.innerHTML = empty('无法读取收集任务', esc(error.message), '<button class="btn small-btn" data-task-action="reload">重新加载</button>');
      showError(error);
    } finally {
      if (root && seq === requestSerial) root.removeAttribute('aria-busy');
    }
  }

  function dispatchModal() {
    openModal('派发收集任务', '采集端领取后按目标抓取并整理；来源留空时自动创建“Agent 收集”。', `
      <form data-task-form="dispatch" class="task-form" novalidate>
        <div class="task-template-bar">
          <label class="task-field task-template-picker"><span>任务模板</span>
            <select name="template_id" data-task-action="template-apply">${templateOptions('')}</select>
          </label>
          <div class="task-template-actions">
            <button type="button" class="text-btn" data-task-action="template-save" title="保存当前任务配置为模板">保存为模板</button>
            <button type="button" class="text-btn danger" data-task-action="template-delete" title="删除当前选择的模板" ${templates.length ? '' : 'disabled'}>删除模板</button>
          </div>
        </div>
        <div class="task-template-save" data-template-save hidden>
          <input name="template_name" maxlength="80" placeholder="模板名称，例如：官方博客文章采集" aria-label="模板名称">
          <button type="button" class="btn small-btn" data-task-action="template-save-confirm">保存</button>
          <button type="button" class="text-btn" data-task-action="template-save-cancel">取消</button>
        </div>
        <label class="task-field">收集来源
          <select name="source_id">
            <option value="">自动创建（Agent 收集）</option>
            ${sources.map(item => `<option value="${esc(item.id)}">${esc(item.name)}${item.enabled === false ? '（已停用）' : ''}</option>`).join('')}
          </select>
        </label>
        <p class="task-hint">${icon('shield')}留空时系统会自动创建“Agent 收集”来源，无需提前准备。</p>
        <label class="task-field">数据类型
          <select name="content_type">
            <option value="">不限</option>
            <option value="article">文章</option>
            <option value="video">视频（必须包含视频文件）</option>
            <option value="audio">音频（必须包含音频文件）</option>
            <option value="carousel">图文</option>
          </select>
        </label>
        <label class="task-field">采集目标<span class="task-required">*</span>
          <textarea name="goal" rows="3" maxlength="4000" required placeholder="例如：采集 Go 官方博客最近三篇文章，整理成中文学习资料并附原文链接"></textarea>
        </label>
        <label class="task-field">任务标题
          <input name="title" maxlength="200" placeholder="留空时自动截取采集目标">
        </label>
        <div class="task-form-grid">
          <label class="task-field">最多入库<input name="max_items" type="number" min="1" max="100" value="5"></label>
          <label class="task-field">最大轮次<input name="max_rounds" type="number" min="1" max="1000" value="100"></label>
          <label class="task-field">工具调用上限<input name="max_tool_calls" type="number" min="1" max="1000" value="100"></label>
          <label class="task-field">超时（秒）<input name="timeout_seconds" type="number" min="30" max="1800" value="" placeholder="自动：媒体1800，其他300"></label>
          <label class="task-field">优先级<input name="priority" type="number" min="-10" max="10" value="0"></label>
          <label class="task-field">指定采集端
            <select name="assigned_agent_id"><option value="">任意采集端</option>${agents.map(item => `<option value="${esc(item.id)}">${esc(item.name)}</option>`).join('')}</select>
          </label>
        </div>
        <div class="task-form-grid">
          <label class="task-field">语言<input name="language" maxlength="35" placeholder="zh-CN"></label>
          <label class="task-field">分类<input name="category" maxlength="200" placeholder="留空沿用来源分类"></label>
        </div>
        <label class="task-field">标签<input name="tags" placeholder="用逗号分隔，最多 20 个"></label>
        <label class="task-field">重复内容<select name="duplicate_policy"><option value="skip" selected>跳过已有或已删除内容（只新增）</option><option value="update">允许更新同来源已有内容</option></select></label>
        <label class="task-field">允许域名<input name="allow_domains" placeholder="用逗号分隔，如 go.dev, blog.golang.org；留空不限（仍禁止内网）"></label>
        <p class="task-hint">${icon('shield')}配额、允许域名与条目字段由服务端再次校验；采集端不能把数据写入其他来源。</p>
      </form>`,
      '<button class="btn" data-action="close-modal">取消</button><button class="btn primary" data-task-action="dispatch-submit">派发任务</button>',
      { icon: 'upload', className: 'task-dialog' });
  }

  const CONTENT_TYPE_LABELS = { article: '文章', video: '视频', audio: '音频', carousel: '图文' };

  const splitList = value => String(value || '').split(',').map(item => item.trim()).filter(Boolean);  const numberValue = (data, name, fallback) => {
    const parsed = Number(data.get(name));
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
  };
  const integerValue = (data, name, fallback) => {
    const raw = String(data.get(name) || '').trim();
    if (!raw) return fallback;
    const parsed = Number(raw);
    return Number.isInteger(parsed) ? parsed : fallback;
  };
  const utf8Bytes = value => new TextEncoder().encode(String(value || '')).length;
  const templateOptions = selected => `<option value="">不使用模板（空白配置）</option>` + templates.map(item => `<option value="${esc(item.id)}"${item.id === selected ? ' selected' : ''}>${esc(item.name)}</option>`).join('');

  function formConfig(data) {
    return {
      title: String(data.get('title') || '').trim(),
      goal: String(data.get('goal') || '').trim(),
      constraints: {
        max_items: numberValue(data, 'max_items', 5),
        content_type: String(data.get('content_type') || ''),
        duplicate_policy: String(data.get('duplicate_policy') || 'skip'),
        language: String(data.get('language') || '').trim(),
        category: String(data.get('category') || '').trim(),
        tags: splitList(data.get('tags')),
        allow_domains: splitList(data.get('allow_domains')),
      },
      budget: {
        max_rounds: numberValue(data, 'max_rounds', 100),
        max_tool_calls: numberValue(data, 'max_tool_calls', 100),
        timeout_seconds: integerValue(data, 'timeout_seconds', ['video','audio'].includes(String(data.get('content_type')))?1800:300),
      },
      priority: integerValue(data, 'priority', 0),
      assigned_agent_id: String(data.get('assigned_agent_id') || ''),
    };
  }

  async function submitDispatch(form) {
    const data = new FormData(form);
    const button = $('[data-task-action="dispatch-submit"]');
    const body = {
      source_id: String(data.get('source_id') || ''),
      ...formConfig(data),
    };
    if (!body.goal) { toast('请填写采集目标。', true); return; }
    if (body.goal.length > 4000) { toast('采集目标不能超过 4000 字节。', true); return; }
    if (button) button.disabled = true;
    try {
      await api(`${agentBase()}/tasks`, { method: 'POST', body });
      closeModal();
      toast('任务已派发，等待采集端领取。');
      filter = 'all';
      await load();
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  const templateRow = form => form.querySelector('[data-template-save]');
  const templateDeleteButton = form => form.querySelector('[data-task-action="template-delete"]');

  function openTemplateSave(form) {
    const row = templateRow(form);
    const input = form.querySelector('[name="template_name"]');
    if (!row || !input) return;
    if (!input.value.trim()) {
      const data = new FormData(form);
      const source = String(data.get('title') || '').trim() || String(data.get('goal') || '').trim();
      input.value = source.length > 24 ? source.slice(0, 24) : source;
    }
    row.hidden = false;
    input.focus();
    input.select();
  }

  function closeTemplateSave(form) {
    const row = templateRow(form);
    if (row) row.hidden = true;
  }

  function applyTemplate(form, id) {
    const template = templates.find(item => item.id === id);
    const remove = templateDeleteButton(form);
    if (remove) remove.disabled = !template;
    if (!template) return;
    const values = {
      title: template.title || '',
      goal: template.goal || '',
      content_type: template.constraints?.content_type || '',
      duplicate_policy: template.constraints?.duplicate_policy || 'skip',
      max_items: template.constraints?.max_items ?? 5,
      language: template.constraints?.language || '',
      category: template.constraints?.category || '',
      tags: (template.constraints?.tags || []).join(', '),
      allow_domains: (template.constraints?.allow_domains || []).join(', '),
      max_rounds: template.budget?.max_rounds ?? 100,
      max_tool_calls: template.budget?.max_tool_calls ?? 100,
      timeout_seconds: template.budget?.timeout_seconds ?? '',
      priority: template.priority ?? 0,
      assigned_agent_id: agents.some(item => item.id === template.assigned_agent_id) ? template.assigned_agent_id : '',
    };
    Object.entries(values).forEach(([name, value]) => {
      const field = form.elements.namedItem(name);
      if (field) field.value = String(value);
    });
    closeTemplateSave(form);
    toast(`已套用模板「${template.name}」。`);
  }

  async function saveTemplate(form) {
    const input = form.querySelector('[name="template_name"]');
    const name = String(input?.value || '').trim();
    if (!name) {
      toast('请填写模板名称。', true);
      input?.focus();
      return;
    }
    if (utf8Bytes(name) > 80) {
      toast('模板名称不能超过 80 字节（约 26 个汉字）。', true);
      input?.focus();
      return;
    }
    const button = form.querySelector('[data-task-action="template-save-confirm"]');
    if (button) button.disabled = true;
    try {
      const created = await api(`${agentBase()}/task-templates`, { method: 'POST', body: { name, ...formConfig(new FormData(form)) } });
      templates = [created, ...templates.filter(item => item.id !== created.id)];
      const select = form.querySelector('[name="template_id"]');
      if (select) select.innerHTML = templateOptions(created.id);
      const remove = templateDeleteButton(form);
      if (remove) remove.disabled = false;
      closeTemplateSave(form);
      toast(`模板「${created.name}」已保存。`);
    } catch (error) {
      toast(error.status === 409 ? '已有同名模板，请换一个名称。' : error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  async function deleteTemplate(form) {
    const select = form.querySelector('[name="template_id"]');
    const template = templates.find(item => item.id === select?.value);
    if (!template) {
      toast('请先选择要删除的模板。', true);
      return;
    }
    if (!window.confirm(`删除模板「${template.name}」？当前表单配置会保留。`)) return;
    try {
      await api(`${agentBase()}/task-templates/` + encodeURIComponent(template.id) + '/delete', { method: 'POST', body: {} });
      templates = templates.filter(item => item.id !== template.id);
      if (select) select.innerHTML = templateOptions('');
      const remove = templateDeleteButton(form);
      if (remove) remove.disabled = true;
      closeTemplateSave(form);
      toast(`模板「${template.name}」已删除，表单内容保留。`);
    } catch (error) {
      toast(error.message, true);
    }
  }

  const formatBytes = value => {
    const size = Number(value || 0);
    if (size >= 1 << 20) return `${(size / (1 << 20)).toFixed(1)} MB`;
    if (size >= 1 << 10) return `${(size / (1 << 10)).toFixed(0)} KB`;
    return `${size} B`;
  };

  function releaseMarkup() {
    const rows = releases.length ? releases.map(release => `
      <div class="release-row">
        <div>
          <strong>${esc(release.version)} · ${esc(release.platform)} · ${esc(release.arch)}</strong>
          <small>${esc(release.filename)} · ${formatBytes(release.size)} · ${date(release.created_at)}</small>
          ${release.notes ? `<small>${esc(clip(release.notes, 160))}</small>` : ''}
        </div>
        <div class="release-actions">
          <a class="btn small-btn" href="/api/v1${ADMIN_BASE}/releases/${encodeURIComponent(release.id)}/download">下载</a>
          <button class="text-btn danger" data-task-action="release-delete" data-id="${esc(release.id)}">删除</button>
        </div>
      </div>`).join('') : '<p class="task-hint">还没有发布记录。上传一个程序包后，可以从这里下载部署。</p>';
    return `<form data-task-form="release" class="task-form" novalidate>
      <div class="task-form-grid">
        <label class="task-field">版本号<span class="task-required">*</span><input name="version" maxlength="64" required placeholder="0.1.0"></label>
        <label class="task-field">平台<select name="platform"><option value="windows">Windows</option><option value="linux">Linux</option><option value="macos">macOS</option></select></label>
        <label class="task-field">架构<select name="arch"><option value="amd64">amd64</option><option value="arm64">arm64</option></select></label>
      </div>
      <label class="task-field">程序包<span class="task-required">*</span><input name="file" type="file" accept=".exe,.zip,.msi,.tar.gz,.tgz" required></label>
      <label class="task-field">更新说明<textarea name="notes" rows="2" maxlength="2000" placeholder="本版本包含哪些改动"></textarea></label>
      <p class="task-hint">${icon('shield')}支持 .exe / .zip / .msi / .tar.gz，最大 64 MiB；同一版本 + 平台 + 架构不可重复。程序包只对管理员开放下载。</p>
    </form>
    <h3 class="task-detail-title">已发布</h3>
    <div class="release-list">${rows}</div>`;
  }

  async function loadReleases() {
    if (!isAdmin()) return;
    const root = $('#release-root');
    if (!root) return;
    root.innerHTML = '<p class="task-hint">正在读取发布记录…</p>';
    try {
      const list = await api(`${ADMIN_BASE}/releases`);
      releases = Array.isArray(list) ? list : [];
      root.innerHTML = releaseMarkup();
    } catch (error) {
      root.innerHTML = `<p class="task-hint">读取失败：${esc(error.message)}</p>`;
    }
  }

  async function releasesModal() {
    if (!isAdmin()) {
      toast('程序发布仅管理员可用。', true);
      return;
    }
    openModal('Agent 程序发布', '上传采集端分发包，管理员可下载部署；文件不会进入资料库。', '<div id="release-root"><p class="task-hint">正在读取发布记录…</p></div>', '<button class="btn" data-action="close-modal">关闭</button><button class="btn primary" data-task-action="release-upload">上传发布</button>', { icon: 'download', className: 'task-dialog' });
    await loadReleases();
  }

  async function submitRelease(form) {
    const data = new FormData(form);
    const file = data.get('file');
    if (!data.get('version')) { toast('请填写版本号。', true); return; }
    if (!file || !file.size) { toast('请选择程序包文件。', true); return; }
    try {
      await api(`${ADMIN_BASE}/releases`, { method: 'POST', body: data });
      toast('程序包已发布。');
      await loadReleases();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function deleteRelease(id) {
    if (!window.confirm('删除这个程序包？已经下载到本地的副本不受影响。')) return;
    try {
      await api(`${ADMIN_BASE}/releases/` + encodeURIComponent(id) + '/delete', { method: 'POST', body: {} });
      toast('程序包已删除。');
      await loadReleases();
    } catch (error) {
      toast(error.message, true);
    }
  }

  function detailMarkup(task, events) {
    const stats = task.stats || {};
    const error = task.result?.error;
    const rows = [
      ['新增', stats.items_created || 0],
      ['更新', stats.items_updated || 0],
      ['未变', stats.items_unchanged || 0],
      ['跳过重复', stats.items_skipped || 0],
      ['轮次', stats.rounds || 0],
      ['工具调用', stats.tool_calls || 0],
      ['耗时', stats.duration_ms ? `${stats.duration_ms} ms` : '—'],
    ];
    return `<div class="task-detail">
      <div class="task-detail-head">
        ${badge(task.status)}
        ${['succeeded', 'failed', 'cancelled', 'expired'].includes(task.status) ? `<button class="text-btn danger" data-task-action="delete" data-id="${esc(task.id)}">删除任务</button>` : ''}
        <span>来源：${esc(sourceName(task.source_id))}</span>
        <span>采集端：${esc(agentName(task.agent_id))}</span>
        <span>类型：${esc(CONTENT_TYPE_LABELS[task.constraints?.content_type] || '不限')}</span>
        <span>尝试：${Number(task.attempts || 0)} / ${Number(task.max_attempts || 0)}</span>
      </div>
      <p class="task-detail-goal">${esc(task.goal)}</p>
      <div class="task-detail-stats">${rows.map(([label, value]) => `<div><span>${label}</span><strong>${typeof value === 'number' ? value.toLocaleString('zh-CN') : esc(value)}</strong></div>`).join('')}</div>
      ${error ? `<div class="task-alert error">${icon('info')}<span>${esc(error.code)}：${esc(error.message)}</span></div>` : ''}
      ${task.cancel_requested ? `<div class="task-alert">${icon('info')}<span>已请求取消${task.cancel_reason ? `：${esc(task.cancel_reason)}` : ''}，等待采集端在下次心跳时停止。</span></div>` : ''}
      <h3 class="task-detail-title">进度事件</h3>
      ${events === null ? '<p class="task-hint">正在读取事件…</p>' : events.length ? `<ol class="task-events">${events.map(event => `<li>
        <span class="task-event-kind ${esc(event.kind)}">${EVENT_LABELS[event.kind] || esc(event.kind)}</span>
        <span class="task-event-text">${esc(clip(event.text || event.summary || event.tool || '', 280))}</span>
        <time>${date(event.at)}</time>
      </li>`).join('')}</ol>` : empty('暂无事件', '采集端领取任务后会逐步上报轮次与工具调用。')}
    </div>`;
  }

  async function showDetail(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    const seq = ++detailSerial;
    openModal('任务详情', esc(task.title), `<div id="task-detail-root">${detailMarkup(task, null)}</div>`, '<button class="btn" data-action="close-modal">关闭</button>', { icon: 'layers', className: 'task-dialog' });
    try {
      const events = await api(`${agentBase()}/tasks/` + encodeURIComponent(id) + '/events');
      const root = $('#task-detail-root');
      if (root && seq === detailSerial) root.innerHTML = detailMarkup(task, Array.isArray(events) ? events : []);
    } catch (error) {
      const root = $('#task-detail-root');
      if (root && seq === detailSerial) root.innerHTML = detailMarkup(task, []) + `<p class="task-hint">事件读取失败：${esc(error.message)}</p>`;
    }
  }

  async function cancelTask(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    if (!window.confirm(`取消任务「${task.title}」？运行中的任务会在采集端下次心跳时停止。`)) return;
    try {
      await api(`${agentBase()}/tasks/` + encodeURIComponent(id) + '/cancel', { method: 'POST', body: { reason: isAdmin() ? '管理员取消' : '用户取消' } });
      toast(task.status === 'pending' ? '任务已取消。' : '已请求取消，等待采集端停止。');
      await load();
    } catch (error) {
      toast(error.message, true);
    }
  }


  async function deleteTask(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    if (!window.confirm(`删除任务「${task.title}」？任务与进度事件会一并删除，无法恢复。`)) return;
    try {
      await api(`${agentBase()}/tasks/` + encodeURIComponent(id) + '/delete', { method: 'POST', body: {} });
      toast('任务已删除。');
      if ($('#task-detail-root')) closeModal();
      await load();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function retryTask(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    if (!window.confirm(`把任务「${task.title}」重新放回待领取队列？`)) return;
    try {
      await api(`${agentBase()}/tasks/` + encodeURIComponent(id) + '/retry', { method: 'POST', body: {} });
      toast('任务已重置为待领取。');
      await load();
    } catch (error) {
      toast(error.message, true);
    }
  }

  function bindEvents() {
    document.addEventListener('submit', event => {
      const release = event.target.closest('form[data-task-form="release"]');
      if (release) {
        event.preventDefault();
        submitRelease(release).catch(error => toast(error.message, true));
        return;
      }
      const form = event.target.closest('form[data-task-form="dispatch"]');
      if (!form) return;
      event.preventDefault();
      submitDispatch(form).catch(error => toast(error.message, true));
    });
    document.addEventListener('change', event => {
      const select = event.target.closest('[data-task-action="template-apply"]');
      if (!select) return;
      const form = select.closest('form[data-task-form="dispatch"]');
      if (form) applyTemplate(form, select.value);
    });
    document.addEventListener('click', async event => {
      const filterButton = event.target.closest('[data-task-filter]');
      if (filterButton) {
        filter = filterButton.dataset.taskFilter;
        render();
        return;
      }
      const button = event.target.closest('[data-task-action]');
      if (!button) return;
      const action = button.dataset.taskAction;
      const id = button.dataset.id;
      try {
        if (action === 'dispatch') dispatchModal();
        else if (action === 'template-save') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) openTemplateSave(form);
        } else if (action === 'template-save-confirm') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) await saveTemplate(form);
        } else if (action === 'template-save-cancel') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) closeTemplateSave(form);
        } else if (action === 'template-delete') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) await deleteTemplate(form);
        } else if (action === 'releases') await releasesModal();
        else if (action === 'release-upload') {
          const form = $('#release-root form[data-task-form="release"]');
          if (form) await submitRelease(form);
        }
        else if (action === 'release-delete') await deleteRelease(id);
        else if (action === 'dispatch-submit') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) await submitDispatch(form);
        } else if (action === 'reload') await load();
        else if (action === 'detail') await showDetail(id);
        else if (action === 'cancel') await cancelTask(id);
        else if (action === 'retry') await retryTask(id);
        else if (action === 'delete') await deleteTask(id);
      } catch (error) {
        toast(error.message, true);
      }
    });
  }

  bindEvents();

  return { render, load };
})();
