"""Thin stdio MCP adapter for WzStudy, using the shared Skill client.

Supports per-request MCP 2026-07-28 and initialized legacy 2025 revisions.
Default tools only ingest materials for a dedicated source. Optional catalog
collection and learning roadmap management appears only when a separate user
session token is configured; there is still no approval, query-client,
arbitrary HTTP, download or credential tool.
"""
import argparse
import json
import os
import re
from pathlib import Path
import sys
from urllib.parse import urlencode
from uuid import UUID, uuid4

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'skills' / 'wzstudy-push' / 'scripts'))
from wzstudy_push import (Client, PushError, batches, credentials, encode, endpoint, normalize_items,
                          read_json, receipt, upload)

MODERN = '2026-07-28'
LEGACY = ('2025-11-25', '2025-06-18', '2025-03-26')
SUPPORTED = [MODERN, *LEGACY]
SERVER_INFO = {'name': 'wzstudy-ingest', 'version': '1.1.0'}
META_VERSION = 'io.modelcontextprotocol/protocolVersion'
META_INFO = 'io.modelcontextprotocol/clientInfo'
META_CAPS = 'io.modelcontextprotocol/clientCapabilities'
MAX_LINE = 5 * 1024 * 1024

CATALOG_TOOLS = ('wzstudy_catalog_list', 'wzstudy_catalog_get', 'wzstudy_catalog_save', 'wzstudy_catalog_delete')
ROADMAP_TOOLS = ('wzstudy_roadmap_list', 'wzstudy_roadmap_get', 'wzstudy_roadmap_save', 'wzstudy_roadmap_delete')
MANAGEMENT_TOOLS = CATALOG_TOOLS + ROADMAP_TOOLS


def schema(properties=None, required=()):
    return {'type': 'object', 'properties': properties or {}, 'required': list(required), 'additionalProperties': False}


def tool(name, description, inputs, readonly=False, idempotent=True, destructive=False):
    return {'name': name, 'description': description, 'inputSchema': inputs,
            'annotations': {'readOnlyHint': readonly, 'destructiveHint': destructive, 'idempotentHint': idempotent, 'openWorldHint': False}}


def uuid_text(value, label):
    if not isinstance(value, str) or not value:
        raise PushError('invalid_input', f'{label} must be a UUID string.')
    try:
        parsed = UUID(value)
    except (ValueError, TypeError, AttributeError):
        raise PushError('invalid_input', f'{label} must be a UUID string.') from None
    if parsed.int == 0:
        raise PushError('invalid_input', f'{label} must not be the nil UUID.')
    return str(parsed)


def revision_arg(arguments):
    value = arguments.get('revision', 0)
    if type(value) is not int or value < 0:
        raise PushError('invalid_input', 'revision must be a non-negative integer.')
    return value


def text_value(value, label, limit, required=False):
    if value is None:
        value = ''
    if not isinstance(value, str):
        raise PushError('invalid_input', f'{label} must be text.')
    value = value.strip()
    if required and not value:
        raise PushError('invalid_input', f'{label} must not be empty.')
    if len(value) > limit:
        raise PushError('invalid_input', f'{label} must be at most {limit} characters.')
    return value


def page_args(arguments):
    limit, offset = arguments.get('limit', 20), arguments.get('offset', 0)
    if type(limit) is not int or not 1 <= limit <= 50:
        raise PushError('invalid_input', 'limit must be an integer between 1 and 50.')
    if type(offset) is not int or not 0 <= offset <= 10000:
        raise PushError('invalid_input', 'offset must be an integer between 0 and 10000.')
    return limit, offset


def catalog_body(arguments):
    """Build the same request body the admin console sends; the server stays authoritative."""
    body = {'revision': revision_arg(arguments),
            'title': text_value(arguments.get('title'), 'title', 500, True),
            'summary': text_value(arguments.get('summary', ''), 'summary', 8000)}
    chapters = arguments.get('chapters', [])
    if not isinstance(chapters, list) or len(chapters) > 100:
        raise PushError('invalid_input', 'chapters must be an array of at most 100 chapters.')
    prepared = []
    for chapter in chapters:
        if not isinstance(chapter, dict) or set(chapter) - {'id', 'title', 'item_id'}:
            raise PushError('invalid_input', 'Each chapter accepts only id, title and item_id.')
        value = {'title': text_value(chapter.get('title'), 'chapter title', 200, True),
                 'item_id': uuid_text(chapter.get('item_id'), 'chapter item_id')}
        if chapter.get('id'):
            value['id'] = uuid_text(chapter.get('id'), 'chapter id')
        prepared.append(value)
    body['chapters'] = prepared
    return body


