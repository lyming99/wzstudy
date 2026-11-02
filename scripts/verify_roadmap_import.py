"""Real-browser WenzFlow JSON import using isolated users and a temporary store.

Does not read .env/.local, production accounts or WenzFlow user data. The only
source file is examples/wenzflow-roadmap.json, a synthetic native-format sample.
"""
from pathlib import Path
import json
import os
import subprocess
import tempfile
import uuid

from playwright.sync_api import expect, sync_playwright
import verify_user_workspaces as fixture

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.artifacts' / 'roadmap-import'
SAMPLE = ROOT / 'examples' / 'wenzflow-roadmap.json'


def check_layout(page, name):
    for width in (1440, 1024, 768, 390, 320):
        page.set_viewport_size({'width': width, 'height': 1000})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), (name, width)
        if name == 'preview':
            assert page.locator('.dialog').evaluate('el => el.scrollWidth <= el.clientWidth + 1'), (name, width)
        if width in (1440, 390):
            page.screenshot(path=str(OUT / f'{name}-{width}.png'), full_page=True)
    page.set_viewport_size({'width': 1440, 'height': 1000})
    fixture.check(f'{name}: 320–1440 px layout')


def verify(page, context, token, other_token, existing):
    errors, external, writes = [], [], []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.on('request', lambda request: external.append(request.url) if request.url.startswith(('http:', 'https:')) and not request.url.startswith(fixture.BASE + '/') else None)
    page.on('request', lambda request: writes.append(request.url) if '/workspace/roadmaps' in request.url and request.method in ('POST', 'PUT', 'DELETE') else None)
    page.goto(fixture.BASE + '/workspace/#roadmaps')
    page.locator('#login-email').fill(fixture.A_EMAIL)
    page.locator('#login-password').fill(fixture.A_PASSWORD)
    page.locator('#login-form button[type=submit]').click()
    expect(page.locator('h1')).to_have_text('学习路线图')
    expect(page.locator('.roadmap-card')).to_have_count(1)
    response = context.request.get(fixture.BASE + '/workspace/roadmap-import.js')
    assert response.status == 200 and 'javascript' in response.headers['content-type']
    check_layout(page, 'list')

    def open_import():
        page.locator('[data-action=roadmap-import]').click()
        expect(page.locator('#roadmap-import-form')).to_be_visible()
        expect(page.locator('[data-action=roadmap-import-confirm]')).to_be_disabled()

    def choose_sample():
        page.locator('#roadmap-import-file').set_input_files(str(SAMPLE))
        expect(page.locator('#roadmap-import-preview')).to_contain_text('4 个节点')
        expect(page.locator('[data-action=roadmap-import-confirm]')).to_be_enabled()

    open_import()
    page.locator('#roadmap-import-file').set_input_files({'name': 'broken.json', 'mimeType': 'application/json', 'buffer': b'{"nodes":['})
    expect(page.locator('#form-error')).to_contain_text('有效的 JSON')
    expect(page.locator('[data-action=roadmap-import-confirm]')).to_be_disabled()
    choose_sample()
    expect(page.locator('.roadmap-import-tree li')).to_have_count(4)
    expect(page.locator('.roadmap-import-tree li span')).to_have_text(['Go 学习路线', '学习准备', '基础语法', '变量与函数'])
    expect(page.locator('.roadmap-import-warnings')).to_contain_text('子笔记引用')
    check_layout(page, 'preview')
    assert fixture.call('/workspace/roadmaps', token=token)['total'] == 1
    assert not writes
    page.locator('.dialog-footer [data-action=close-modal]').click()
    expect(page.locator('.roadmap-card')).to_have_count(1)
    fixture.check('invalid-file recovery, complete preview, cancel and no write before save')

    open_import()
    unsafe = {'nodes': [{'uuid': 'root', 'parentId': None, 'content': '<img src=x onerror="window.importXSS=1">', 'link': 'javascript:window.importXSS=2'}]}
    page.locator('#roadmap-import-file').set_input_files({'name': 'unsafe.json', 'mimeType': 'application/json', 'buffer': json.dumps(unsafe).encode()})
    expect(page.locator('[data-action=roadmap-import-confirm]')).to_be_enabled()
    expect(page.locator('#roadmap-import-preview img')).to_have_count(0)
    assert page.evaluate('window.importXSS') is None
    page.locator('.dialog-footer [data-action=close-modal]').click()
    fixture.check('untrusted labels and links are inert text')

    open_import()
    choose_sample()
    page.locator('[data-action=roadmap-import-confirm]').click()
    expect(page.locator('#roadmap-properties')).to_be_visible()
    expect(page.locator('#roadmap-title')).to_have_value('Go 学习路线')
    expect(page.locator('#roadmap-save-state')).to_contain_text('未保存')
    expect(page.locator('.roadmap-node')).to_have_count(3)  # The fourth is under a collapsed branch.
    assert fixture.call('/workspace/roadmaps', token=token)['total'] == 1
    assert not writes
    assert page.evaluate('roadmapEditor.doc.nodes.every(n => n.item_ids.length === 0)')
    assert page.evaluate('roadmapEditor.doc.id === "" && roadmapEditor.doc.revision === 0')

    page.once('dialog', lambda dialog: dialog.dismiss())
    page.locator('[data-action=roadmap-back]').click()
    expect(page.locator('#roadmap-title')).to_have_value('Go 学习路线')
    page.locator('.roadmap-node-main').filter(has=page.locator('strong', has_text='基础语法')).click()
    expect(page.locator('#roadmap-node-note')).to_contain_text('WenzFlow 子笔记引用')
    page.locator('[data-action=roadmap-child]').click()
    expect(page.locator('#roadmap-count')).to_contain_text('5 / 300')
    page.locator('[data-action=roadmap-undo]').click()
    expect(page.locator('#roadmap-count')).to_contain_text('4 / 300')
    page.locator('[data-action=roadmap-redo]').click()
    expect(page.locator('#roadmap-count')).to_contain_text('5 / 300')
    page.locator('[data-action=roadmap-undo]').click()
    page.locator('#roadmap-title').fill('Go 学习路线（导入验收）')
    page.locator('#roadmap-summary').fill('导入后仍可编辑和关联资料；隔离验证数据。')
    check_layout(page, 'editor')
    page.locator('[data-action=roadmap-save]').click()
    expect(page.locator('#roadmap-save-state')).to_contain_text('已保存')
    expect(page.locator('[data-action=roadmap-save]')).to_be_disabled()
    assert len(writes) == 1
    identifier = page.evaluate('roadmapEditor.doc.id')
    assert identifier != existing['id']
    saved = fixture.call('/workspace/roadmaps/' + identifier, token=token)
    assert saved['revision'] == 1 and len(saved['nodes']) == 4
    assert [n['title'] for n in saved['nodes']] == ['Go 学习路线（导入验收）', '学习准备', '基础语法', '变量与函数']
    assert saved['nodes'][2]['collapsed']
    assert saved['nodes'][3]['parent_id'] == saved['nodes'][2]['id']
    assert all(n['item_ids'] == [] for n in saved['nodes'])
    source_ids = {n['uuid'] for n in json.loads(SAMPLE.read_text(encoding='utf-8'))['nodes']}
    assert not source_ids.intersection(n['id'] for n in saved['nodes'])
    assert fixture.call('/workspace/roadmaps/' + existing['id'], token=token) == existing
    fixture.call('/workspace/roadmaps/' + identifier, token=other_token, expected=404)
    assert fixture.call('/workspace/roadmaps', token=other_token)['total'] == 0
    fixture.check('new draft, leave guard, undo/redo, save, fresh IDs, unchanged existing roadmap and user isolation')

    page.reload()
    page.locator(f'[data-action=roadmap-edit][data-id="{identifier}"]').first.click()
    expect(page.locator('#roadmap-title')).to_have_value(saved['title'])
    expect(page.locator('.roadmap-node')).to_have_count(3)
    page.locator('.roadmap-node-main').filter(has=page.locator('strong', has_text='基础语法')).click()
    page.locator('#roadmap-toolbar [data-action=roadmap-fold]').click()
    expect(page.locator('.roadmap-node')).to_have_count(4)
    page.locator('[data-action=roadmap-undo]').click()
    expect(page.locator('.roadmap-node')).to_have_count(3)
    assert not errors, errors
    assert not external, external
    fixture.check('saved roadmap reloads with order/references/fold state; no JS errors or external requests')
    return identifier, saved


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / 'verification.json').unlink(missing_ok=True)
    fixture.BINARY = OUT / ('wzstudy-import.exe' if os.name == 'nt' else 'wzstudy-import')
    subprocess.run(['go', 'build', '-o', str(fixture.BINARY), './cmd/api'], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix='isolated-', dir=OUT) as temp:
        directory = Path(temp)
        env = dict(os.environ)
        for key in list(env):
            if key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(key)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{fixture.PORT}', ADMIN_API_KEY=fixture.ADMIN, READ_API_KEY=fixture.READER,
                   SYSTEM_ADMIN_EMAIL=fixture.ADMIN_EMAIL, SYSTEM_ADMIN_PASSWORD=fixture.ADMIN_PASSWORD,
                   POLL_ENABLED='false', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (directory / 'service.log').open('ab') as log:
            try:
                fixture.start(env, log)
                for email, password in [(fixture.A_EMAIL, fixture.A_PASSWORD), (fixture.B_EMAIL, fixture.B_PASSWORD)]:
                    fixture.call('/admin/users', 'POST', {'email': email, 'display_name': '路线导入验证用户', 'password': password}, fixture.ADMIN, 201)
                token = fixture.login(fixture.A_EMAIL, fixture.A_PASSWORD)
                other_token = fixture.login(fixture.B_EMAIL, fixture.B_PASSWORD)
                existing = fixture.call('/workspace/roadmaps', 'POST', {
                    'revision': 0, 'title': '已有路线图，不应覆盖', 'summary': '原有内容',
                    'nodes': [{'id': str(uuid.uuid4()), 'parent_id': '', 'title': '已有路线图，不应覆盖', 'note': '原有说明', 'item_ids': [], 'collapsed': False}],
                }, token, 201)
                with sync_playwright() as playwright:
                    browser = playwright.chromium.launch(channel='chrome', headless=True)
                    context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    page = context.new_page()
                    identifier, saved = verify(page, context, token, other_token, existing)
                    browser.close()
                fixture.stop()
                fixture.start(env, log)
                fresh_token = fixture.login(fixture.A_EMAIL, fixture.A_PASSWORD)
                assert fixture.call('/workspace/roadmaps/' + identifier, token=fresh_token) == saved
                fixture.check('imported roadmap persists across isolated server restart')
            finally:
                fixture.stop()
    (OUT / 'verification.json').write_text(json.dumps({'checks': fixture.CHECKS}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(f'PASS: {len(fixture.CHECKS)} roadmap import checks; artifacts: {OUT}', flush=True)


if __name__ == '__main__':
    main()
