'use strict';

// The home screen only uses the current user's workspace or the separately
// authorized administrator account list. It never selects another workspace.
const homeView = (() => {
  let requestSerial = 0;
  const count = value => {
    const number = Number(value);
    return Number.isFinite(number) && number >= 0 ? Math.floor(number).toLocaleString('zh-CN') : '0';
  };
  const date = () => new Intl.DateTimeFormat('zh-CN', {month:'long', day:'numeric', weekday:'long'}).format(new Date());
  const artwork = () => `<div class="home-art" aria-hidden="true"><div class="home-art-orbit orbit-one"></div><div class="home-art-orbit orbit-two"></div><div class="home-art-circle"></div><div class="home-art-note note-back"><span></span><span></span><span></span></div><div class="home-art-note note-front"><div class="home-art-icon">${icon('layers')}</div><span></span><span></span><span></span></div><div class="home-art-spark spark-one">✦</div><div class="home-art-spark spark-two">✧</div></div>`;
  const metric = (label, value, unit, symbol, tone, id = '') => `<article class="home-metric"><span class="home-metric-icon ${tone}">${icon(symbol)}</span><div class="home-metric-label">${label}</div><div class="home-metric-value"><strong${id ? ` id="${id}"` : ''}>${value}</strong><span>${unit}</span></div></article>`;
  const shortcut = (symbol, label, description, destination, tone) => `<button class="home-shortcut" data-route="${destination}"><span class="home-shortcut-icon ${tone}">${icon(symbol)}</span><span class="home-shortcut-copy"><strong>${label}</strong><small>${description}</small></span><span class="home-shortcut-arrow">${icon('arrowRight')}</span></button>`;
  const status = value => ({published:'已发布', pending:'待审核', draft:'草稿'})[value] || '资料';

  function userHome() {
    const summary = state.summary || {};
    const name = esc(state.user?.display_name || '朋友');
    const items = (state.recent || []).slice(0, 4);
    const recent = items.map(item => `<button class="home-recent-row" data-action="detail" data-id="${esc(item.id)}" aria-label="查看 ${esc(item.title)}"><span class="home-recent-icon ${esc(item.type)}">${icon(item.type)}</span><span class="home-recent-copy"><strong>${esc(item.title)}</strong><small>${esc(TYPES[item.type] || '资料')} · ${esc(item.date)}</small></span><span class="home-recent-status">${status(item.status)}</span>${icon('chevronRight')}</button>`).join('');
    const sourceCount = (state.sources || []).filter(source => !source.system && source.enabled).length;
    const clientCount = (state.clients || []).filter(client => client.enabled).length;
    const adminOverview = state.role === 'admin' ? `<section class="home-overview" aria-labelledby="home-admin-title"><div class="home-section-heading"><div><span class="home-overline">ADMINISTRATION</span><h2 id="home-admin-title">用户管理</h2></div><button class="home-text-link" data-route="users">管理用户 ${icon('arrowRight')}</button></div><div class="home-metrics home-metrics-admin">${metric('用户总数','—','个账号','layers','sage','home-admin-total')}${metric('已启用','—','个账号','checkCircle','mint','home-admin-enabled')}${metric('已停用','—','个账号','clock','sand','home-admin-disabled')}</div><div class="home-admin-status" id="home-admin-status" role="status" aria-live="polite">正在读取账号概览…</div></section>` : '';
    return `<div class="home-screen">
      <section class="home-hero" aria-labelledby="home-title"><div class="home-hero-copy"><div class="home-kicker"><span></span>YOUR KNOWLEDGE SPACE</div><p class="home-date">${esc(date())}</p><h1 id="home-title">你好，${name}<span>。</span></h1><p class="home-hero-description">把喜欢的内容，慢慢积累成自己的知识。<br>每一次记录，都让成长更有迹可循。</p><div class="home-hero-actions"><button class="home-primary" data-action="upload">${icon('plus')}录入资料</button><button class="home-secondary" data-route="library">进入资料库 ${icon('arrowRight')}</button></div><div class="home-hero-foot"><span class="home-online-dot"></span>你的个人资料工作区</div></div>${artwork()}</section>
      <section class="home-overview" aria-labelledby="home-overview-title"><div class="home-section-heading"><div><span class="home-overline">AT A GLANCE</span><h2 id="home-overview-title">我的资料概览</h2></div><span class="home-caption">属于你的每一份积累</span></div><div class="home-metrics">${metric('资料总量',count(summary.total),'份资料','library','sage')}${metric('已发布',count(summary.published),'份可查询','checkCircle','mint')}${metric('待审核',count(summary.pending),'份待处理','clock','sand')}${metric('近 7 天新增',count(summary.week_new),'份资料','trend','lavender')}</div></section>${adminOverview}
      <div class="home-content-grid"><section class="home-surface" aria-labelledby="home-shortcuts-title"><div class="home-panel-heading"><div><span class="home-overline">QUICK ACCESS</span><h2 id="home-shortcuts-title">从这里开始</h2><p>常用功能，随手可达</p></div></div><div class="home-shortcuts">${shortcut('upload','数据录入','整理和录入学习资料','library','sage')}${shortcut('search','数据管理','查找资料与管理合集','query','mint')}${shortcut('roadmap','学习路线图','为目标规划清晰路径','roadmaps','sand')}${shortcut('monitor','客户端管理','连接你的学习客户端','clients','lavender')}${state.role==='admin'?shortcut('shield','用户管理','创建和管理用户账号','users','sage'):''}</div></section>
      <section class="home-surface home-recent-panel" aria-labelledby="home-recent-title"><div class="home-panel-heading"><div><span class="home-overline">RECENT ACTIVITY</span><h2 id="home-recent-title">最近更新</h2><p>回到最近整理过的内容</p></div><button class="home-text-link" data-route="library">全部资料 ${icon('arrowRight')}</button></div><div class="home-recent-list">${recent || `<div class="home-empty"><span>${icon('library')}</span><strong>这里还没有资料</strong><p>录入第一份内容，开始积累你的知识。</p><button class="home-text-link" data-action="upload">录入资料 ${icon('arrowRight')}</button></div>`}</div></section></div>
      <section class="home-bottom-note"><span class="home-bottom-icon">${icon('shield')}</span><div><strong>你的资料，由你掌握</strong><p>资料、附件与学习记录按用户隔离，查询客户端只访问获得授权的内容。</p></div><div class="home-bottom-facts"><span>已连接来源 <b>${count(sourceCount)}</b></span><span>启用客户端 <b>${count(clientCount)}</b></span><span>附件 <b>${count(summary.asset_count)}</b></span></div></section>
    </div>` + footer();
  }

  function render() { return userHome(); }
  function reset() { requestSerial++; }
  async function load() {
    if (state.role !== 'admin' || route !== 'home') return;
    const serial = ++requestSerial, epoch = authEpoch;
    const current = () => serial === requestSerial && epoch === authEpoch && state.authenticated && state.role === 'admin' && route === 'home';
    const message = $('#home-admin-status');
    if (message) message.textContent = '正在读取账号概览…';
    try {
      const users = await api('/admin/users');
      if (!current()) return;
      if (!Array.isArray(users)) throw new Error('账号列表响应格式无效');
      const enabled = users.filter(user => user.enabled).length;
      $('#home-admin-total').textContent = count(users.length);
      $('#home-admin-enabled').textContent = count(enabled);
      $('#home-admin-disabled').textContent = count(users.length - enabled);
      $('#home-admin-status').textContent = '账号状态已更新';
    } catch (error) {
      if (!current() || !$('#home-admin-status')) return;
      $('#home-admin-status').innerHTML = `暂时无法读取账号概览：${esc(error.message)} <button class="home-text-link" data-action="home-retry">重新加载</button>`;
    }
  }
  return {render, load, reset};
})();
