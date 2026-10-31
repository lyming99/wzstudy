"""Browser regression for deployment model selection, using isolated fake models.
Build .artifacts/wzstudy.exe first. No real provider keys or deployment creation.
"""
from pathlib import Path
import json
import tempfile

from playwright.sync_api import expect, sync_playwright
import verify_two_chains as fixture


def main():
    checks = []
    with tempfile.TemporaryDirectory(prefix='model-picker-', dir=fixture.ARTIFACTS) as temp, (fixture.ARTIFACTS / 'model-picker-service.log').open('wb') as log:
        fixture.start(Path(temp), log)
        try:
            with sync_playwright() as pw:
                browser = pw.chromium.launch(channel='chrome', headless=True)
                context = browser.new_context(viewport={'width': 1440, 'height': 1100})
                page = context.new_page()
                errors, held, requests = [], [], []
                page.on('pageerror', lambda error: errors.append(str(error)))
                response = {'models': ['model-alpha', 'model-beta', 'model-gamma']}
                mode = {'value': 'ok'}

                def models_route(route):
                    requests.append(route.request.post_data_json)
                    if mode['value'] == 'hold':
                        held.append(route)
                    elif mode['value'] == 'error':
                        route.fulfill(status=502, json={'error': {'message': '测试模型查询失败'}})
                    else:
                        route.fulfill(json={'data': response})

                page.route('**/api/v1/admin/agent/models', models_route)
                page.goto(fixture.BASE + '/workspace/#agent-access')
                page.locator('#login-email').fill(fixture.EMAIL)
                page.locator('#login-password').fill(fixture.PASSWORD)
                page.locator('#login-form button[type=submit]').click()
                open_button = page.locator('[data-action=deploy-agent]')
                expect(open_button).to_be_visible()
                open_button.click()
                field = page.locator('#deploy-model')
                menu = page.locator('#deploy-model-options')
                query = page.locator('[data-action=deploy-models]')
                toggle = page.locator('[data-action=deploy-model-toggle]')
                field.fill('deepseek-flash')
                page.locator('#deploy-form [name=api_key]').fill('synthetic-test-key')
                query.click()
                expect(menu).to_be_visible()
                expect(menu.locator('[data-deploy-model]')).to_have_count(3)
                assert requests[-1]['provider'] == 'deepseek'
                menu.locator('[data-deploy-model="model-beta"]').click()
                expect(field).to_have_value('model-beta')
                expect(menu).to_be_hidden()
                toggle.click()
                expect(menu.locator('[data-deploy-model]')).to_have_count(3)
                menu.locator('[data-deploy-model="model-alpha"]').click()
                expect(field).to_have_value('model-alpha')
                checks.append('已有模型名不限制候选，查询后自动展开，箭头可重复展开并选择')

                field.focus()
                field.press('ArrowDown')
                field.press('ArrowDown')
                field.press('Enter')
                expect(field).to_have_value('model-beta')
                field.press('ArrowDown')
                field.press('Escape')
                expect(menu).to_be_hidden()
                expect(page.locator('#deploy-form')).to_be_visible()
                field.fill('manual/custom-model')
                assert page.locator('#deploy-form').evaluate("form=>new FormData(form).get('model')") == 'manual/custom-model'
                toggle.click()
                page.locator('.dialog-header h2').click()
                expect(menu).to_be_hidden()
                checks.append('键盘选择、Esc 仅关闭列表、外部点击收起以及手动输入正常')

                page.locator('#deploy-provider').select_option('custom')
                toggle.click()
                expect(menu.locator('[data-deploy-model]')).to_have_count(0)
                expect(menu).to_contain_text('请先查询')
                page.locator('#deploy-form [name=base_url]').fill('https://models.example.test/v1')
                mode['value'] = 'hold'
                query.click()
                expect(query).to_be_disabled()
                page.locator('#deploy-provider').select_option('qwen')
                assert held
                held.pop().fulfill(json={'data': {'models': ['stale-provider-model']}})
                expect(query).to_be_enabled()
                expect(menu.locator('[data-deploy-model]')).to_have_count(0)
                expect(menu).to_be_hidden()
                checks.append('切换供应商清空候选，过期请求不会覆盖当前供应商')

                mode['value'] = 'ok'
                response['models'] = []
                query.click()
                expect(query).to_be_enabled()
                expect(menu).to_be_hidden()
                mode['value'] = 'error'
                query.click()
                expect(page.locator('.toast-stack')).to_contain_text('测试模型查询失败')
                expect(query).to_be_enabled()
                expect(menu.locator('[data-deploy-model]')).to_have_count(0)
                checks.append('空列表和查询失败不残留旧候选，仍允许手动填写')

                mode['value'] = 'hold'
                query.click()
                expect(query).to_be_disabled()
                fixture.close_modal(page)
                open_button.click()
                held.pop().fulfill(json={'data': {'models': ['old-dialog-model']}})
                expect(menu.locator('[data-deploy-model]')).to_have_count(0)
                expect(menu).to_be_hidden()
                checks.append('关闭后重开弹窗不会被旧请求污染')

                page.set_viewport_size({'width': 390, 'height': 1000})
                mode['value'] = 'ok'
                response['models'] = ['model-alpha', 'model-beta', 'model-gamma']
                query.click()
                expect(menu).to_be_visible()
                menu.locator('[data-deploy-model="model-gamma"]').click()
                expect(field).to_have_value('model-gamma')
                assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
                assert not errors, errors
                checks.append('390px 手机视口可展开选择，无横向溢出或浏览器错误')
                browser.close()
        finally:
            fixture.stop()
    (fixture.ARTIFACTS / 'deploy-model-picker-verification.json').write_text(json.dumps({'passed': len(checks), 'checks': checks}, ensure_ascii=False, indent=2), encoding='utf-8')
    for check in checks:
        print('PASS:', check)


if __name__ == '__main__':
    main()
