'use strict';

// Account management is rendered inside the shared workbench, but all data
// still comes from the administrator-only API. No user workspace is selected.
const adminUsersView = (() => {
  let users = [];
  let requestSerial = 0;
  let pending = null;

  function reset() {
    users = [];
    requestSerial++;
    pending = null;
  }

  function render() {
    return heading('用户管理', '创建、编辑和停用学习用户；个人资料仍属于各用户。',
      `<button class="btn primary" data-action="admin-user-new">${icon('plus')}创建用户</button>`, 'USER ADMINISTRATION') +
      `<div class="info-box admin-users-boundary">${icon('shield')}<span>管理员仅管理账号，不默认读取用户的资料、附件或学习记录。停用账号不会删除其数据。</span></div>
       <section class="panel admin-user-form-panel" id="admin-user-form-panel" hidden></section>
       <section class="panel admin-users-panel" aria-label="用户列表"><div class="admin-users-panel-header"><h2>学习用户</h2></div><div id="admin-user-results">${loading()}</div></section>` + footer();
  }

  async function load() {
    const serial = ++requestSerial;
    const epoch = authEpoch;
    const result = await api('/admin/users');
    if (serial !== requestSerial || epoch !== authEpoch || !state.authenticated || state.role !== 'admin' || route !== 'users') return;
    users = Array.isArray(result) ? result : [];
    const results = $('#admin-user-results');
    if (!results) return;
    results.innerHTML = users.map(user => `<article class="admin-user-row" data-admin-user="${esc(user.id)}">
      <div class="admin-user-details"><strong>${esc(user.display_name)}</strong><p>${esc(user.email)}</p><span class="badge ${user.enabled ? 'published' : 'paused'}">${user.enabled ? '已启用' : '已停用'}</span></div>
      <div class="admin-user-actions"><button class="btn small-btn" data-action="admin-user-edit" data-id="${esc(user.id)}">编辑 / 重置密码</button><button class="btn small-btn" data-action="admin-user-toggle" data-id="${esc(user.id)}">${user.enabled ? '停用' : '启用'}</button></div>
    </article>`).join('') || empty('还没有用户', '请创建账号，并安全地向本人交付初始密码。');
  }

  function edit(id = '') {
    const user = users.find(item => item.id === id);
    if (id && !user) throw new Error('用户信息已变化，请刷新后重试。');
    const panel = $('#admin-user-form-panel');
    panel.hidden = false;
    panel.innerHTML = `<h2>${user ? '编辑用户' : '创建用户'}</h2><form id="admin-user-form" data-id="${esc(id)}">
      <div class="field"><label for="admin-user-email">用户邮箱</label><input id="admin-user-email" name="email" type="email" required maxlength="254" value="${esc(user?.email || '')}"></div>
      <div class="field"><label for="admin-user-name">显示名称</label><input id="admin-user-name" name="display_name" required maxlength="40" value="${esc(user?.display_name || '')}"></div>
      <div class="field"><label for="admin-user-password">${user ? '新密码（不修改请留空）' : '初始密码'}</label><input id="admin-user-password" name="password" type="password" autocomplete="new-password" minlength="8" maxlength="128" ${user ? '' : 'required'}><small>8–128 UTF-8 字节。重置密码后，用户所有设备需要重新登录。</small></div>
      <div id="admin-user-error" role="alert"></div><div class="form-actions"><button class="btn primary" type="submit">保存账号</button><button class="btn" type="button" data-action="admin-user-cancel">取消</button></div>
    </form>`;
    $('#admin-user-email').focus();
  }

  async function action(button) {
    if (pending) { toast('账号正在保存，请等待完成。'); return; }
    const id = button.dataset.id;
    switch (button.dataset.action) {
      case 'admin-user-new': edit(); return;
      case 'admin-user-edit': edit(id); return;
      case 'admin-user-cancel': $('#admin-user-form-panel').hidden = true; $('#admin-user-form-panel').innerHTML = ''; return;
      case 'admin-user-toggle': {
        const user = users.find(item => item.id === id);
        if (!user) throw new Error('用户信息已变化，请刷新后重试。');
        if (!confirm(user.enabled ? '停用后该用户的全部会话立即失效，但资料保留。确认停用？' : '启用后用户需重新登录，确认继续？')) return;
        const operation = pending = {};
        try {
          await api('/admin/users/' + encodeURIComponent(id), {method:'PUT', body:{revision:user.revision, email:user.email, display_name:user.display_name, enabled:!user.enabled}});
          await load();
          toast(user.enabled ? '用户已停用' : '用户已启用');
        } finally { if (pending === operation) pending = null; }
      }
    }
  }

  async function submit(form) {
    if (pending) return;
    const old = users.find(item => item.id === form.dataset.id);
    if (form.dataset.id && !old) throw new Error('用户信息已变化，请刷新后重试。');
    const fd = new FormData(form);
    const body = {email:String(fd.get('email') || '').trim(), display_name:String(fd.get('display_name') || '').trim(), ...(old ? {revision:old.revision, enabled:old.enabled} : {})};
    if (fd.get('password')) body.password = String(fd.get('password'));
    const operation = pending = {};
    try {
      await api('/admin/users' + (old ? '/' + encodeURIComponent(old.id) : ''), {method:old ? 'PUT' : 'POST', body});
      if (!form.isConnected || state.role !== 'admin') return;
      form.reset();
      $('#admin-user-form-panel').hidden = true;
      await load();
      toast('用户账号已保存');
    } catch (error) {
      if (form.isConnected && $('#admin-user-error')) $('#admin-user-error').innerHTML = `<div class="form-error">${esc(error.message)}</div>`;
      else if (error.status !== 401) showError(error);
    } finally { if (pending === operation) pending = null; }
  }

  return {render, load, action, submit, reset, busy:() => !!pending};
})();
