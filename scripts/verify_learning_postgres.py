"""Test migrations/channels against a disposable LOCAL PostgreSQL container.
Uses an already-installed postgres:17-alpine image. Never pulls an image, reads
.env, uses the active Docker context, or touches existing containers/volumes.
"""
from pathlib import Path
import json
import os
import secrets
import socket
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / '.artifacts' / 'learning-postgres'
IMAGE = 'postgres:17-alpine'
HOST = 'npipe:////./pipe/dockerDesktopLinuxEngine' if os.name == 'nt' else 'unix:///var/run/docker.sock'
DOCKER = ['docker', '--host', HOST]


def docker(*args, env=None, timeout=20):
    return subprocess.run(DOCKER + list(args), env=env, capture_output=True, text=True,
                          encoding='utf-8', errors='replace', timeout=timeout)


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    result_file = OUT / 'verification.json'
    result_file.unlink(missing_ok=True)
    if docker('image', 'inspect', '--format', '{{.Id}}', IMAGE).returncode:
        raise RuntimeError('required image unavailable in local Docker engine; no image was pulled')
    name = 'wzstudy-learning-test-' + uuid.uuid4().hex[:16]
    env = dict(os.environ)
    password = secrets.token_urlsafe(32)
    env['POSTGRES_PASSWORD'] = password
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    created = False
    try:
        result = docker('run', '--detach', '--rm', '--pull=never', '--name', name,
                        '--label', 'wzstudy.test=learning-channels',
                        '-e', 'POSTGRES_PASSWORD', '-e', 'POSTGRES_USER=wztest', '-e', 'POSTGRES_DB=wztest',
                        '-p', f'127.0.0.1:{port}:5432', IMAGE, env=env, timeout=30)
        if result.returncode:
            raise RuntimeError('could not start isolated local PostgreSQL test container')
        created = True
        print('Started disposable local PostgreSQL; existing containers/volumes unchanged.', flush=True)
        for _ in range(100):
            if docker('exec', name, 'pg_isready', '-q', '-h', '127.0.0.1', '-U', 'wztest', '-d', 'wztest').returncode == 0:
                break
            time.sleep(.2)
        else:
            raise RuntimeError('isolated PostgreSQL readiness timeout')
        env['TEST_DATABASE_URL'] = f'postgres://wztest:{password}@127.0.0.1:{port}/wztest?sslmode=disable'
        outputs = []
        commands = [
            ['go', 'test', './internal/study', '-run', 'TestLearningChannelStoreContract|TestPostgresLearningChannelUniqueAndReadiness', '-v', '-count=1', '-timeout=120s'],
            ['go', 'test', './...', '-count=1', '-timeout=240s'],
        ]
        for command in commands:
            result = subprocess.run(command, cwd=ROOT, env=env, capture_output=True, text=True,
                                    encoding='utf-8', errors='replace', timeout=300)
            text = result.stdout + result.stderr
            outputs.append(text)
            (OUT / 'go-test.log').write_text('\n'.join(outputs), encoding='utf-8')
            print(text, flush=True)
            if result.returncode:
                raise RuntimeError('isolated PostgreSQL regression failed; see project test log')
        result_file.write_text(json.dumps({'ok': True, 'postgres_live_test': True,
            'migration': '000014_learning_channels.sql', 'full_go_suite': True,
            'production_touched': False, 'local_engine_only': True}, indent=2), encoding='utf-8')
        print('PASS: PostgreSQL channel migration, uniqueness, readiness, tenant isolation and full Go suite.', flush=True)
    finally:
        # The random name is created only by this invocation; never enumerate or
        # remove unrelated containers. --rm also removes its anonymous volume.
        if created:
            removed = docker('rm', '--force', '--volumes', name, timeout=30)
            if removed.returncode:
                raise RuntimeError('could not remove this invocation\'s isolated test container')
            print('Removed this invocation\'s PostgreSQL container and temporary volume.', flush=True)


if __name__ == '__main__':
    main()
