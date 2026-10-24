"""Run isolated Go tests and retain machine-readable results at repository root.
PostgreSQL is opt-in: --postgres requires a dedicated TEST_DATABASE_URL.
"""
from pathlib import Path
import argparse
import json
import os
import subprocess
import sys

sys.stdout.reconfigure(encoding='utf-8')
root=Path(__file__).resolve().parents[1]
artifacts=root/'.artifacts'
artifacts.mkdir(exist_ok=True)
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--postgres',action='store_true')
parser.add_argument('--race',action='store_true')
args=parser.parse_args()
env=os.environ.copy()
if args.postgres:
    parser.error('--postgres requires dedicated TEST_DATABASE_URL') if not env.get('TEST_DATABASE_URL') else None
else:
    env.pop('TEST_DATABASE_URL',None)
cmd=['go','test','-count=1','-json']
if args.race:cmd.append('-race')
cmd.append('./...')
r=subprocess.run(cmd,cwd=root,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
raw=r.stdout.decode('utf-8',errors='replace')
stem='go-race' if args.race else 'go'
(artifacts/f'{stem}-tests.jsonl').write_text(raw,encoding='utf-8')
events=[]
for line in raw.splitlines():
    try:events.append(json.loads(line))
    except json.JSONDecodeError:pass
passed=[e for e in events if e.get('Action')=='pass' and e.get('Test')]
failed=[e for e in events if e.get('Action')=='fail']
skipped=[e for e in events if e.get('Action')=='skip' and e.get('Test')]
report={'result':'passed' if r.returncode==0 else 'failed','command':cmd,'passed_test_cases':len(passed),'passed_top_level_tests':len([e for e in passed if '/' not in e['Test']]),'skipped_tests':[e['Test'] for e in skipped],'failed_tests':[e.get('Test',e.get('Package')) for e in failed],'postgres_enabled':args.postgres,'race':args.race}
(artifacts/f'{stem}-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False,indent=2),flush=True)
if r.returncode:print(raw)
sys.exit(r.returncode)
