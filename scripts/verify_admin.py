"""Verify startup-only administrator bootstrap against an isolated real service.
Uses random test passwords/keys and never reads the user's .env or .local files.
"""
from contextlib import ExitStack
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
BINARY = ARTIFACTS / 'wzstudy.exe'
EMAIL = 'administrator@example.test'
PASSWORD = '  ' + secrets.token_urlsafe(24) + '  '
ADMIN_KEY = secrets.token_urlsafe(32)
READ_KEY = secrets.token_urlsafe(32)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    PORT = sock.getsockname()[1]
BASE = f'http://127.0.0.1:{PORT}'
CHECKS = []
process = None
sys.stdout.reconfigure(encoding='utf-8')


def stop():
    global process
    if process is not None:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        process = None


def call(path, method='GET', body=None, key=None):
    headers = {'Authorization': 'Bearer ' + key} if key else {}
    if body is not None:
        headers['Content-Type'] = 'application/json'
    req = Request(BASE + '/api/v1' + path, method=method, headers=headers,
                  data=json.dumps(body).encode() if body is not None else None)
    try:
        with urlopen(req, timeout=10) as response:
            return response.status, json.load(response), response.headers
    except HTTPError as response:
        return response.code, json.load(response), response.headers


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


with ExitStack() as stack:
    temp = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix='admin-auth-', dir=ARTIFACTS)))
    log = stack.enter_context((temp / 'service.log').open('ab'))
    stack.callback(stop)
    data = temp / 'data'
    env = dict(os.environ)
    for name in list(env):
        if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
            env.pop(name)
    env.update(APP_ENV='test', STORE='file', DATA_DIR=str(data), HTTP_ADDR=f'127.0.0.1:{PORT}',
               ADMIN_API_KEY=ADMIN_KEY, READ_API_KEY=READ_KEY, POLL_ENABLED='false')

    def start(overrides=None, fail=False):
        global process
        stop()
        process = subprocess.Popen([str(BINARY)], cwd=ROOT, env={**env, **(overrides or {})},
                                   stdout=log, stderr=subprocess.STDOUT)
        if fail:
            assert process.wait(timeout=15) != 0, 'Unsafe bootstrap unexpectedly started'
            process = None
            return
        for _ in range(100):
            assert process.poll() is None, 'Service exited; test logs intentionally not printed'
            try:
                with urlopen(BASE + '/api/v1/health/ready', timeout=.3) as response:
                    if response.status == 200:
                        return
            except (URLError, OSError):
                time.sleep(.1)
        raise AssertionError('Administrator test service did not become ready')

    start(fail=True)
    assert not (data / 'workspace.json').exists()
    check('First startup without administrator email/password fails closed and does not create an account')

    start({'SYSTEM_ADMIN_EMAIL': ' Administrator@Example.TEST ', 'SYSTEM_ADMIN_PASSWORD': PASSWORD,
           'SYSTEM_ADMIN_DISPLAY_NAME': '系统管理员'})
    status, body, headers = call('/auth/login', 'POST', {'email': EMAIL.upper(), 'password': PASSWORD})
    assert status == 200 and body['data']['administrator']['email'] == EMAIL
    identity = body['data']['administrator']['id']
    assert 'HttpOnly' in headers['Set-Cookie'] and 'SameSite=Strict' in headers['Set-Cookie']
    raw = (data / 'workspace.json').read_text(encoding='utf-8')
    assert PASSWORD not in raw and '$argon2id$' in raw
    check('Configured email is normalized; exact password whitespace is retained; only salted Argon2id hash is persisted')

    bad = call('/auth/login', 'POST', {'email': EMAIL, 'password': 'incorrect-password'})
    unknown = call('/auth/login', 'POST', {'email': 'unknown@example.test', 'password': 'incorrect-password'})
    assert bad[0] == unknown[0] == 401 and bad[1] == unknown[1]
    assert call('/auth/login', 'POST', {'access_key': ADMIN_KEY})[0] == 400
    assert call('/auth/login', 'POST', {'email': EMAIL, 'password': ADMIN_KEY})[0] == 401
    check('Unknown email and wrong password share one error; API key cannot be used for browser sign-in')

    status, body, _ = call('/admin/items', 'POST', {'status': 'pending', 'content': {
        'title': 'Keep existing learning material', 'content': 'Persistent content', 'format': 'text'}}, ADMIN_KEY)
    assert status == 201
    item_id = body['data']['id']
    check('Existing administrator API automation continues to work without exposing the password hash')

    start()
    status, body, _ = call('/auth/login', 'POST', {'email': EMAIL, 'password': PASSWORD})
    assert status == 200 and body['data']['administrator']['id'] == identity
    assert call('/admin/items/' + item_id, key=ADMIN_KEY)[0] == 200
    check('Restart without bootstrap environment retains administrator identity, password and learning material')

    start({'SYSTEM_ADMIN_EMAIL': EMAIL, 'SYSTEM_ADMIN_PASSWORD': 'a-different-configured-password'})
    assert call('/auth/login', 'POST', {'email': EMAIL, 'password': PASSWORD})[0] == 200
    assert call('/auth/login', 'POST', {'email': EMAIL, 'password': 'a-different-configured-password'})[0] == 401
    check('Repeated startup with the same email never resets the existing password')

    start({'SYSTEM_ADMIN_EMAIL': 'other@example.test', 'SYSTEM_ADMIN_PASSWORD': 'another-password'}, fail=True)
    start()
    assert call('/auth/login', 'POST', {'email': EMAIL, 'password': PASSWORD})[0] == 200
    assert call('/admin/items/' + item_id, key=ADMIN_KEY)[0] == 200
    check('A different configured administrator is rejected without replacing the account or modifying existing material')
    stop()

report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS,
          'version': '0.3.0', 'credentials': 'isolated random test values; never printed',
          'reference': 'wenzflow startup-only SYSTEM_ADMIN bootstrap rules',
          'postgres_integration': 'not run by this isolated file-store suite'}
(ARTIFACTS / 'admin-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
