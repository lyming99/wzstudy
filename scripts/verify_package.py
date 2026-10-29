"""Verify a PostgreSQL-backed Windows release archive without using real user data.

--static checks the archive and read-only launcher diagnostics only. Without
--static, an isolated Docker PostgreSQL integration test is also required; it
creates a uniquely named temporary project/volume and removes only resources
whose private ownership label matches that fixture. Cleanup failure preserves
its temporary profile and credentials for recovery.
"""
from pathlib import Path
from urllib.error import URLError
from urllib.request import Request, urlopen
from urllib.parse import urlsplit, unquote
import argparse
import hashlib
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import zipfile

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_ARCHIVE = ROOT / 'dist/wzstudy-v0.3.0-windows-amd64.zip'
REQUIRED = {
    'start-local.ps1', 'start-postgres-local.ps1', 'start-local.cmd',
    'scripts/docker-compose-local-postgres.yml', 'scripts/local-service-lifecycle.ps1',
    'scripts/admin-environment.ps1', 'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1',
    'dist/wzstudy-v0.3.0-windows-amd64.exe', 'dist/wzstudy-migrate.exe',
}


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def local_docker_environment():
    """Resolve a local-only Docker target before any daemon or cleanup call."""
    env = dict(os.environ)
    host, context = env.get('DOCKER_HOST', ''), env.get('DOCKER_CONTEXT', '')
    if host and context:
        raise RuntimeError('Ambiguous Docker target; no integration test was started')
    if not host:
        result = subprocess.run(['docker', 'context', 'show'], env=env, capture_output=True, text=True, check=True, timeout=15)
        context = result.stdout.strip()
        result = subprocess.run(['docker', 'context', 'inspect', context], env=env, capture_output=True, text=True, check=True, timeout=15)
        entries = json.loads(result.stdout)
        if len(entries) != 1:
            raise RuntimeError('Invalid Docker context; no integration test was started')
        host = entries[0]['Endpoints']['docker']['Host']
        env['DOCKER_CONTEXT'] = context  # Keep cleanup on this same local context.
    if not (re.fullmatch(r'npipe:////\./pipe/[a-z0-9_.-]+', host, re.I) or
            re.fullmatch(r'unix:///\S+', host)):
        raise RuntimeError('Only a local Docker context is accepted; no integration test was started')
    subprocess.run(['docker', 'version', '--format', '{{.Server.Version}}'], env=env,
                   capture_output=True, check=True, timeout=20)
    return env


def inspect_volume(docker_env, volume):
    result = subprocess.run(['docker', 'volume', 'inspect', volume], env=docker_env,
                            capture_output=True, text=True, timeout=20)
    if result.returncode:
        listing = subprocess.run(['docker', 'volume', 'ls', '--filter', f'name={volume}', '--format', '{{.Name}}'],
                                 env=docker_env, capture_output=True, text=True, timeout=20, check=True)
        if volume in {line.strip() for line in listing.stdout.splitlines()}:
            raise RuntimeError('Could not inspect the fixture volume; resources were retained')
        return None
    data = json.loads(result.stdout)
    if len(data) != 1 or data[0].get('Name') != volume:
        raise RuntimeError('Unexpected Docker volume metadata')
    return data[0]


def read_fixture_env(path):
    """Read synthetic fixture .env values only; do not print credentials."""
    values = {}
    for line in path.read_text(encoding='utf-8-sig').splitlines():
        if line.lstrip().startswith('#') or '=' not in line:
            continue
        name, value = line.split('=', 1)
        name = name.strip().upper()
        if name in values:
            raise RuntimeError('Duplicate fixture environment key')
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ('"', "'"):
            value = value[1:-1]
        if not name.endswith('PASSWORD'):
            value = value.strip()
        values[name] = value
    return values