def roadmap_body(arguments):
    """Normalize a node tree: ids are generated when absent and parent_index is resolved."""
    title = text_value(arguments.get('title'), 'title', 200, True)
    body = {'revision': revision_arg(arguments), 'title': title,
            'summary': text_value(arguments.get('summary', ''), 'summary', 2000)}
    nodes = arguments.get('nodes')
    allowed = {'id', 'parent_id', 'parent_index', 'title', 'note', 'item_ids', 'collapsed'}
    if not isinstance(nodes, list) or not 1 <= len(nodes) <= 300:
        raise PushError('invalid_input', 'nodes must be an array of 1-300 nodes.')
    prepared = []
    for node in nodes:
        if not isinstance(node, dict) or set(node) - allowed:
            raise PushError('invalid_input', 'Each node accepts only id, parent_id, parent_index, title, note, item_ids and collapsed.')
        item_ids = node.get('item_ids', [])
        if not isinstance(item_ids, list) or len(item_ids) > 20:
            raise PushError('invalid_input', 'item_ids must be an array of at most 20 material UUIDs.')
        values = [uuid_text(value, 'item_ids entry') for value in item_ids]
        if len(set(values)) != len(values):
            raise PushError('invalid_input', 'item_ids must not repeat a material.')
        if 'collapsed' in node and type(node['collapsed']) is not bool:
            raise PushError('invalid_input', 'collapsed must be a boolean.')
        prepared.append({'id': uuid_text(node['id'], 'node id') if node.get('id') else str(uuid4()),
                         'title': text_value(node.get('title'), 'node title', 200, True),
                         'note': text_value(node.get('note', ''), 'node note', 4000),
                         'item_ids': values, 'collapsed': node.get('collapsed', False), 'parent_id': ''})
    if len({node['id'] for node in prepared}) != len(prepared):
        raise PushError('invalid_input', 'Node ids must be unique.')
    roots = []
    for index, node in enumerate(nodes):
        parent_id, parent_index = node.get('parent_id', ''), node.get('parent_index')
        if parent_index is not None and parent_id:
            raise PushError('invalid_input', 'Use parent_id or parent_index for a node, not both.')
        if parent_index is not None:
            if type(parent_index) is not int or not 0 <= parent_index < len(prepared) or parent_index == index:
                raise PushError('invalid_input', 'parent_index must reference another node in the same array.')
            parent_id = prepared[parent_index]['id']
        if parent_id:
            prepared[index]['parent_id'] = uuid_text(parent_id, 'parent_id')
        else:
            roots.append(index)
    if len(roots) != 1:
        raise PushError('invalid_input', 'Exactly one root node without parent_id/parent_index is required.')
    if prepared[roots[0]]['title'] != title:
        raise PushError('invalid_input', 'The root node title must equal the roadmap title.')
    body['nodes'] = prepared
    return body


def workspace_credentials(config=None, timeout=60, fallback_base=''):
    """Optional user-session credentials; never inferred from the source config alone.

    The token must be configured explicitly, either in the dedicated workspace
    config or as WZSTUDY_USER_TOKEN. The endpoint may fall back to the already
    validated source endpoint so both credentials keep talking to the same
    server; an explicitly configured different origin is refused instead of
    silently forwarding the token elsewhere.
    """
    token, base, filename = os.environ.get('WZSTUDY_USER_TOKEN'), os.environ.get('WZSTUDY_BASE_URL'), config
    if filename:
        cfg = read_json(filename)
        if not isinstance(cfg, dict) or set(cfg) - {'base_url', 'user_token'}:
            raise PushError('invalid_config', 'Workspace configuration accepts only base_url and user_token.')
        token, base = cfg.get('user_token'), cfg.get('base_url')
    elif not token:
        return None
    if not isinstance(token, str) or not token.startswith('wzus_') or not token.isascii() or len(token) > 256 or any(c.isspace() or ord(c) < 33 for c in token):
        raise PushError('invalid_credentials', 'A wzus_ user session token is required for catalog and roadmap tools.')
    if base is None or base == '':
        base = fallback_base
    if not isinstance(base, str) or not base:
        raise PushError('invalid_config', 'Configure base_url for the user session or the source, but not both origins.')
    base = endpoint(base)
    if fallback_base and base != endpoint(fallback_base):
        raise PushError('endpoint_mismatch', 'The user session is bound to a different origin than the ingest source; create a separate process instead of forwarding the token.')
    return Client(base, '', token, timeout)


