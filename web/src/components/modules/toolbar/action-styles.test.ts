import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  TOOLBAR_ACTION_CLASS,
  TOOLBAR_ICON_ACTION_CLASS,
  TOOLBAR_TEXT_ACTION_CLASS,
  TOOLBAR_PRIMARY_ACTION_CLASS,
} from './action-styles.ts';

const toolbar = readFileSync(new URL('./index.tsx', import.meta.url), 'utf8');
const maintenance = readFileSync(new URL('../group/MaintenanceButton.tsx', import.meta.url), 'utf8');

test('toolbar actions share responsive dimensions and interaction styles', () => {
  for (const style of [TOOLBAR_ICON_ACTION_CLASS, TOOLBAR_TEXT_ACTION_CLASS, TOOLBAR_PRIMARY_ACTION_CLASS]) {
    assert.ok(style.startsWith(TOOLBAR_ACTION_CLASS));
    assert.match(style, /\bmin-h-11\b/);
    assert.match(style, /sm:h-11/);
    assert.match(style, /rounded-lg/);
    assert.match(style, /focus-visible:ring/);
    assert.doesNotMatch(style, /translate|scale/);
  }
  assert.match(TOOLBAR_PRIMARY_ACTION_CLASS, /border-primary bg-primary text-primary-foreground/);
  assert.match(TOOLBAR_PRIMARY_ACTION_CLASS, /dark:hover:bg-primary\/90/);
});

test('toolbar and group triggers use the same action styles', () => {
  assert.match(toolbar, /TOOLBAR_ICON_ACTION_CLASS/);
  assert.match(toolbar, /TOOLBAR_TEXT_ACTION_CLASS/);
  assert.match(toolbar, /TOOLBAR_PRIMARY_ACTION_CLASS/);
  assert.match(maintenance, /className: TOOLBAR_TEXT_ACTION_CLASS/);
});

test('toolbar avoids nested filter borders and keeps small-screen icons visible', () => {
  assert.doesNotMatch(toolbar, /className="flex h-9 min-w-0 items-center gap-0\.5 rounded-xl border/);
  assert.match(toolbar, /className="flex max-w-full items-center gap-1 sm:gap-2/);
  assert.match(toolbar, /header-action-icon\]:max-\[380px\]:!block/);
  assert.match(toolbar, /header-action-icon\]:max-\[380px\]:!size-4/);
  assert.match(toolbar, /calc\(100vw-12rem\)/);
});

test('model view switcher uses a compact touch selector on mobile and segmented buttons on desktop', () => {
  assert.match(toolbar, /<Select[\s\S]*?value=\{modelView\}[\s\S]*?onValueChange=\{\(value\) => setModelView\(value as ModelView\)\}/);
  assert.match(toolbar, /className="h-11 w-\[9\.5rem\] min-w-0 max-\[380px\]:w-28 sm:hidden"/);
  assert.match(toolbar, /className="hidden items-center gap-0\.5 rounded-lg border border-border bg-card p-0\.5 sm:flex/);
  assert.match(toolbar, /<SelectItem value="market">\{modelT\('marketTitle'\)\}<\/SelectItem>/);
  assert.match(toolbar, /<SelectItem value="endpoints">\{endpointsT\('title'\)\}<\/SelectItem>/);
  assert.match(toolbar, /<SelectItem value="categories">\{modelT\('priceCategory\.title'\)\}<\/SelectItem>/);
});
