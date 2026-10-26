"""Read-only file-workspace preflight. Outputs counts and opaque integrity digests.
Never prints material content, key values, key hashes or administrator records.
This tool does not migrate, initialize, create credentials or rewrite the workspace.
"""
import argparse
import hashlib
import json
from pathlib import Path
import sys


def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def inspect(data_directory, credential_file=None):
    directory = Path(data_directory).resolve()
    file = directory / 'workspace.json'
    raw = file.read_bytes()
    state = json.loads(raw)
    if state.get('version') != 1 or not isinstance(state.get('sources'), dict) or not isinstance(state.get('items'), dict) or not isinstance(state.get('records'), dict):
        raise ValueError('unsupported workspace')
    sources, items, records = state['sources'], state['items'], state['records']
    pending, identities, materials = [], [], []
    client_count = asset_count = 0
    for identifier, record in sorted(sources.items()):
        source = record['source']
        secret = record.get('internal', {})
        permissions = source.get('permissions')
        identities.append(('source', identifier, secret.get('key_hash'), permissions, source.get('enabled')))
        if source.get('system') or source.get('kind') != 'manual':
            continue
        query = [p for p in ['read', 'sync'] if p in (permissions or [])]
        writing = [p for p in ['ingest', 'upload'] if p in (permissions or [])]
        if query:
            pending.append({'source_id': identifier, 'name': source.get('name'), 'revision': source.get('revision'),
                            'enabled': source.get('enabled'), 'ingest_permissions': writing, 'query_permissions': query,
                            'requires_key_choice': bool(writing)})
    for name, record in sorted(records.items()):
        if name.startswith('client/'):
            client_count += 1
            client = record['client']
            identities.append(('client', client['id'], record.get('internal', {}).get('key_hash'), client.get('permissions'), client.get('enabled')))
        if name.startswith('asset/'):
            asset_count += 1
    for identifier, item in sorted(items.items()):
        published = item.get('published') or {}
        materials.append((identifier, item.get('source_id'), item.get('external_id'), item.get('revision'),
                          item.get('content_hash'), item.get('status'), item.get('deleted_at'),
                          digest(item.get('draft')), digest(published)))
    result = {'data_directory': str(directory), 'file_version': state['version'],
              'workspace_sha256': hashlib.sha256(raw).hexdigest(),
              'identity_digest': digest(identities), 'material_digest': digest(materials),
              'cursor': state.get('cursor'), 'changes_digest': digest(state.get('changes', [])),
              'material_records': len(items), 'source_records': len(sources), 'query_clients': client_count,
              'asset_records': asset_count, 'record_count': len(records),
              'pending_query_identities': len(pending), 'mixed_identities': sum(p['requires_key_choice'] for p in pending),
              'migration_preview': pending}
    if credential_file:
        result['credential_file_sha256'] = hashlib.sha256(Path(credential_file).read_bytes()).hexdigest()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data', required=True, help='Explicit file-mode DATA_DIR, not a discovered path.')
    parser.add_argument('--credential-file', help='Optional explicit launcher credential file; hashes bytes only, never parses or prints values.')
    args = parser.parse_args()
    try:
        result = inspect(args.data, args.credential_file)
    except (OSError, ValueError, KeyError, TypeError):
        print('Workspace preflight failed. Verify the explicit paths and file format locally; nothing was changed.', file=sys.stderr)
        return 2
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == '__main__':
    sys.stdout.reconfigure(encoding='utf-8')
    raise SystemExit(main())
