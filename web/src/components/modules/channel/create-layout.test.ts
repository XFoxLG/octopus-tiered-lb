import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const create = readFileSync(new URL('./Create.tsx', import.meta.url), 'utf8');
const form = readFileSync(new URL('./Form.tsx', import.meta.url), 'utf8');
const toolbar = readFileSync(new URL('../toolbar/index.tsx', import.meta.url), 'utf8');

test('channel creation starts with explicit interfaces without brand presets', () => {
  assert.match(create, /connection_config: newConnectionConfig\(\)/);
  assert.doesNotMatch(create, /showPresetPicker|TemplatePickerGrid/);
  assert.match(form, /<ConnectionEditor/);
  assert.match(create, /layout="create"/);
  assert.match(create, /cancelText=\{tForm\('modelPicker.cancel'\)\}/);
});

test('channel dialog overrides default width and height constraints without nested decoration', () => {
  assert.match(toolbar, /if \(activeItem === 'channel'\)[\s\S]*?sm:max-w-none/);
  assert.match(toolbar, /max-h-\[calc\(100dvh-1rem\)\]/);
  assert.match(toolbar, /md:w-\[min\(100vw-3rem,64rem\)\]/);
  assert.doesNotMatch(create, /radial-gradient|shadow-inner|tracking-tight/);
  assert.match(create, /disableLayoutAnimation className="flex min-h-0 flex-1 flex-col overflow-hidden"/);
});

test('key strategy and proxy mode stay inside advanced settings', () => {
  // 高级设置自 v2.8.x 起从 Accordion 换成原生 <details>（默认收起，键盘可达）。
  const advancedStart = form.indexOf("{t('advanced')}");
  assert.notEqual(advancedStart, -1, '高级设置折叠区应存在');
  const advanced = form.slice(advancedStart, form.indexOf('</details>', advancedStart));
  assert.match(advanced, /t\('keySelectionStrategy'\)/);
  assert.match(advanced, /grid min-w-0 items-start gap-4 md:grid-cols-2/);
  assert.match(advanced, /<ProxySelector\s+layout="stacked"/);
  assert.equal(form.split("t('keySelectionStrategy')").length - 1, 1);
});

test('creation form scrolls independently and keeps safe-area-aware actions outside the scroll region', () => {
  assert.match(form, /layout = 'default'/);
  assert.match(form, /md:grid-cols-2/);
  assert.match(form, /overflow-y-auto overscroll-contain/);
  assert.match(form, /env\(safe-area-inset-bottom\)/);
  assert.match(form, /onClick=\{onCancel\}\s+disabled=\{isPending\}/);
  assert.match(form, /aria-label=\{`\$\{t\('apiKey'\)\} \$\{idx \+ 1\}`\}/);
  assert.match(form, /break-all whitespace-normal/);
  assert.doesNotMatch(form, /\[&_\[data-slot=select-trigger\]\]:w-full/);
  assert.match(form, /lg:grid-cols-\[minmax\(0,1fr\)_8rem_6\.5rem_2\.75rem\]/);
});
