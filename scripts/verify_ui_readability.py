"""Workspace visual/readability regression using an isolated, synthetic service.

Optional --baseline-overlay captures the pre-change frontend without modifying
source or a running service. Screenshots and measurements contain fixture data
only, under .artifacts/ui-polish-before or .artifacts/ui-polish-after.
"""
from pathlib import Path
from urllib.error import URLError
import argparse
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time

from playwright.sync_api import expect, sync_playwright
from verify_shared_workbench import ROOT, call, login_workbench

if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')

AUDIT = r"""() => {
  const rgba = value => (value.match(/[\d.]+/g) || []).map(Number);
  const luminance = rgb => rgb.slice(0,3).map(v => {
    v /= 255; return v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4;
  }).reduce((n,v,i) => n + v * [.2126,.7152,.0722][i], 0);
  const blend = (fg,bg) => {
    const a = fg.length > 3 ? fg[3] : 1;
    return fg.slice(0,3).map((v,i) => v*a + bg[i]*(1-a));
  };
  const background = element => {
    const layers = [];
    for (let node=element; node; node=node.parentElement) {
      const css=getComputedStyle(node);
      if (css.backgroundImage !== 'none' || Number(css.opacity) < 1) return null;
      const color=rgba(css.backgroundColor);
      if (color.length >= 3) layers.unshift(color);
    }
    return layers.reduce((bg,layer) => blend(layer,bg), [255,255,255]);
  };
  const problems=[], measurements=[];
  for (const el of document.querySelectorAll('.main *, .sidebar *, .topbar *, .dialog *')) {
    if (el.namespaceURI !== 'http://www.w3.org/1999/xhtml' || !el.getClientRects().length) continue;
    const css=getComputedStyle(el);
    if (css.visibility !== 'visible' || css.display === 'none' || el.closest('[disabled]')) continue;
    const text=[...el.childNodes].filter(n => n.nodeType===3).map(n=>n.textContent).join('').trim();
    if (!text || !/[\p{L}\p{N}]/u.test(text)) continue;
    // Native summary and collapsed descendants have different visibility semantics.
    if (el.closest('details:not([open])') && !el.closest('summary')) continue;
    const rect=el.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) continue;
    const size=parseFloat(css.fontSize), fg=rgba(css.color), bg=background(el);
    let contrast=null;
    if (bg && fg.length>=3) {
      const a=luminance(blend(fg,bg)), b=luminance(bg);
      contrast=(Math.max(a,b)+.05)/(Math.min(a,b)+.05);
    }
    const item={tag:el.tagName.toLowerCase(), selector:el.className || el.id,
      text:text.slice(0,60), size, color:css.color,
      contrast:contrast===null?null:Math.round(contrast*100)/100};
    measurements.push(item);
    const large=size>=24 || (size>=18.66 && Number(css.fontWeight)>=700);
    if (size<12 || (contrast!==null && contrast<(large?3:4.5))) problems.push(item);
  }
  return {bodySize:parseFloat(getComputedStyle(document.body).fontSize),
    pageWidth:document.documentElement.scrollWidth, viewport:innerWidth, problems, measurements};
}"""


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--baseline-overlay')
    args = parser.parse_args()
    baseline = bool(args.baseline_overlay)
    artifacts = ROOT / '.artifacts' / ('ui-polish-before' if baseline else 'ui-polish-after')
    artifacts.mkdir(parents=True, exist_ok=True)
    results = {}
    errors, external = [], []
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    admin_email, user_email = 'admin@ui.test', 'learner@ui.test'
    admin_password, user_password = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    admin_key, read_key = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
    with tempfile.TemporaryDirectory(prefix='ui-readability-', dir=ROOT / '.artifacts') as tmp:
        directory = Path(tmp)
        binary = directory / ('wzstudy.exe' if os.name == 'nt' else 'wzstudy')
        build = ['go', 'build']
        if baseline:
            build += ['-overlay', str((ROOT / args.baseline_overlay).resolve())]
        subprocess.run(build + ['-o', str(binary), './cmd/api'], cwd=ROOT, check=True)
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{port}', ADMIN_API_KEY=admin_key, READ_API_KEY=read_key,
                   SYSTEM_ADMIN_EMAIL=admin_email, SYSTEM_ADMIN_PASSWORD=admin_password,
                   POLL_ENABLED='false', AGENT_ENABLED='true', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (directory / 'service.log').open('ab') as log:
            server = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if server.poll() is not None:
                        raise RuntimeError('isolated service exited before readiness')
                    try:
                        call(base, '/health/ready')
                        break
                    except (URLError, OSError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('isolated service readiness timeout')
                call(base, '/admin/users', 'POST', {'email':user_email, 'display_name':'学习资料管理员', 'password':user_password}, admin_key)
                token=call(base, '/user/auth/login', 'POST', {'email':user_email, 'password':user_password, 'session_mode':'token'})['access_token']
                titles = ['让知识形成体系：用费曼学习法整理读书笔记', 'AI 工具入门：从提问到建立个人工作流', '学习路线规划：把目标拆成可执行的阶段', '每周回顾：让收藏的内容真正变成积累', '文章与视频的阅读记录管理', '专注力练习与复习计划']
                for i,title in enumerate(titles):
                    item=call(base, '/workspace/items', 'POST', {'status':'pending', 'content':{
                        'kind':'article', 'title':title, 'category':'学习方法' if i%2==0 else '人工智能',
                        'tags':['学习','方法'], 'summary':'从资料整理到持续学习，让每一次记录都能被清晰检索。',
                        'content':'这是隔离回归使用的学习资料。\n\n包含阅读建议、整理步骤与复习要点。', 'format':'text'}}, token)
                    if i<4:
                        call(base, '/workspace/items/'+item['id']+'/publish', 'POST', {'revision':item['revision']}, token)
                for name in ['平板学习端', '书房电脑 · 浏览器学习端']:
                    call(base, '/workspace/clients', 'POST', {'slug':'ui-'+secrets.token_hex(4), 'name':name,
                        'description':'用于个人知识库阅读与增量同步，学习记录通过用户账号保存。',
                        'enabled':True, 'permissions':['read','sync']}, token)
                with sync_playwright() as pw:
                    browser=pw.chromium.launch(headless=True)
                    try:
                        context=browser.new_context(viewport={'width':1440,'height':1050}, reduced_motion='reduce', locale='zh-CN')
                        page=context.new_page()
                        page.set_default_timeout(15000)
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        page.on('request', lambda request: external.append(request.url)
                                if request.url.startswith(('http://','https://')) and not request.url.startswith(base+'/') else None)
                        login_workbench(page,base,user_email,user_password,'user')
                        page.locator('#home-title').wait_for()
                        if not baseline:
                            assert page.request.get(base+'/workspace/workspace.css').status == 200
                        def inspect(name, capture=True):
                            report=page.evaluate(AUDIT)
                            results[name]=report
                            if capture:
                                page.screenshot(path=str(artifacts/(name+'.png')), full_page=True)
                            (artifacts/'measurements.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf-8')
                            if not baseline:
                                assert report['pageWidth'] <= report['viewport']+1, (name,report['pageWidth'],report['viewport'])
                            return report
                        def nav(route,heading):
                            page.locator(f'.sidebar [data-route="{route}"]').click()
                            expect(page.locator('#main-content h1')).to_have_text(heading)
                            if route=='library':
                                expect(page.locator('.material-name').first).to_be_visible()
                            if route=='query':
                                expect(page.locator('#query-results')).to_contain_text('让知识形成体系')
                        inspect('home-desktop')
                        for route,heading in [('library','数据录入'),('query','数据列表'),('clients','客户端管理'),('tasks','收集任务'),('roadmaps','学习路线图'),('catalog','合集管理'),('learning','学习数据'),('agent-access','Agent 接入'),('rss','RSS 订阅')]:
                            nav(route,heading)
                            inspect(route+'-desktop')
                        if not baseline:
                            nav('library','数据录入')
                            assert not page.locator('.intake-options').evaluate('el=>el.open')
                            page.locator('.intake-options > summary').click()
                            expect(page.locator('[data-action="collector-guide"]')).to_be_visible()
                            page.locator('[data-action="collector-guide"]').click()
                            expect(page.locator('.dialog')).to_be_visible()
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            page.locator('[data-action="view"][data-value="grid"]').click()
                            expect(page.locator('.material-card')).to_have_count(6)
                            page.locator('[data-action="view"][data-value="list"]').click()
                            page.locator('[data-action="upload"]').first.click()
                            expect(page.locator('#material-title')).to_be_visible()
                            inspect('upload-dialog-desktop')
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            nav('query','数据列表')
                            assert not page.locator('.query-advanced').evaluate('el=>el.open')
                            page.locator('#query-q').fill('费曼')
                            page.locator('#query-form button[type="submit"]').click()
                            expect(page.locator('.query-title')).to_have_count(1)
                            expect(page.locator('.query-title')).to_contain_text('费曼')
                            page.locator('.query-advanced > summary').click()
                            page.locator('#query-client').select_option(index=1)
                            page.locator('#query-form button[type="submit"]').click()
                            expect(page.locator('#query-results')).to_contain_text('workspace/clients/')
                            page.locator('.query-notes > summary').click()
                            expect(page.locator('.query-notes')).to_contain_text('next_cursor')
                            page.reload()
                            expect(page.locator('#main-content h1')).to_have_text('数据列表')
                            expect(page.locator('#query-results')).to_contain_text('让知识形成体系')
                            nav('clients','客户端管理')
                            expect(page.locator('.client-primary-actions')).to_have_count(2)
                            page.locator('.client-row').filter(has_text='平板学习端').locator('[data-action="edit-client"]').click()
                            expect(page.locator('#client-name')).to_have_value('平板学习端')
                            inspect('client-dialog-desktop')
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            page.locator('[data-action="client-feedback"]').first.click()
                            expect(page.locator('.client-feedback-dialog')).to_be_visible()
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            page.locator('[data-action="client-learning"]').first.click()
                            expect(page.locator('.learning-state-dialog')).to_be_visible()
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            page.locator('.usage-notes > summary').click()
                            expect(page.locator('.usage-notes')).to_contain_text('历史记录')
                            print('PASS: 折叠说明、列表/卡片切换、查询及历史筛选、客户端配置与历史记录均可用', flush=True)
                            nav('tasks','收集任务')
                            page.locator('[data-task-action="dispatch"]').click()
                            expect(page.locator('.dialog')).to_be_visible()
                            assert page.locator('.dialog .task-required').first.evaluate('el=>el.getBoundingClientRect().top-el.parentElement.getBoundingClientRect().top<12')
                            inspect('task-dialog-desktop')
                            page.locator('#modal-root [data-action="close-modal"]').last.click()
                            nav('roadmaps','学习路线图')
                            page.locator('[data-action="roadmap-new"]').first.click()
                            expect(page.locator('.roadmap-node')).to_have_count(4)
                            page.locator('#roadmap-title').fill('清晰可读的学习路线')
                            page.locator('[data-action="roadmap-child"]').click()
                            expect(page.locator('.roadmap-node')).to_have_count(5)
                            page.locator('#roadmap-node-title').fill('知识整理与实践')
                            page.locator('[data-action="roadmap-save"]').click()
                            expect(page.locator('#roadmap-save-state')).to_contain_text('已保存')
                            inspect('roadmap-editor-desktop')
                            page.set_viewport_size({'width':390,'height':844})
                            page.locator('[data-action="roadmap-fit"]').click()
                            inspect('roadmap-editor-mobile')
                            page.set_viewport_size({'width':1440,'height':1050})
                            print('PASS: 任务配置弹窗及路线图节点编辑/保存/手机适配正常', flush=True)

                        for width in [1100,900,390]:
                            page.set_viewport_size({'width':width,'height':844})
                            for route,heading in [('home',None),('library','数据录入'),('query','数据列表'),('clients','客户端管理'),('tasks','收集任务'),('roadmaps','学习路线图')]:
                                if width<=700:
                                    page.get_by_role('button',name='打开导航',exact=True).click()
                                page.locator(f'.sidebar [data-route="{route}"]').click()
                                if route=='home':
                                    page.locator('#home-title').wait_for()
                                else:
                                    expect(page.locator('#main-content h1')).to_have_text(heading)
                                if route=='library':
                                    expect(page.locator('.material-name').first).to_be_visible()
                                if route=='query':
                                    expect(page.locator('#query-results')).to_contain_text('让知识形成体系')
                                inspect(route+'-'+str(width),capture=width==390)
                            if width==390 and not baseline:
                                page.get_by_role('button',name='打开导航',exact=True).click()
                                page.locator('.sidebar [data-route="library"]').click()
                                page.locator('[data-action="upload"]').first.click()
                                inspect('upload-dialog-mobile')
                                expect(page.locator('#material-title')).to_be_visible()
                                page.locator('#modal-root [data-action="close-modal"]').last.click()
                        admin_context=browser.new_context(viewport={'width':1440,'height':1050},reduced_motion='reduce')
                        admin=admin_context.new_page()
                        admin.on('pageerror', lambda error: errors.append(str(error)))
                        login_workbench(admin,base,admin_email,admin_password,'admin')
                        admin.locator('#home-title').wait_for()
                        admin.locator('.sidebar [data-route="users"]').click()
                        expect(admin.locator('#main-content h1')).to_have_text('用户管理')
                        admin.get_by_text(user_email,exact=True).wait_for()
                        results['users-desktop']=admin.evaluate(AUDIT)
                        admin.screenshot(path=str(artifacts/'users-desktop.png'),full_page=True)
                        (artifacts/'measurements.json').write_text(json.dumps(results,ensure_ascii=False,indent=2),encoding='utf-8')
                        if not baseline:
                            problems={name:report['problems'] for name,report in results.items() if report['problems']}
                            assert not problems, json.dumps(problems,ensure_ascii=False,indent=2)
                            assert all(report['bodySize']>=15 for report in results.values())
                        assert not errors, errors
                        assert not external, external
                        print(('CAPTURE' if baseline else 'PASS')+': '+str(len(results))+' 组桌面/平板/手机测量与截图；字号、文本对比、横向溢出和脚本异常检查完成',flush=True)
                    finally:
                        browser.close()
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=10)


if __name__=='__main__':
    main()
