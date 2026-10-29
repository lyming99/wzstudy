"""Exercise administrator .env bootstrap against an isolated real PostgreSQL.

Windows only. Creates a fresh temporary project, a uniquely labelled Docker
volume, and synthetic credentials; never reads or modifies the user's profile.
Cleanup verifies volume ownership and retains the fixture if cleanup is unsafe.
"""
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from urllib.parse import quote
import json
import hashlib
import os
import re
import secrets
import shutil
import socket
import subprocess
import tempfile
import time

from verify_package import cleanup_fixture, read_fixture_env


def update_fixture_env(path, updates):
    lines = path.read_text(encoding='utf-8-sig').splitlines()
    found = set()
    for index, line in enumerate(lines):
        if line.lstrip().startswith('#') or '=' not in line:
            continue
        key = line.split('=', 1)[0].strip()
        if key in updates:
            lines[index] = f'{key}="{updates[key]}"'
            found.add(key)
    for key, value in updates.items():
        if key not in found:
            lines.append(f'{key}="{value}"')
    path.write_text('\n'.join(lines) + '\n', encoding='utf-8')

ROOT = Path(__file__).resolve().parents[1]


def free_port(excluded=0):
    while True:
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        if port != excluded:
            return port


def docker_environment():
    # Never propagate any real administrator initialization variables.
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'WZSTUDY_LOCAL_PG_'))
           and key not in ('DATA_DIR', 'DATABASE_URL', 'POSTGRES_PASSWORD')}
    host, context = env.get('DOCKER_HOST', ''), env.get('DOCKER_CONTEXT', '')
    if host and context:
        raise RuntimeError('Ambiguous Docker target; test not started')
    if not host:
        context = subprocess.run(['docker', 'context', 'show'], env=env, capture_output=True,
                                 text=True, check=True, timeout=15).stdout.strip()
        result = subprocess.run(['docker', 'context', 'inspect', context], env=env,
                                capture_output=True, text=True, check=True, timeout=15)
        entries = json.loads(result.stdout)
        if len(entries) != 1:
            raise RuntimeError('Invalid Docker context; test not started')
        host = entries[0]['Endpoints']['docker']['Host']
        env['DOCKER_CONTEXT'] = context
    if not (re.fullmatch(r'npipe:////\./pipe/[a-z0-9_.-]+', host, re.I) or
            re.fullmatch(r'unix:///\S+', host)):
        raise RuntimeError('Only a local Docker endpoint is permitted')
    subprocess.run(['docker', 'version', '--format', '{{.Server.Version}}'], env=env,
                   capture_output=True, check=True, timeout=20)
    return env


def wait_ready(process, base):
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError('Isolated service exited; fixture log is not printed')
        try:
            with urlopen(base + '/api/v1/health/ready', timeout=1) as response:
                if response.status == 200:
                    return
        except (URLError, OSError):
            time.sleep(.5)
    raise RuntimeError('Isolated service readiness timed out')


def login_status(base, email, password):
    request = Request(base + '/api/v1/auth/login', method='POST',
                      data=json.dumps({'email': email, 'password': password}).encode('utf-8'),
                      headers={'Content-Type': 'application/json'})
    try:
        with urlopen(request, timeout=10) as response:
            return response.status
    except HTTPError as error:
        return error.code


def fixture_request(base, path, method='GET', body=None, *, key=None, cookie=None):
    headers = {}
    raw = None
    if body is not None:
        raw = json.dumps(body).encode('utf-8')
        headers['Content-Type'] = 'application/json'
    if key:
        headers['Authorization'] = 'Bearer ' + key
    if cookie:
        headers['Cookie'] = cookie
    try:
        with urlopen(Request(base + '/api/v1' + path, method=method, data=raw, headers=headers), timeout=15) as response:
            payload = json.loads(response.read())
            return response.status, payload.get('data'), response.headers.get('Set-Cookie', '').split(';', 1)[0]
    except HTTPError as error:
        return error.code, None, ''


def stop(process):
    if process is not None and process.poll() is None:
        subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'],
                       capture_output=True, timeout=30)
        process.wait(timeout=15)