def cleanup_fixture(target, docker_env):
    suffix = hashlib.sha256(str(target.resolve()).lower().encode('utf-8')).hexdigest()[:12]
    expected_project = 'wzstudy-local-' + suffix
    expected_volume = expected_project + '-pgdata'
    volume = inspect_volume(docker_env, expected_volume)
    env_path = target / '.env'
    if not env_path.exists():
        if volume is not None:
            raise RuntimeError('Fixture .env is missing while its volume exists; resources retained')
        return
    values = read_fixture_env(env_path)
    instance = values.get('WZSTUDY_LOCAL_INSTANCE_ID', '')
    state = values.get('WZSTUDY_LOCAL_VOLUME_STATE', '')
    if not instance and not state and volume is None:
        return
    if not re.fullmatch(r'[a-f0-9]{32}', instance) or state not in ('pending', 'created'):
        raise RuntimeError('Fixture ownership cannot be confirmed; resources retained')
    if volume is None:
        if state == 'created':
            raise RuntimeError('Fixture volume is missing; configuration retained')
        return
    if (volume.get('Labels') or {}).get('org.wzstudy.local-instance') != instance:
        raise RuntimeError('Fixture volume ownership mismatch; resources retained')
    url = urlsplit(values.get('DATABASE_URL', ''))
    password = values.get('POSTGRES_PASSWORD', '')
    if (url.scheme not in ('postgres','postgresql') or url.hostname not in ('127.0.0.1','localhost') or
            not url.username or not url.path.strip('/') or unquote(url.password or '') != password):
        raise RuntimeError('Fixture database configuration is invalid; resources retained')
    env = dict(docker_env, WZSTUDY_LOCAL_PG_PASSWORD=password,
               WZSTUDY_LOCAL_PG_PORT=str(url.port or 5432), WZSTUDY_LOCAL_PG_VOLUME=expected_volume,
               WZSTUDY_LOCAL_PG_USER=unquote(url.username), WZSTUDY_LOCAL_PG_DATABASE=unquote(url.path[1:]))
    empty = target / 'cleanup-compose.env'
    empty.write_text('# isolated test; values are supplied through the environment\n', encoding='utf-8')
    command = ['docker', 'compose', '--project-directory', str(target), '--env-file', str(empty),
               '-p', expected_project, '-f', str(target / 'scripts/docker-compose-local-postgres.yml'), 'down']
    subprocess.run(command, env=env, capture_output=True, check=True, timeout=60)
    volume = inspect_volume(docker_env, expected_volume)
    if volume is None or (volume.get('Labels') or {}).get('org.wzstudy.local-instance') != instance:
        raise RuntimeError('Fixture volume changed during cleanup; resources retained')
    subprocess.run(['docker', 'volume', 'rm', expected_volume], env=docker_env,
                   capture_output=True, check=True, timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--static', action='store_true', help='Check archive and launcher diagnostics only; do not claim a runtime pass')
    parser.add_argument('--archive', type=Path, default=DEFAULT_ARCHIVE)
    args = parser.parse_args()
    assert os.name == 'nt', 'This verifier targets a Windows release archive'
    ps = shutil.which('powershell.exe')
    assert ps and args.archive.is_file(), 'Windows PowerShell and the release archive are required'
    parent = ROOT / '.tmp'
    parent.mkdir(exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix='postgres-package-test-', dir=parent))
    target = temporary / 'isolated package'
    target.mkdir()
    process = None
    log_handle = None
    docker_env = None
    preserve = False
    try:
        with zipfile.ZipFile(args.archive) as archive:
            names = set(archive.namelist())
            assert REQUIRED <= names, f'Missing launcher dependencies: {REQUIRED - names}'
            for name in names:
                parts = Path(name).parts
                assert not Path(name).is_absolute() and '..' not in parts
                assert '.local' not in parts and '.env' not in parts
            assert archive.testzip() is None
            archive.extractall(target)
        manifest_names = set()
        for line in (target / 'SHA256SUMS.txt').read_text(encoding='utf-8').splitlines():
            digest, name = line.split('  ', 1)
            parts = Path(name).parts
            assert name in names and name not in manifest_names and not Path(name).is_absolute() and '..' not in parts
            assert re.fullmatch(r'[a-f0-9]{64}', digest)
            manifest_names.add(name)
            assert hashlib.sha256((target / name).read_bytes()).hexdigest() == digest, name
        assert REQUIRED <= manifest_names, 'Every launcher dependency must be covered by the checksum manifest'
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')) and key != 'DATA_DIR'}
        check = subprocess.run([ps, '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
                                '-File', str(target / 'start-local.ps1'), '-Check'], cwd=temporary, env=env,
                               capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=30, check=True)
        config = json.loads(check.stdout.lstrip('\ufeff'))
        assert config['store'] == 'postgres' and Path(config['data_directory']) == target / '.local/postgres-local/data'
        assert not (target / '.local').exists()
        print('PASS: archive checksums and mandatory PostgreSQL launcher dependencies are valid')
        print('PASS: packaged -Check resolves PostgreSQL and the default attachment path without creating data')
        if args.static:
            print('PARTIAL: no actual packaged API, Docker, migration or PostgreSQL integration was tested')
            return

        docker_env = local_docker_environment()
        for key in list(docker_env):
            if key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'WZSTUDY_LOCAL_PG_')) or key in ('DATA_DIR', 'DATABASE_URL', 'POSTGRES_PASSWORD'):
                docker_env.pop(key, None)
        api_port, db_port = free_port(), free_port()
        while db_port == api_port:
            db_port = free_port()
        log_handle = (temporary / 'launcher.log').open('w', encoding='utf-8')
        process = subprocess.Popen([ps, '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
                                    '-File', str(target / 'start-local.ps1'), '-ApiPort', str(api_port),
                                    '-DatabasePort', str(db_port)], cwd=temporary, env=docker_env,
                                   stdout=log_handle, stderr=subprocess.STDOUT)
        base = f'http://127.0.0.1:{api_port}'
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError('Isolated packaged launcher exited; its log was not printed because it may contain sensitive details')
            try:
                with urlopen(base + '/api/v1/health/ready', timeout=1) as response:
                    if response.status == 200:
                        break
            except (URLError, OSError):
                time.sleep(.5)
        else:
            raise RuntimeError('Isolated packaged service did not become ready')
        settings = read_fixture_env(target / '.env')
        assert not (target / '.local/postgres-local/connection.json').exists()
        assert not (target / '.local/postgres-local/admin-initial.json').exists()
        login = Request(base + '/api/v1/auth/login', method='POST',
                        data=json.dumps({'email': settings['SYSTEM_ADMIN_EMAIL'], 'password': settings['SYSTEM_ADMIN_PASSWORD']}).encode(),
                        headers={'Content-Type': 'application/json'})
        with urlopen(login, timeout=10) as response:
            assert response.status == 200
        with urlopen(base + '/workspace/', timeout=10) as response:
            assert response.status == 200
        print('PASS: isolated packaged API started with real PostgreSQL, applied migrations, and accepted its generated administrator')
    finally:
        if process is not None and process.poll() is None:
            subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'], capture_output=True, timeout=30)
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                preserve = True
                print('WARNING: isolated launcher did not exit; fixture and resources were retained', file=sys.stderr)
        if log_handle:
            log_handle.close()
        if docker_env is not None and not preserve:
            try:
                cleanup_fixture(target, docker_env)
            except Exception as error:
                preserve = True
                print(f'WARNING: fixture cleanup failed ({type(error).__name__}); credentials and resources retained', file=sys.stderr)
        if preserve:
            print(f'Isolated fixture retained at: {temporary}', file=sys.stderr)
        else:
            shutil.rmtree(temporary)
    if preserve:
        raise RuntimeError('The isolated fixture could not be fully cleaned up')


if __name__ == '__main__':
    main()
