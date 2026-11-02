'use strict';
// Pure frontend contract: no service, network, credentials, or business data.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('internal/adminui/collection-plans.js', 'utf8');
const sandbox = {document: {addEventListener() {}}, esc: value => String(value ?? ''), icon: () => '<span class="icon"></span>'};
vm.createContext(sandbox);
vm.runInContext(source + '\nthis.view = collectionPlansView;', sandbox);
const view = sandbox.view;
const schedule = (value, zone = 'Asia/Shanghai', choice = '') => view.readSchedule(new Map([
  ['schedule_kind', 'once'], ['plan_timezone', zone], ['schedule_at', value], ['schedule_at_choice', choice]
]));

const initialZone = process.env.TZ;
try {
  for (const browserZone of ['UTC', 'Pacific/Honolulu', 'America/New_York', 'Asia/Tokyo']) {
    process.env.TZ = browserZone;
    assert.equal(schedule('2031-01-02T09:15').at, '2031-01-02T01:15:00.000Z');
    assert.equal(schedule('2031-01-02T09:15:30').at, '2031-01-02T01:15:30.000Z');
    assert.equal(schedule('2031-01-02T09:15', 'Asia/Kathmandu').at, '2031-01-02T03:30:00.000Z');
    assert.equal(schedule('2031-01-02T09:15', 'America/New_York').at, '2031-01-02T14:15:00.000Z');
    assert.equal(schedule('2031-07-02T09:15', 'America/New_York').at, '2031-07-02T13:15:00.000Z');
    assert.equal(schedule('2031-01-02T00:00', 'UTC').at, '2031-01-02T00:00:00.000Z');
    assert.equal(schedule('2032-02-29T23:59:59', 'UTC').at, '2032-02-29T23:59:59.000Z');
    assert.throws(() => schedule('2030-03-10T02:30', 'America/New_York'), /不存在/);
    assert.throws(() => schedule('2030-11-03T01:30', 'America/New_York'), /出现两次/);
    assert.throws(() => schedule('2030-11-03T01:30', 'America/New_York', '2030-11-03T07:30:00.000Z'), /UTC 偏移/);
    for (const choice of ['2030-11-03T05:30:00.000Z', '2030-11-03T06:30:00.000Z']) {
      assert.equal(schedule('2030-11-03T01:30', 'America/New_York', choice).at, choice);
    }
    // Lord Howe's transition is 30 minutes, not an assumed full hour.
    assert.throws(() => schedule('2030-04-07T01:45', 'Australia/Lord_Howe'), /出现两次/);
    assert.equal(schedule('2030-04-07T01:45', 'Australia/Lord_Howe', '2030-04-06T15:15:00.000Z').at, '2030-04-06T15:15:00.000Z');
  }
} finally {
  if (initialZone === undefined) delete process.env.TZ;
  else process.env.TZ = initialZone;
}

for (const value of ['', '2031-02-29T10:00', '2031-02-30T10:00', '2031-01-02T24:00', '2031-01-02T10:60', '2031-01-02T09:00:00+08:00', '2031-01-02T09:15:30.125']) {
  assert.throws(() => schedule(value), /请选择有效/);
}
assert.throws(() => schedule('2031-01-02T09:00', 'Mars/Olympus'), /计划时区无效/);
assert.equal(schedule('2031-01-02T00:00', '').timezone, 'UTC');
assert.equal(view.readSchedule(new Map([['schedule_kind', 'daily'], ['plan_timezone', 'Asia/Shanghai'], ['time_of_day', '09:00']])).time_of_day, '09:00');
for (const kind of ['manual', 'interval', 'webhook']) {
  assert.equal(view.readSchedule(new Map([['schedule_kind', kind], ['interval_minutes', '60']])).kind, kind);
}

const shanghai = view.editorFields({schedule: {kind: 'once', timezone: 'Asia/Shanghai', at: '2031-01-02T01:15:30Z'}});
assert.match(shanghai, /type="datetime-local"/);
assert.match(shanghai, /step="1"/);
assert.match(shanghai, /<details class="collection-plan-advanced">/);
assert.doesNotMatch(shanghai, /<details[^>]*\bopen\b/);
assert.match(shanghai, /value="2031-01-02T09:15:30"/);
assert.doesNotMatch(shanghai, /ISO 时间，必须含 UTC 偏移/);
const fold = view.editorFields({schedule: {kind: 'once', timezone: 'America/New_York', at: '2030-11-03T06:30:00Z'}});
assert.match(fold, /value="2030-11-03T01:30"/);
assert.match(fold, /data-original-at="2030-11-03T06:30:00Z"/);
const unread = values => view.readSchedule(new Map(Object.entries({schedule_kind:'unread_inventory',plan_timezone:'UTC',unread_operator:'lt',unread_threshold:'20',unread_check_minutes:'10',...values})));
assert.equal(unread({}).unread_condition.threshold, 20);
assert.equal(unread({}).interval_minutes, 10);
assert.equal(unread({unread_threshold:'0',unread_operator:'eq'}).unread_condition.threshold, 0);
for (const values of [{unread_threshold:''},{unread_threshold:'-1'},{unread_threshold:'1000001'},{unread_threshold:'1.5'},{unread_operator:'bad'},{unread_operator:'__proto__'},{unread_check_minutes:''},{unread_check_minutes:'0'},{unread_check_minutes:'10081'},{unread_check_minutes:'1.5'}]) {
  assert.throws(() => unread(values), /阈值|比较条件|检查间隔/);
}
const inventoryHTML = view.editorFields({schedule:{kind:'unread_inventory',timezone:'UTC',interval_minutes:15,unread_condition:{operator:'lte',threshold:0}}});
assert.match(inventoryHTML, /value="unread_inventory" selected/);
assert.match(inventoryHTML, /name="unread_threshold"[^>]*value="0"/);
assert.match(inventoryHTML, /name="unread_check_minutes"[^>]*value="15"/);
assert.match(inventoryHTML, /value="lte" selected/);
console.log('PASS: datetime picker conversions/DST (4 browser timezones), unread condition defaults/zero/validation, and edit values');