def deny_take_ownership(ps, path):
    quoted = str(path).replace("'", "''")
    command = (f"$ErrorActionPreference='Stop'; $p='{quoted}'; "
               "$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User; "
               "$a=[IO.File]::GetAccessControl($p,[Security.AccessControl.AccessControlSections]::Access); "
               "$null=$a.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,[Security.AccessControl.FileSystemRights]::TakeOwnership,[Security.AccessControl.AccessControlType]::Deny)); "
               "[IO.File]::SetAccessControl($p,$a)")
    subprocess.run([ps, '-NoProfile', '-NonInteractive', '-Command', command], check=True, capture_output=True, timeout=30)


def main():
    assert os.name == 'nt', 'Windows is required'
    ps, go = shutil.which('powershell.exe'), shutil.which('go')
    assert ps and go, 'Windows PowerShell and Go are required'
    docker_env = docker_environment()
    parent = ROOT / '.tmp'
    parent.mkdir(exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix='postgres-admin-env-', dir=parent))
    project = temporary / 'isolated project'
    project.mkdir()
    projects = [project]
    process = None
    log = None
    preserve = False
    password = '  ' + secrets.token_urlsafe(24) + '=$  '
    changed_password = secrets.token_urlsafe(24)
    email = 'bootstrap@example.test'
    try:
        for name in ['start-local.ps1', 'start-postgres-local.ps1',
                     'scripts/docker-compose-local-postgres.yml',
                     'scripts/local-service-lifecycle.ps1', 'scripts/admin-environment.ps1',
                     'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1']:
            dest = project / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, dest)
        (project / 'dist').mkdir()
        for output, source in [('wzstudy-migrate.exe', './cmd/migrate'),
                               ('wzstudy-v0.3.0-windows-amd64.exe', './cmd/api')]:
            subprocess.run([go, 'build', '-trimpath', '-o', str(project / 'dist' / output), source],
                           cwd=ROOT, env=docker_env, check=True, capture_output=True, timeout=180)
        env_file = project / '.env'
        env_file.write_text(f'SYSTEM_ADMIN_EMAIL={email}\nSYSTEM_ADMIN_PASSWORD="{password}"\n'
                            'SYSTEM_ADMIN_DISPLAY_NAME=测试管理员\n', encoding='utf-8')
        # Reproduce a non-admin owner who can edit the DACL but lacks WRITE_OWNER.
        deny_take_ownership(ps, env_file)
        api_port = free_port()
        db_port = free_port(api_port)
        base = f'http://127.0.0.1:{api_port}'

        def launch(log_name, target=project, api=api_port, database=db_port):
            handle = (temporary / log_name).open('w', encoding='utf-8')
            try:
                proc = subprocess.Popen([ps, '-NoLogo', '-NoProfile', '-NonInteractive',
                                         '-ExecutionPolicy', 'Bypass', '-File',
                                         str(target / 'start-local.ps1'), '-SkipBuild',
                                         '-ApiPort', str(api), '-DatabasePort', str(database)],
                                        cwd=target, env=docker_env, stdout=handle, stderr=subprocess.STDOUT)
            except BaseException:
                handle.close()
                raise
            return proc, handle

        process, log = launch('first.log')
        wait_ready(process, base)
        assert login_status(base, email, password) == 200, 'Configured .env credentials did not log in'
        assert login_status(base, email, password.strip()) == 401, 'Password whitespace was not preserved'
        assert not (project / '.local/postgres-local/admin-initial.json').exists()
        assert not (project / '.local/postgres-local/connection.json').exists()
        helper = str(project / 'scripts/local-env.ps1').replace("'", "''")
        protected_path = str(env_file).replace("'", "''")
        subprocess.run([ps, '-NoProfile', '-NonInteractive', '-Command',
                        f"$ErrorActionPreference='Stop'; . '{helper}'; Assert-LocalEnvPrivate '{protected_path}'"],
                       check=True, capture_output=True, timeout=30)
        generated_credentials = read_fixture_env(env_file)
        persistent_keys = ('DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY','WZSTUDY_LOCAL_INSTANCE_ID','WZSTUDY_LOCAL_VOLUME_STATE')
        generated_before = {key: generated_credentials[key] for key in persistent_keys}
        assert len(generated_credentials['POSTGRES_PASSWORD']) >= 32
        learner_email = 'learner@example.test'
        learner_password = secrets.token_urlsafe(24)
        status, learner, _ = fixture_request(base, '/admin/users', 'POST',
            {'email': learner_email, 'display_name': '学习用户', 'password': learner_password}, key=generated_credentials['ADMIN_API_KEY'])
        assert status == 201 and learner['email'] == learner_email
        status, admin_login, _ = fixture_request(base, '/auth/login', 'POST', {'email': email, 'password': password})
        assert status == 200 and admin_login['role'] == 'admin'
        status, user_login, user_cookie = fixture_request(base, '/auth/login', 'POST', {'email': learner_email, 'password': learner_password})
        assert status == 200 and user_login['role'] == 'user' and user_login['user']['id'] == learner['id']
        assert user_cookie.startswith('wzstudy_user=')
        assert fixture_request(base, '/admin/users', cookie=user_cookie)[0] == 401
        assert fixture_request(base, '/admin/users', 'POST',
            {'email': email, 'display_name': 'duplicate', 'password': learner_password}, key=generated_credentials['ADMIN_API_KEY'])[0] == 409
        stop(process)
        process = None
        log.close()
        log = None
        first_log = (temporary / 'first.log').read_text(encoding='utf-8', errors='replace')
        assert password not in first_log and 'First administrator email:' in first_log
        print('PASS: isolated real PostgreSQL initialized from .env; exact password accepted and not logged', flush=True)

        update_fixture_env(env_file, {'SYSTEM_ADMIN_EMAIL': 'changed@example.test',
                                      'SYSTEM_ADMIN_PASSWORD': changed_password})
        changed_before = env_file.read_bytes()
        process, log = launch('email-mismatch.log')
        assert process.wait(timeout=120) != 0, 'A mismatched configured admin email must not take over the old account'
        process = None
        log.close()
        log = None
        mismatch_log = (temporary / 'email-mismatch.log').read_text(encoding='utf-8', errors='replace')
        assert 'configured system administrator email does not match' in mismatch_log
        assert changed_password not in mismatch_log and env_file.read_bytes() == changed_before
        update_fixture_env(env_file, {'SYSTEM_ADMIN_EMAIL': email})
        process, log = launch('restart.log')
        wait_ready(process, base)
        assert login_status(base, email, password) == 200, 'Original administrator was not preserved'
        assert login_status(base, email, changed_password) == 401
        status, restored_user, _ = fixture_request(base, '/auth/login', 'POST', {'email': learner_email, 'password': learner_password})
        assert status == 200 and restored_user['role'] == 'user' and restored_user['user']['id'] == learner['id']
        after = read_fixture_env(env_file)
        assert {key: after[key] for key in persistent_keys} == generated_before
        stop(process)
        process = None
        log.close()
        log = None
        restart_log = (temporary / 'restart.log').read_text(encoding='utf-8', errors='replace')
        assert 'no password was reset' in restart_log and changed_password not in restart_log
        print('PASS: mismatched admin email was rejected; original account and generated database password were preserved', flush=True)

        configured = temporary / 'configured database project'
        configured.mkdir()
        projects.append(configured)
        for name in ['start-local.ps1', 'start-postgres-local.ps1',
                     'scripts/docker-compose-local-postgres.yml', 'scripts/local-service-lifecycle.ps1',
                     'scripts/admin-environment.ps1', 'scripts/local-env.ps1',
                     'scripts/local-postgres-state.ps1', 'dist/wzstudy-migrate.exe',
                     'dist/wzstudy-v0.3.0-windows-amd64.exe']:
            dest = configured / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(project / name, dest)
        configured_api = free_port()
        configured_db = free_port(configured_api)
        database_password = '  Db@:/%#密码$' + secrets.token_urlsafe(12) + '  '
        database_url = f'postgresql://custom_user:{quote(database_password, safe="")}@localhost:{configured_db}/custom_data?sslmode=disable'
        (configured / '.env').write_text(
            f'DATABASE_URL={database_url}\nPOSTGRES_PASSWORD="{database_password}"\n'
            f'SYSTEM_ADMIN_EMAIL={email}\nSYSTEM_ADMIN_PASSWORD="{password}"\n', encoding='utf-8')
        configured_base = f'http://127.0.0.1:{configured_api}'
        process, log = launch('configured.log', configured, configured_api, configured_db)
        wait_ready(process, configured_base)
        assert login_status(configured_base, email, password) == 200
        configured_env_file = configured / '.env'
        configured_before = configured_env_file.read_bytes()
        actual = read_fixture_env(configured_env_file)
        assert actual['DATABASE_URL'] == database_url and actual['POSTGRES_PASSWORD'] == database_password
        assert not (configured / '.local/postgres-local/connection.json').exists()
        assert not (configured / '.local/postgres-local/admin-initial.json').exists()
        stop(process)
        process = None
        log.close()
        log = None
        configured_log = (temporary / 'configured.log').read_text(encoding='utf-8', errors='replace')
        assert database_password not in configured_log and database_url not in configured_log
        process, log = launch('configured-restart.log', configured, configured_api, configured_db)
        wait_ready(process, configured_base)
        assert login_status(configured_base, email, password) == 200
        assert configured_env_file.read_bytes() == configured_before
        stop(process)
        process = None
        log.close()
        log = None
        print('PASS: real PostgreSQL used the configured database user, name and special-character password on startup and restart', flush=True)

        conflicting_password = secrets.token_urlsafe(24)
        configured_env_file = configured / '.env'
        configured_env_before = configured_env_file.read_bytes()
        conflict_url = database_url.replace(quote(database_password, safe=''), quote(conflicting_password, safe=''))
        update_fixture_env(configured_env_file, {'DATABASE_URL': conflict_url, 'POSTGRES_PASSWORD': conflicting_password})
        conflict_before = configured_env_file.read_bytes()
        process, log = launch('conflict.log', configured, configured_api, configured_db)
        assert process.wait(timeout=120) != 0, 'Incorrect database credentials must be rejected'
        process = None
        log.close()
        log = None
        conflict_log = (temporary / 'conflict.log').read_text(encoding='utf-8', errors='replace')
        assert ('database connection' in conflict_log.lower() or 'migration failed' in conflict_log.lower()) and conflicting_password not in conflict_log
        assert configured_env_file.read_bytes() == conflict_before
        configured_env_file.write_bytes(configured_env_before)
        process, log = launch('after-conflict.log', configured, configured_api, configured_db)
        wait_ready(process, configured_base)
        assert login_status(configured_base, email, password) == 200
        assert configured_env_file.read_bytes() == configured_before
        stop(process)
        process = None
        log.close()
        log = None
        print('PASS: conflicting database settings were rejected and the real existing database remained usable', flush=True)

        # Recreate the pre-.env-only layout inside this isolated fixture and
        # prove a real, already-populated volume migrates without resetting it.
        settings = read_fixture_env(configured_env_file)
        suffix = hashlib.sha256(str(configured.resolve()).lower().encode('utf-8')).hexdigest()[:12]
        project_name = 'wzstudy-local-' + suffix
        oldprofile = configured / '.local/postgres-local'
        oldprofile.mkdir(parents=True, exist_ok=True)
        legacy = {
            'version': 1, 'project': project_name, 'volume': project_name + '-pgdata',
            'instance_id': settings['WZSTUDY_LOCAL_INSTANCE_ID'], 'database_host': '127.0.0.1',
            'database_port': configured_db, 'database_user': 'custom_user', 'database_name': 'custom_data',
            'database_password': database_password,
            'database_url': f'postgres://custom_user:{quote(database_password, safe="")}@127.0.0.1:{configured_db}/custom_data?sslmode=disable',
            'admin_api_key': settings['ADMIN_API_KEY'], 'read_api_key': settings['READ_API_KEY'],
        }
        old_connection = oldprofile / 'connection.json'
        old_connection.write_text(json.dumps(legacy, ensure_ascii=False), encoding='utf-8')
        helper = str(ROOT / 'scripts/local-env.ps1').replace("'", "''")
        legacy_path = str(old_connection).replace("'", "''")
        command = f"$ErrorActionPreference='Stop'; . '{helper}'; $s=Read-LocalEnvSnapshot '{legacy_path}'; Protect-LocalEnv $s"
        subprocess.run([ps, '-NoProfile', '-NonInteractive', '-Command', command], check=True, capture_output=True, timeout=30)
        marker = oldprofile / 'volume-created.txt'
        marker.write_text(settings['WZSTUDY_LOCAL_INSTANCE_ID'], encoding='utf-8')
        old_before = old_connection.read_bytes()
        omit = {'ADMIN_API_KEY','READ_API_KEY','WZSTUDY_LOCAL_INSTANCE_ID','WZSTUDY_LOCAL_VOLUME_STATE'}
        original_lines = configured_env_file.read_text(encoding='utf-8-sig').splitlines()
        configured_env_file.write_text('\n'.join(line for line in original_lines if line.split('=',1)[0].strip() not in omit) + '\n', encoding='utf-8')
        process, log = launch('legacy-migration.log', configured, configured_api, configured_db)
        wait_ready(process, configured_base)
        assert login_status(configured_base, email, password) == 200
        migrated = read_fixture_env(configured_env_file)
        assert migrated['WZSTUDY_LOCAL_INSTANCE_ID'] == settings['WZSTUDY_LOCAL_INSTANCE_ID']
        assert migrated['ADMIN_API_KEY'] == settings['ADMIN_API_KEY'] and migrated['READ_API_KEY'] == settings['READ_API_KEY']
        assert old_connection.read_bytes() == old_before and marker.exists()
        stop(process)
        process = None
        log.close()
        log = None
        old_connection.unlink()
        marker.unlink()
        process, log = launch('without-legacy.log', configured, configured_api, configured_db)
        wait_ready(process, configured_base)
        assert login_status(configured_base, email, password) == 200 and not old_connection.exists()
        stop(process)
        process = None
        log.close()
        log = None
        print('PASS: real existing PostgreSQL migrated legacy settings into .env and restarted without the legacy files', flush=True)

        generated = temporary / 'generated env project'
        generated.mkdir()
        projects.append(generated)
        for name in ['start-local.ps1', 'start-postgres-local.ps1',
                     'scripts/docker-compose-local-postgres.yml', 'scripts/local-service-lifecycle.ps1',
                     'scripts/admin-environment.ps1', 'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1',
                     'dist/wzstudy-migrate.exe', 'dist/wzstudy-v0.3.0-windows-amd64.exe']:
            dest = generated / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(project / name, dest)
        generated_api = free_port()
        generated_db = free_port(generated_api)
        generated_base = f'http://127.0.0.1:{generated_api}'
        process, log = launch('all-generated.log', generated, generated_api, generated_db)
        wait_ready(process, generated_base)
        generated_env_file = generated / '.env'
        new_values = read_fixture_env(generated_env_file)
        assert len(new_values['POSTGRES_PASSWORD']) >= 32 and len(new_values['SYSTEM_ADMIN_PASSWORD']) >= 32
        assert login_status(generated_base, new_values['SYSTEM_ADMIN_EMAIL'], new_values['SYSTEM_ADMIN_PASSWORD']) == 200
        assert not (generated / '.local/postgres-local/connection.json').exists()
        assert not (generated / '.local/postgres-local/admin-initial.json').exists()
        env_before = generated_env_file.read_bytes()
        stop(process)
        process = None
        log.close()
        log = None
        generated_log = (temporary / 'all-generated.log').read_text(encoding='utf-8', errors='replace')
        assert all(new_values[key] not in generated_log for key in ('POSTGRES_PASSWORD','DATABASE_URL','ADMIN_API_KEY','READ_API_KEY','SYSTEM_ADMIN_PASSWORD'))
        process, log = launch('all-generated-restart.log', generated, generated_api, generated_db)
        wait_ready(process, generated_base)
        assert generated_env_file.read_bytes() == env_before
        assert login_status(generated_base, new_values['SYSTEM_ADMIN_EMAIL'], new_values['SYSTEM_ADMIN_PASSWORD']) == 200
        stop(process)
        process = None
        log.close()
        log = None
        print('PASS: missing .env was generated with database and administrator credentials; real login and restart succeeded', flush=True)
    finally:
        try:
            stop(process)
        except Exception:
            preserve = True
            print('WARNING: isolated launcher did not stop; fixture and volume retained', flush=True)
        if log is not None:
            log.close()
        if not preserve:
            for fixture in reversed(projects):
                try:
                    cleanup_fixture(fixture, docker_env)
                except Exception as error:
                    preserve = True
                    print(f'WARNING: ownership-checked cleanup failed ({type(error).__name__}); fixture retained', flush=True)
        if preserve:
            print(f'Isolated fixture retained at: {temporary}', flush=True)
        else:
            shutil.rmtree(temporary)
    if preserve:
        raise RuntimeError('Isolated resources could not be safely cleaned up')


if __name__ == '__main__':
    main()
