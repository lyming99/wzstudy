"""Isolated real-service/browser regression. Never reads .env or production data."""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError
import io
import json
import os
import secrets
import shutil
import socket
import struct
import subprocess
import tempfile
import time
import uuid
import wave

from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.artifacts' / 'learning-optimization'
BINARY = OUT / ('wzstudy-learning.exe' if os.name == 'nt' else 'wzstudy-learning')
OUT.mkdir(parents=True, exist_ok=True)
ADMIN, READER, PASSWORD = [secrets.token_urlsafe(32) for _ in range(3)]
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
CHECKS = []


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def call(path, method='GET', body=None, token=None, expected=200, raw=None, content_type=None):
    headers = {'Authorization': 'Bearer ' + token} if token else {}
    if body is not None:
        raw = json.dumps(body).encode('utf-8')
        headers['Content-Type'] = 'application/json'
    if content_type:
        headers['Content-Type'] = content_type
    request = Request(BASE + '/api/v1' + path, method=method, headers=headers, data=raw)
    try:
        response = urlopen(request, timeout=20)
    except HTTPError as error:
        response = error
    with response:
        status, data = response.status, response.read()
    assert status == expected, f'{method} {path}: status {status}, expected {expected}: {data[:250]}'
    if data.startswith(b'{'):
        result = json.loads(data)
        return result.get('data', result)
    return data


def create_user(name):
    user = call('/admin/users', 'POST', {'email': name + '@learning.test', 'display_name': name, 'password': PASSWORD}, ADMIN, 201)
    login = call('/user/auth/login', 'POST', {'email': user['email'], 'password': PASSWORD, 'session_mode': 'token'})
    return user, login['access_token']


def upload_lesson(token, title, kind="audio", media_data=None):
    buf = io.BytesIO()
    with wave.open(buf, 'wb') as audio:
        audio.setnchannels(1)
        audio.setsampwidth(2)
        audio.setframerate(8000)
        audio.writeframes(struct.pack('<h', 0) * 64000)
    metadata = {'status': 'pending', 'content': {'kind': kind, 'title': title, 'content': 'audio body', 'duration_ms': 8000,
        'chapters': [{'id': 'intro', 'title': 'Opening', 'start_ms': 0}, {'id': 'practice', 'title': 'Practice', 'start_ms': 4000}],
        'subtitles': [
            {'id': 'en', 'language': 'en', 'format': 'srt', 'content': '1\n00:00:00,000 --> 00:00:02,000\nHello\n\n2\n00:00:02,000 --> 00:00:04,000\nPractice now\n\n3\n00:00:04,000 --> 00:00:06,000\nTry again\n\n4\n00:00:06,000 --> 00:00:08,000\nGoodbye'},
            {'id': 'zh', 'language': 'zh-CN', 'format': 'vtt', 'content': 'WEBVTT\n\n00:00.000 --> 00:02.000\n你好\n\n00:02.000 --> 00:03.000\n现在练习\n\n00:03.000 --> 00:04.000\n继续跟读\n\n00:04.000 --> 00:06.000\n再试一次\n\n00:06.000 --> 00:08.000\n再见'}]}}
    if media_data is None:
        media_data = buf.getvalue()
    filename, mime = ('lesson.webm', 'video/webm') if kind == 'video' else ('lesson.wav', 'audio/wav')
    boundary = 'wz-learning-' + uuid.uuid4().hex
    raw = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'.encode() + json.dumps(metadata).encode() +
        f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="{filename}"\r\nContent-Type: {mime}\r\n\r\n'.encode() + media_data + f'\r\n--{boundary}--\r\n'.encode())
    item = call('/workspace/uploads', 'POST', token=token, expected=201, raw=raw, content_type='multipart/form-data; boundary=' + boundary)
    return call('/workspace/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']}, token)


def browser_login(page, user):
    page.goto(BASE + '/learn/')
    page.get_by_label('用户邮箱', exact=True).fill(user['email'])
    page.get_by_label('登录密码', exact=True).fill(PASSWORD)
    page.get_by_role('button', name='进入个人学习', exact=True).click()
    expect(page.locator('.intro h1')).to_be_visible()