class Adapter:
    def __init__(self, client, allowed_roots=(), workspace=None):
        self.client = client
        self.allowed_roots = tuple(Path(p).resolve(strict=True) for p in allowed_roots)
        if any(not root.is_dir() for root in self.allowed_roots):
            raise ValueError('Every --allow-root must be an existing directory.')
        self.workspace = workspace
        self.legacy_version = None
        self.initialized = False

    def tools(self):
        definitions = [
            tool('wzstudy_ingest_status', 'Read-only health and own-source ingest permission check. No materials are created.', schema(), True),
            tool('wzstudy_ingest_items', 'Submit 1-100 authorized materials through the unified ingestion API. Stable external_id updates the same record. Does not approve, publish or push to learning clients.',
                 schema({'items': {'type': 'array', 'minItems': 1, 'maxItems': 100, 'items': {'type': 'object'}}, 'dry_run': {'type': 'boolean', 'default': False}}, ['items'])),
        ]
        if self.workspace:
            definitions += [
                tool('wzstudy_catalog_list', 'Read-only list of the signed-in user workspace catalog collections. Requires the configured user session token.',
                     schema({'limit': {'type': 'integer', 'minimum': 1, 'maximum': 50, 'default': 20},
                             'offset': {'type': 'integer', 'minimum': 0, 'maximum': 10000, 'default': 0}}), True),
                tool('wzstudy_catalog_get', 'Read-only detail of one catalog collection, including its chapters. Requires the configured user session token.',
                     schema({'id': {'type': 'string'}}, ['id']), True),
                tool('wzstudy_catalog_save', 'Create or replace one catalog collection. Saving takes effect immediately; read the latest revision first because a stale revision returns 409. Never deletes or publishes materials.',
                     schema({'id': {'type': 'string'}, 'revision': {'type': 'integer', 'minimum': 0},
                             'title': {'type': 'string'}, 'summary': {'type': 'string'},
                             'chapters': {'type': 'array', 'maxItems': 100, 'items': {'type': 'object'}},
                             'dry_run': {'type': 'boolean', 'default': False}}, ['title'])),
                tool('wzstudy_catalog_delete', 'Delete one catalog collection and its chapter links only; materials and learning records are not deleted. Uses the current revision.',
                     schema({'id': {'type': 'string'}, 'revision': {'type': 'integer', 'minimum': 0},
                             'dry_run': {'type': 'boolean', 'default': False}}, ['id', 'revision']), destructive=True),
                tool('wzstudy_roadmap_list', 'Read-only list of the signed-in user workspace learning roadmaps. Requires the configured user session token.',
                     schema({'q': {'type': 'string'}, 'limit': {'type': 'integer', 'minimum': 1, 'maximum': 50, 'default': 20},
                             'offset': {'type': 'integer', 'minimum': 0, 'maximum': 10000, 'default': 0}}), True),
                tool('wzstudy_roadmap_get', 'Read-only detail of one learning roadmap, including every node. Requires the configured user session token.',
                     schema({'id': {'type': 'string'}}, ['id']), True),
                tool('wzstudy_roadmap_save', 'Create or replace one learning roadmap and its node tree. The root title must equal the roadmap title, with at most 300 nodes and 12 levels. A missing node id is generated and parent_index may point at another node. Does not change materials or personal learning progress.',
                     schema({'id': {'type': 'string'}, 'revision': {'type': 'integer', 'minimum': 0},
                             'title': {'type': 'string'}, 'summary': {'type': 'string'},
                             'nodes': {'type': 'array', 'minItems': 1, 'maxItems': 300, 'items': {'type': 'object'}},
                             'dry_run': {'type': 'boolean', 'default': False}}, ['title', 'nodes'])),
                tool('wzstudy_roadmap_delete', 'Delete one learning roadmap only; materials and learning progress are not deleted. Uses the current revision.',
                     schema({'id': {'type': 'string'}, 'revision': {'type': 'integer', 'minimum': 0},
                             'dry_run': {'type': 'boolean', 'default': False}}, ['id', 'revision']), destructive=True),
            ]
        if self.allowed_roots:
            definitions.append(tool('wzstudy_upload_files', 'Upload 1-20 actual media/text files from explicitly allowed directories. No arbitrary filesystem reads; no URL download. Does not approve or publish.',
                                    schema({'metadata': {'type': 'object'}, 'files': {'type': 'array', 'minItems': 1, 'maxItems': 20, 'items': {'type': 'string'}}, 'dry_run': {'type': 'boolean', 'default': False}}, ['metadata', 'files']), idempotent=False))
        return definitions

    def files(self, values):
        if not self.allowed_roots or not isinstance(values, list) or not 1 <= len(values) <= 20:
            raise PushError('file_access_denied', 'File uploads require explicitly configured allowed directories.')
        resolved = []
        for value in values:
            if not isinstance(value, str) or not value:
                raise PushError('invalid_upload', 'File paths must be non-empty strings.')
            try:
                path = Path(value).resolve(strict=True)
            except (OSError, ValueError):
                raise PushError('file_access_denied', 'Requested file is unavailable or outside the allowed directories.') from None
            if not path.is_file() or not any(path.is_relative_to(root) for root in self.allowed_roots):
                raise PushError('file_access_denied', 'Requested file is unavailable or outside the allowed directories.')
            resolved.append(path)
        return resolved

    def call(self, name, arguments):
        definitions = {v['name']: v for v in self.tools()}
        if name not in definitions:
            raise RPCError(-32602, 'Unknown or unavailable tool.')
        inputs = definitions[name]['inputSchema']
        if not isinstance(arguments, dict) or set(arguments) - set(inputs['properties']) or any(k not in arguments for k in inputs['required']):
            raise RPCError(-32602, 'Invalid tool arguments; credentials and endpoints are not tool parameters.')
        if 'dry_run' in arguments and type(arguments['dry_run']) is not bool:
            raise RPCError(-32602, 'dry_run must be a boolean.')
        try:
            if name == 'wzstudy_ingest_status':
                value = self.client.doctor()
            elif name == 'wzstudy_ingest_items':
                if not isinstance(arguments['items'], list) or not 1 <= len(arguments['items']) <= 100:
                    raise PushError('invalid_input', 'Each tool call accepts 1-100 materials.')
                items = normalize_items(arguments['items'])
                grouped = batches(items)
                if len(grouped) != 1:
                    raise PushError('payload_too_large', 'One MCP call must fit one atomic API batch (4 MiB maximum).')
                value = {'dry_run': True, 'items': len(items)} if arguments.get('dry_run') else receipt(self.client.request('/ingest/' + self.client.source_id, 'POST', encode({'items': items})))
            elif name == 'wzstudy_upload_files':
                if not isinstance(arguments['metadata'], dict):
                    raise RPCError(-32602, 'metadata must be an object.')
                paths = self.files(arguments['files'])
                value = upload(self.client, arguments['metadata'], paths, arguments.get('dry_run', False))
            else:
                value = self.manage(name, arguments)
            value = {'ok': True, **value}
            return {'content': [{'type': 'text', 'text': json.dumps(value, ensure_ascii=False)}], 'structuredContent': value, 'isError': False}
        except PushError as error:
            value = {'ok': False, 'code': error.code, 'message': error.message, 'status': error.status, 'commit_uncertain': error.uncertain}
            return {'content': [{'type': 'text', 'text': json.dumps(value, ensure_ascii=False)}], 'structuredContent': value, 'isError': True}

    def workspace_request(self, path, method='GET', body=None):
        """Keep the shared client's safety rules but report user-session failures."""
        try:
            return self.workspace.request(path, method, body)
        except PushError as error:
            if error.status == 401:
                raise PushError('user_session_required', 'The configured user session token was rejected or has expired; a source or query client Key cannot replace it.', 401) from None
            if error.status == 403:
                raise PushError('workspace_denied', 'This user session cannot manage catalog collections or roadmaps in its workspace.', 403) from None
            raise

    def manage(self, name, arguments):
        if self.workspace is None:
            raise RPCError(-32602, 'Unknown or unavailable tool.')
        dry = bool(arguments.get('dry_run'))
        if name == 'wzstudy_catalog_list':
            limit, offset = page_args(arguments)
            return self.workspace_request('/workspace/catalog/collections?' + urlencode({'limit': limit, 'offset': offset}))
        if name == 'wzstudy_roadmap_list':
            limit, offset = page_args(arguments)
            query = {'limit': limit, 'offset': offset}
            if arguments.get('q'):
                query['q'] = text_value(arguments.get('q'), 'q', 200)
            return self.workspace_request('/workspace/roadmaps?' + urlencode(query))
        if name == 'wzstudy_catalog_get':
            return self.workspace_request('/workspace/catalog/collections/' + uuid_text(arguments.get('id'), 'id'))
        if name == 'wzstudy_roadmap_get':
            return self.workspace_request('/workspace/roadmaps/' + uuid_text(arguments.get('id'), 'id'))
        if name == 'wzstudy_catalog_save':
            return self.save('/workspace/catalog/collections', arguments, catalog_body(arguments), dry)
        if name == 'wzstudy_roadmap_save':
            return self.save('/workspace/roadmaps', arguments, roadmap_body(arguments), dry)
        if name == 'wzstudy_catalog_delete':
            return self.remove('/workspace/catalog/collections', arguments, dry)
        if name == 'wzstudy_roadmap_delete':
            return self.remove('/workspace/roadmaps', arguments, dry)
        raise RPCError(-32602, 'Unknown or unavailable tool.')

    def save(self, base, arguments, body, dry_run):
        target = arguments.get('id', '')
        if target:
            path, method = base + '/' + uuid_text(target, 'id'), 'PUT'
            if not body['revision']:
                raise PushError('invalid_input', 'Updating an existing record requires its current revision.')
        else:
            path, method = base, 'POST'
            if body['revision']:
                raise PushError('invalid_input', 'A new record must use revision 0.')
        if dry_run:
            return {'dry_run': True, 'method': method, 'path': path, 'body': body}
        return self.workspace_request(path, method, encode(body))

    def remove(self, base, arguments, dry_run):
        path = base + '/' + uuid_text(arguments.get('id'), 'id') + '/delete'
        body = {'revision': revision_arg(arguments)}
        if dry_run:
            return {'dry_run': True, 'method': 'POST', 'path': path, 'body': body}
        return self.workspace_request(path, 'POST', encode(body))

    def dispatch(self, request):
        identifier = request.get('id') if isinstance(request, dict) else None
        try:
            if not isinstance(request, dict) or request.get('jsonrpc') != '2.0' or not isinstance(request.get('method'), str):
                raise RPCError(-32600, 'Invalid JSON-RPC request.')
            method = request['method']
            params = request.get('params', {})
            if not isinstance(params, dict):
                raise RPCError(-32602, 'params must be an object.')
            if 'id' not in request:
                # Notifications never execute tools and never receive a response.
                if method == 'notifications/initialized' and self.legacy_version:
                    self.initialized = True
                return None
            if type(identifier) not in (str, int):
                raise RPCError(-32600, 'Request ID must be a string or integer, never null.')
            if method == 'initialize':
                if self.legacy_version:
                    raise RPCError(-32600, 'This stdio process has already been initialized.')
                if not isinstance(params.get('protocolVersion'), str) or not isinstance(params.get('capabilities'), dict) or not isinstance(params.get('clientInfo'), dict):
                    raise RPCError(-32602, 'Invalid legacy initialization parameters.')
                info = params['clientInfo']
                if not isinstance(info.get('name'), str) or not isinstance(info.get('version'), str):
                    raise RPCError(-32602, 'Legacy clientInfo requires name and version.')
                self.legacy_version = params['protocolVersion'] if params['protocolVersion'] in LEGACY else LEGACY[0]
                return {'jsonrpc': '2.0', 'id': identifier, 'result': {'protocolVersion': self.legacy_version, 'capabilities': {'tools': {}}, 'serverInfo': SERVER_INFO, 'instructions': self.instructions}}
            meta = params.get('_meta', {})
            if not isinstance(meta, dict):
                raise RPCError(-32602, '_meta must be an object.')
            modern = META_VERSION in meta
            if modern:
                version = meta[META_VERSION]
                if not isinstance(version, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}', version):
                    raise RPCError(-32602, 'protocolVersion must be a protocol revision date.')
                if version != MODERN:
                    raise RPCError(-32022, 'Unsupported protocol version', {'requested': version, 'supported': SUPPORTED})
                info = meta.get(META_INFO)
                if not isinstance(info, dict) or not isinstance(info.get('name'), str) or not isinstance(info.get('version'), str) or not isinstance(meta.get(META_CAPS), dict):
                    raise RPCError(-32602, 'Modern requests require clientInfo and clientCapabilities metadata.')
            elif not self.initialized:
                raise RPCError(-32600, 'Supply modern per-request metadata, or complete legacy initialization.')
            clean = {k: v for k, v in params.items() if k != '_meta'}
            if method == 'ping':
                result = {}
            elif method == 'server/discover' and modern:
                if clean:
                    raise RPCError(-32602, 'Discovery accepts only standard metadata.')
                result = {'supportedVersions': SUPPORTED, 'capabilities': {'tools': {}}, 'instructions': 'Unified ingestion' + (', catalog collection and learning roadmap management for the configured user session' if self.workspace else '') + '. No approval, query-client or data push tools.'}
            elif method == 'tools/list':
                if set(clean) - {'cursor'} or clean.get('cursor'):
                    raise RPCError(-32602, 'Unknown tool-list cursor or arguments.')
                result = {'tools': self.tools()}
            elif method == 'tools/call':
                if set(clean) - {'name', 'arguments'} or not isinstance(clean.get('name'), str):
                    raise RPCError(-32602, 'Invalid tool call.')
                result = self.call(clean['name'], clean.get('arguments', {}))
            else:
                raise RPCError(-32601, 'Method not found.')
            if not modern and self.legacy_version == '2025-03-26':
                result.pop('structuredContent', None)
            if modern:
                result = {'resultType': 'complete', **result, '_meta': {'io.modelcontextprotocol/serverInfo': SERVER_INFO}}
            return {'jsonrpc': '2.0', 'id': identifier, 'result': result}
        except RPCError as error:
            return rpc_error(identifier, error.code, error.message, error.data)
        except Exception:
            # Do not echo exceptions, tool arguments, private paths or credentials.
            return rpc_error(identifier, -32603, 'Internal adapter error; no automatic retry was performed.')

    @property
    def instructions(self):
        text = 'Ingestion only; use a dedicated source Key.'
        if self.workspace:
            text += ' Catalog collection and learning roadmap management use the separately configured user session token.'
        return text + ' Learning clients query the HTTP API separately.'


