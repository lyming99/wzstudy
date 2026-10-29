"""One-time, explicitly authorized management setup for the source-only push Skill.
Never prints or persists the user login token. Never rotates an existing source.
"""
from pathlib import Path
import argparse
import json
import os
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'skills/wzstudy-push/scripts'))
from wzstudy_push import Client, PushError, credentials, encode, endpoint, read_json, source_uuid


def reserve_private_file(path):
    """Restrict an empty file BEFORE writing any source secret into it."""
    path = Path(path).resolve()
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        if os.name == 'nt':
            escaped = str(path).replace("'", "''")
            command = ("$ErrorActionPreference='Stop';$p='" + escaped + "';"
                       "$i=[Security.Principal.WindowsIdentity]::GetCurrent();$a=Get-Acl -LiteralPath $p;"
                       "$a.SetAccessRuleProtection($true,$false);$a.SetOwner($i.User);"
                       "$r=New-Object Security.AccessControl.FileSystemAccessRule($i.User,'FullControl','Allow');"
                       "$a.SetAccessRule($r);Set-Acl -LiteralPath $p -AclObject $a")
            shell = str(Path(os.environ['WINDIR']) / 'System32/WindowsPowerShell/v1.0/powershell.exe')
            result = subprocess.run([shell, '-NoProfile', '-NonInteractive', '-Command', command],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
            if result.returncode:
                raise PushError('config_permissions', 'Cannot restrict the client config ACL; no source was created.')
        else:
            os.fchmod(fd, 0o600)
        return fd, path
    except BaseException:
        os.close(fd)
        path.unlink(missing_ok=True)
        raise


def setup(base_url, config, user_token_file=None, user_token_env=None, slug='ai-collector', name='AI 资料采集'):
    base_url = endpoint(base_url)
    path = Path(config)
    if path.exists():
        url, source_id, key = credentials(str(path), base_url)
        result = Client(url, source_id, key).doctor()
        return {'configured': True, 'existing_config_reused': True, 'source_id': source_id,
                'base_url': url, 'config_file': str(path.resolve()), 'source_authenticated': result['source_authenticated']}
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,63}', slug) or not name.strip() or len(name.encode('utf-8')) > 200:
        raise PushError('invalid_source', 'Invalid source slug/name.')
    fd, path = reserve_private_file(path)
    created = False
    admin = None
    try:
        if user_token_file:
            private = read_json(user_token_file)
            key = private.get('access_token') if isinstance(private, dict) else None
            private = None
        else:
            key = os.environ.get(user_token_env or '')
        if not isinstance(key, str) or not key.startswith('wzus_') or not 32 <= len(key) <= 256 or not key.isascii() or any(c.isspace() for c in key):
            raise PushError('missing_user_session', 'Provide an explicit user access_token from /user/auth/login (session_mode=token); administrator credentials cannot create sources.')
        admin = Client(base_url, key=key)
        key = None
        sources = admin.request('/workspace/sources')
        if any(source.get('slug') == slug for source in sources):
            raise PushError('source_exists', 'Source slug already exists. It was not changed or rotated; recover its existing config or ask the source owner to rotate it explicitly.')
        result = admin.request('/workspace/sources', 'POST', encode({
            'slug': slug, 'name': name.strip(), 'description': '当前用户的 AI 资料入库来源；只进入本人工作区。',
            'kind': 'manual', 'integration': 'skill', 'feed_url': '', 'category': 'AI 采集', 'language': 'zh-CN',
            'enabled': True, 'auto_publish': False, 'poll_interval_seconds': 900,
            'permissions': ['ingest', 'upload']}))
        created = True
        source = result['source']
        source_key = result['access_key']
        if not isinstance(source_key, str) or not source_key.startswith('wzsk_'):
            raise PushError('invalid_response', 'Source created but credential response was invalid; do not blindly retry.')
        config_value = {'base_url': base_url, 'source_id': source_uuid(source['id']), 'source_key': source_key}
        with os.fdopen(fd, 'wb') as stream:
            fd = None
            stream.write(encode(config_value) + b'\n')
            stream.flush()
            os.fsync(stream.fileno())
        doctor = Client(base_url, config_value['source_id'], source_key).doctor()
        return {'configured': True, 'existing_config_reused': False, 'source_id': source['id'],
                'base_url': base_url, 'config_file': str(path), 'auto_publish': source['auto_publish'],
                'source_authenticated': doctor['source_authenticated'], 'source_key_printed': False}
    finally:
        if admin is not None:
            admin.key = ''
        if fd is not None:
            os.close(fd)
        if not created:
            path.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://127.0.0.1:8091')
    parser.add_argument('--config', required=True)
    auth = parser.add_mutually_exclusive_group(required=True)
    auth.add_argument('--user-token-file', help='Explicit private JSON containing the user access_token; used once, not copied.')
    auth.add_argument('--user-token-env', help='Name of an environment variable holding a user login access token.')
    parser.add_argument('--slug', default='ai-collector')
    parser.add_argument('--name', default='AI 资料采集')
    args = parser.parse_args()
    try:
        print(json.dumps(setup(args.base_url, args.config, args.user_token_file, args.user_token_env, args.slug, args.name), ensure_ascii=False, indent=2))
        return 0
    except (PushError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        code = error.code if isinstance(error, PushError) else 'setup_failed'
        message = error.message if isinstance(error, PushError) else 'Setup could not complete. Check private config and source state before retrying; no existing source was intentionally changed.'
        print(json.dumps({'ok': False, 'error': {'code': code, 'message': message}}, ensure_ascii=False), file=sys.stderr)
        return 2


if __name__ == '__main__':
    if hasattr(sys.stdout, 'reconfigure'):
        sys.stdout.reconfigure(encoding='utf-8')
        sys.stderr.reconfigure(encoding='utf-8')
    raise SystemExit(main())