def main():
    (OUT / 'verification.json').unlink(missing_ok=True)
    subprocess.run(['node', str(ROOT / 'scripts/test-learning-player.cjs')], cwd=ROOT, check=True)
    subprocess.run(['go', 'build', '-o', str(BINARY), './cmd/api'], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory(prefix='isolated-', dir=OUT) as tmp:
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'COLLECTION_', 'AGENT_', 'DATABASE_', 'TEST_DATABASE_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(Path(tmp) / 'data'), HTTP_ADDR=f'127.0.0.1:{PORT}',
            ADMIN_API_KEY=ADMIN, READ_API_KEY=READER, SYSTEM_ADMIN_EMAIL='admin@learning.test',
            SYSTEM_ADMIN_PASSWORD=PASSWORD, POLL_ENABLED='false', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (Path(tmp) / 'service.log').open('ab') as log:
            process = subprocess.Popen([str(BINARY)], cwd=ROOT, env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        call('/health/ready')
                        break
                    except (URLError, AssertionError, OSError):
                        if process.poll() is not None:
                            raise RuntimeError('isolated server exited')
                        time.sleep(.1)
                else:
                    raise RuntimeError('isolated server readiness timeout')
                owner, ka = create_user('publisher')
                reader, kb = create_user('learner')
                outsider, kc = create_user('outsider')
                one = upload_lesson(ka, 'Episode One')
                two = upload_lesson(ka, 'Episode Two')
                check('audio subtitle/chapter upload, publication and real WAV streaming')
                own = call('/workspace/channel', token=ka)
                assert own['id'] == owner['id'] and own['visibility'] == 'private'
                call('/channels/' + own['id'] + '/items', token=kb, expected=404)
                check('per-account stable private channel default')
                errors = []
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True, args=['--autoplay-policy=no-user-gesture-required'])
                    publisher_context = browser.new_context(viewport={'width': 1440, 'height': 1000})
                    learner_context = browser.new_context(viewport={'width': 1440, 'height': 1100})
                    publisher = publisher_context.new_page()
                    page = learner_context.new_page()
                    for tab in [publisher, page]:
                        tab.on('pageerror', lambda error: errors.append(str(error)))
                    browser_login(publisher, owner)
                    publisher.get_by_role('button', name='我的渠道', exact=True).click()
                    expect(publisher.locator('#channel-form')).to_be_visible()
                    publisher.locator('select[name=visibility]').select_option('invited')
                    publisher.locator('input[name=source_ids]').first.check()
                    publisher.locator('textarea[name=invited_emails]').fill(reader['email'])
                    publisher.get_by_role('button', name='保存渠道设置', exact=True).click()
                    expect(publisher.locator('#notice')).to_contain_text('渠道设置已保存')
                    own = call('/workspace/channel', token=ka)
                    assert own['visibility'] == 'invited'
                    call('/channels/' + own['id'] + '/items', token=kc, expected=404)
                    call('/channels/' + own['id'] + '/items', expected=401)
                    discovered = call('/channels', token=kb)
                    assert len(discovered['items']) == 1 and 'invited_users' not in discovered['items'][0]
                    check('channel settings UI, authenticated invites and ACL metadata privacy')
                    browser_login(page, reader)
                    assert call('/items', token=kb)['items'] == []
                    page.get_by_role('button', name='共享渠道', exact=True).click()
                    page.get_by_role('button', name='浏览渠道资料', exact=True).click()
                    expect(page.locator('.lesson')).to_have_count(2)
                    page.locator('.lesson').first.get_by_role('button').click()
                    expect(page.locator('#learning-player')).to_be_visible()
                    expect(page.locator('#learning-sync')).to_contain_text('学习数据同步已开启')
                    page.wait_for_function('()=>document.querySelector("audio")?.duration === 8')
                    title = page.locator('.reader h1').inner_text()
                    check('shared lesson remains separate from own catalog and enables learning reporting')
                    page.get_by_role('button', name='歌词模式', exact=True).click()
                    expect(page.locator('#lyrics-list')).to_be_visible()
                    expect(page.locator('#subtitle-original')).to_have_text('Hello')
                    expect(page.locator('#subtitle-translation')).to_have_text('你好')
                    page.locator('#translation-track').select_option('-1')
                    expect(page.locator('#subtitle-translation')).to_have_text('')
                    page.locator('#translation-track').select_option('1')
                    expect(page.locator('#lyrics-list')).to_contain_text('继续跟读')
                    check('audio lyric mode and non-one-to-one bilingual subtitle alignment')
                    seek = page.locator('#media-seek')
                    seek.scroll_into_view_if_needed()
                    box = seek.bounding_box()
                    page.mouse.move(box['x'] + box['width'] * .05, box['y'] + box['height'] / 2)
                    page.mouse.down()
                    page.mouse.move(box['x'] + box['width'] * .64, box['y'] + box['height'] / 2, steps=10)
                    expect(page.locator('#seek-preview')).to_contain_text('Practice')
                    page.mouse.up()
                    assert 4 < page.locator('audio').evaluate('(m)=>m.currentTime') < 6
                    expect(page.locator('#media-chapter')).to_have_text('Practice')
                    check('real pointer progress drag shows time and chapter preview')
                    page.locator('audio').evaluate('(m)=>{m.currentTime=0}')
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    page.locator('audio').evaluate('(m)=>{m.currentTime=3;return m.play()}')
                    page.get_by_role('button', name='收藏', exact=True).click()
                    expect(page.get_by_role('button', name='已收藏', exact=True)).to_be_visible()
                    assert page.locator('audio').evaluate('(m)=>m.currentTime') >= 3
                    check('refreshing personal state does not seek back to old saved progress')
                    page.locator('#lyrics-list [data-cue="1"]').click()
                    page.get_by_role('button', name='单句循环', exact=True).click()
                    page.wait_for_timeout(2600)
                    position = page.locator('audio').evaluate('(m)=>m.currentTime')
                    assert 1.9 <= position < 4.1, position
                    expect(page.locator('#sentence-toggle')).to_have_attribute('aria-pressed', 'true')
                    page.get_by_role('button', name='单句循环', exact=True).click()
                    # An unchanged pointer gesture must not leave scrubbing on.
                    page.get_by_role('button', name='单句循环', exact=True).click()
                    page.locator('#media-seek').evaluate('(e)=>e.dispatchEvent(new PointerEvent("pointerdown",{bubbles:true}))')
                    page.evaluate('()=>document.dispatchEvent(new PointerEvent("pointerup",{bubbles:true}))')
                    page.wait_for_timeout(2300)
                    assert 1.9 <= page.locator('audio').evaluate('(m)=>m.currentTime') < 4.1
                    page.get_by_role('button', name='单句循环', exact=True).click()
                    check('language-learning sentence loop and unchanged-pointer gesture cleanup')
                    page.locator('#playback-mode').select_option('single')
                    page.locator('audio').evaluate('(m)=>{m.currentTime=7.8;return m.play()}')
                    page.wait_for_timeout(900)
                    expect(page.locator('.reader h1')).to_have_text(title)
                    assert page.locator('audio').evaluate('(m)=>m.currentTime') < 2
                    check('single-episode repeat does not advance')
                    page.locator('#playback-mode').select_option('range')
                    page.locator('#queue-start').select_option('0')
                    page.locator('#queue-end').select_option('1')
                    page.locator('audio').evaluate('(m)=>{m.currentTime=7.8;return m.play()}')
                    expect(page.locator('.reader h1')).not_to_have_text(title, timeout=10000)
                    second_title = page.locator('.reader h1').inner_text()
                    page.wait_for_function('()=>document.querySelector("audio")?.duration === 8')
                    page.locator('audio').evaluate('(m)=>{m.currentTime=7.8;return m.play()}')
                    expect(page.locator('.reader h1')).to_have_text(title, timeout=10000)
                    check('specified queue range automatically advances and wraps')
                    page.wait_for_function('()=>document.querySelector("audio")?.duration === 8')
                    page.locator('#playback-mode').select_option('auto')
                    page.locator('audio').evaluate('(m)=>{m.currentTime=7.8;return m.play()}')
                    expect(page.locator('.reader h1')).to_have_text(second_title, timeout=10000)
                    check('automatic next episode follows the authorized queue')
                    page.wait_for_function('()=>document.querySelector("audio")?.duration === 8')
                    page.locator('audio').evaluate('(m)=>m.pause()')
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    learning = call('/client/learning', token=kb)
                    assert learning['total'] == 2 and learning['duration_ms'] > 0
                    assert call('/client/learning', token=ka)['total'] == 0
                    assert call('/client/learning', token=kc)['total'] == 0
                    check('effective playback duration and progress belong only to the visitor account')
                    before = learning['duration_ms']
                    page.wait_for_timeout(1100)
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    assert call('/client/learning', token=kb)['duration_ms'] == before
                    check('paused media does not accumulate learning duration')
                    page.set_viewport_size({'width': 390, 'height': 844})
                    page.wait_for_timeout(200)
                    assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), 'mobile horizontal overflow'
                    page.screenshot(path=str(OUT / 'player-mobile.png'), full_page=True)
                    publisher.screenshot(path=str(OUT / 'channel-desktop.png'), full_page=True)
                    page.set_viewport_size({'width': 1440, 'height': 1100})
                    page.screenshot(path=str(OUT / 'player-desktop.png'), full_page=True)
                    check('desktop/mobile layout without horizontal overflow')
                    # A lost successful checkpoint must retry the identical event IDs.
                    lost = []
                    def lose_once(route):
                        payload = route.request.post_data_json
                        if not lost and payload.get('events', [{}])[0].get('action') == 'progress':
                            response = route.fetch()
                            assert response.status == 200
                            lost.append(payload)
                            route.fulfill(status=503, content_type='application/json', body=json.dumps({'error': {'message': 'simulated lost ack'}}))
                        else:
                            if lost:
                                assert payload == lost[0], 'retry changed the event identity or original payload'
                            route.continue_()
                    page.route('**/api/v1/client/learning/events', lose_once)
                    page.locator('audio').evaluate('(m)=>{m.currentTime=1.2}')
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    expect(page.locator('#notice')).to_contain_text('simulated lost ack')
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    expect(page.locator('#notice')).to_contain_text('当前播放位置已保存')
                    page.unroute('**/api/v1/client/learning/events', lose_once)
                    assert lost
                    check('lost successful checkpoint retries stable IDs and payload without double accounting')
                    # A stalled report cannot block leaving the lesson for 30 s.
                    held = []
                    page.route('**/api/v1/client/learning/events', lambda route: held.append(route))
                    start = time.monotonic()
                    page.locator('[data-action=back]').click()
                    expect(page.locator('.intro h1')).to_be_visible(timeout=5000)
                    assert time.monotonic() - start < 5
                    for route in held:
                        route.abort()
                    page.unroute('**/api/v1/client/learning/events')
                    check('stalled learning reports do not indefinitely block navigation')
                    # Encode a tiny local WebM fixture, without downloading media.
                    ffmpeg = shutil.which('ffmpeg')
                    assert ffmpeg, 'ffmpeg is required for the real video regression'
                    video_file = Path(tmp) / 'lesson.webm'
                    subprocess.run([ffmpeg, '-v', 'error', '-f', 'lavfi', '-i',
                        'color=c=0x244b3b:s=320x180:r=10', '-t', '8', '-c:v', 'libvpx',
                        '-b:v', '80k', '-an', '-y', str(video_file)], check=True)
                    video = upload_lesson(ka, 'Video Lesson', kind='video', media_data=video_file.read_bytes())
                    page.locator('#search button').click()
                    page.locator('.lesson').filter(has_text='Video Lesson').get_by_role('button').click()
                    expect(page.locator('#learning-player')).to_be_visible()
                    page.wait_for_function('()=>document.querySelector("video")?.readyState >= 2')
                    assert page.locator('video').evaluate('(m)=>m.videoWidth') == 320
                    page.get_by_role('button', name='歌词模式', exact=True).click()
                    page.locator('video').evaluate('(m)=>m.play()')
                    page.wait_for_timeout(800)
                    expect(page.locator('#subtitle-original')).to_have_text('Hello')
                    expect(page.locator('#subtitle-translation')).to_have_text('你好')
                    page.locator('video').evaluate('(m)=>m.pause()')
                    page.get_by_role('button', name='保存当前播放位置', exact=True).click()
                    page.screenshot(path=str(OUT / 'player-video-desktop.png'), full_page=True)
                    expect(page.locator('#personal-progress-status')).to_have_text('上次已保存，可跨设备继续。')
                    assert call('/client/learning', token=kb)['total'] == 3
                    check('real video playback also supports lyrics, translation and personal progress')
                    publisher.locator('select[name=visibility]').select_option('private')
                    publisher.get_by_role('button', name='保存渠道设置', exact=True).click()
                    expect(publisher.locator('#notice')).to_contain_text('渠道设置已保存')
                    item_id = one['id']
                    call('/channels/' + own['id'] + '/items/' + item_id, token=kb, expected=404)
                    asset = one['draft']['assets'][0]['id']
                    call('/channels/' + own['id'] + '/items/' + item_id + '/media/' + asset, token=kb, expected=404)
                    call('/client/learning/events', 'POST', {'events': [{'event_id': str(uuid.uuid4()), 'item_id': item_id, 'action': 'view'}]}, kb, 404)
                    check('revocation immediately blocks subsequent content, media and learning writes')
                    assert not errors, errors
                    check('no uncaught browser JavaScript errors')
                    browser.close()
                # File-store persistence: restart only this isolated service.
                process.terminate()
                process.wait(timeout=10)
                process = subprocess.Popen([str(BINARY)], cwd=ROOT, env=env, stdout=log, stderr=log)
                for _ in range(100):
                    try:
                        state = call('/workspace/channel', token=ka)
                        break
                    except (URLError, OSError):
                        time.sleep(.1)
                assert state['visibility'] == 'private' and state['revision'] >= 2
                assert call('/client/learning', token=kb)['total'] == 3
                check('channel ACL and visitor learning data survive file-store restart')
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
    (OUT / 'verification.json').write_text(json.dumps({'checks': CHECKS, 'count': len(CHECKS), 'production_touched': False}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(f'{len(CHECKS)} isolated browser/service checks passed.', flush=True)


if __name__ == '__main__':
    main()
