"""Thin stdio MCP adapter for WzStudy ingestion, using the shared Skill client.

Supports per-request MCP 2026-07-28 and initialized legacy 2025 revisions.
No client queries, approval, arbitrary HTTP, downloads or credential tools.
"""
import argparse
import json
import re
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'skills' / 'wzstudy-push' / 'scripts'))
from wzstudy_push import Client, PushError, batches, credentials, encode, normalize_items, receipt, upload

MODERN = '2026-07-28'
LEGACY = ('2025-11-25', '2025-06-18', '2025-03-26')
SUPPORTED = [MODERN, *LEGACY]
SERVER_INFO = {'name': 'wzstudy-ingest', 'version': '1.0.0'}
META_VERSION = 'io.modelcontextprotocol/protocolVersion'
META_INFO = 'io.modelcontextprotocol/clientInfo'
META_CAPS = 'io.modelcontextprotocol/clientCapabilities'
MAX_LINE = 5 * 1024 * 1024


def schema(properties=None, required=()):
    return {'type': 'object', 'properties': properties or {}, 'required': list(required), 'additionalProperties': False}


def tool(name, description, inputs, readonly=False, idempotent=True):
    return {'name': name, 'description': description, 'inputSchema': inputs,
            'annotations': {'readOnlyHint': readonly, 'destructiveHint': False, 'idempotentHint': idempotent, 'openWorldHint': False}}


class Adapter:
    def __init__(self, client, allowed_roots=()):
        self.client = client
        self.allowed_roots = tuple(Path(p).resolve(strict=True) for p in allowed_roots)
        if any(not root.is_dir() for root in self.allowed_roots):
            raise ValueError('Every --allow-root must be an existing directory.')
        self.legacy_version = None
        self.initialized = False

    def tools(self):
        definitions = [
            tool('wzstudy_ingest_status', 'Read-only health and own-source ingest permission check. No materials are created.', schema(), True),
            tool('wzstudy_ingest_items', 'Submit 1-100 authorized materials through the unified ingestion API. Stable external_id updates the same record. Does not approve, publish or push to learning clients.',
                 schema({'items': {'type': 'array', 'minItems': 1, 'maxItems': 100, 'items': {'type': 'object'}}, 'dry_run': {'type': 'boolean', 'default': False}}, ['items'])),
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
            else:
                if not isinstance(arguments['metadata'], dict):
                    raise RPCError(-32602, 'metadata must be an object.')
                paths = self.files(arguments['files'])
                value = upload(self.client, arguments['metadata'], paths, arguments.get('dry_run', False))
            value = {'ok': True, **value}
            return {'content': [{'type': 'text', 'text': json.dumps(value, ensure_ascii=False)}], 'structuredContent': value, 'isError': False}
        except PushError as error:
            value = {'ok': False, 'code': error.code, 'message': error.message, 'status': error.status, 'commit_uncertain': error.uncertain}
            return {'content': [{'type': 'text', 'text': json.dumps(value, ensure_ascii=False)}], 'structuredContent': value, 'isError': True}

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
                # Notifications never execute ingestion tools and never receive a response.
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
                return {'jsonrpc': '2.0', 'id': identifier, 'result': {'protocolVersion': self.legacy_version, 'capabilities': {'tools': {}}, 'serverInfo': SERVER_INFO, 'instructions': 'Ingestion only; use a dedicated source Key. Learning clients query the HTTP API separately.'}}
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
                result = {'supportedVersions': SUPPORTED, 'capabilities': {'tools': {}}, 'instructions': 'Unified ingestion only. No approval, query-client management or data push tools.'}
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
    parser.add_argument('--allow-root', action='append', default=[], help='Explicit directory permitted for file upload; repeatable. No roots means no file tool.')
    parser.add_argument('--timeout', type=float, default=60)
    args = parser.parse_args(argv)
    if not 1 <= args.timeout <= 300:
        parser.error('timeout must be 1-300 seconds')
    try:
        base, source_id, key = credentials(args.config)
        adapter = Adapter(Client(base, source_id, key, args.timeout), args.allow_root)
    except (PushError, ValueError, OSError):
        print('MCP adapter configuration rejected. Check the dedicated source config and allowed directories locally.', file=sys.stderr)
        return 2
    if hasattr(sys.stdout, 'reconfigure'):
        sys.stdout.reconfigure(encoding='utf-8', newline='\n')
    serve(adapter, sys.stdin.buffer, sys.stdout)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