class RPCError(Exception):
    def __init__(self, code, message, data=None):
        self.code, self.message, self.data = code, message, data


def rpc_error(identifier, code, message, data=None):
    result = {'jsonrpc': '2.0', 'error': {'code': code, 'message': message}}
    if type(identifier) in (str, int):
        result['id'] = identifier
    if data is not None:
        result['error']['data'] = data
    return result


def serve(adapter, incoming, outgoing):
    while True:
        line = incoming.readline(MAX_LINE + 1)
        if not line:
            return
        if len(line) > MAX_LINE:
            while line and not line.endswith(b'\n'):
                line = incoming.readline(MAX_LINE + 1)
            response = rpc_error(None, -32600, 'Message exceeds the 5 MiB transport limit.')
        else:
            try:
                request = json.loads(line.decode('utf-8'), parse_constant=lambda value: (_ for _ in ()).throw(ValueError(value)))
                response = adapter.dispatch(request)
            except (UnicodeError, ValueError):
                response = rpc_error(None, -32700, 'Invalid UTF-8 JSON message.')
        if response is not None:
            outgoing.write(json.dumps(response, ensure_ascii=False, separators=(',', ':')) + '\n')
            outgoing.flush()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', help='Private dedicated-source configuration; never an admin credential file.')
    parser.add_argument('--workspace-config', help='Optional private user session configuration enabling catalog and roadmap tools; keep the source Key separate.')
    parser.add_argument('--allow-root', action='append', default=[], help='Explicit directory permitted for file upload; repeatable. No roots means no file tool.')
    parser.add_argument('--timeout', type=float, default=60)
    args = parser.parse_args(argv)
    if not 1 <= args.timeout <= 300:
        parser.error('timeout must be 1-300 seconds')
    try:
        base, source_id, key = credentials(args.config)
        workspace = workspace_credentials(args.workspace_config, args.timeout, base)
        adapter = Adapter(Client(base, source_id, key, args.timeout), args.allow_root, workspace)
    except (PushError, ValueError, OSError):
        print('MCP adapter configuration rejected. Check the dedicated source config and the optional user session config locally.', file=sys.stderr)
        return 2
    if hasattr(sys.stdout, 'reconfigure'):
        sys.stdout.reconfigure(encoding='utf-8', newline='\n')
    serve(adapter, sys.stdin.buffer, sys.stdout)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
