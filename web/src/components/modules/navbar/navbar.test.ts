import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { MOBILE_PRIMARY_NAV_COUNT, splitMobileNavItems } from './navbar-mobile.ts';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const source = fs.readFileSync(path.join(__dirname, 'navbar.tsx'), 'utf8');

test('active navbar item uses a visible accent color', () => {
    assert.match(source, /text-primary/);
    assert.equal(
        source.includes('text-sidebar-primary-foreground'),
        false,
        'active navbar items should not use the near-white sidebar foreground token',
    );
});

test('mobile nav labels escape overflow-x clipping via fixed anchors', () => {
    // Labels must not be absolute children of the overflow-x-auto nav.
    assert.match(source, /fixed z-\[60\]/);
    assert.match(source, /getBoundingClientRect/);
    assert.match(
        source,
        /overflow-x-auto[\s\S]*md:overflow-visible/,
        'nav strip still scrolls horizontally on mobile',
    );
    assert.equal(
        /absolute -top-7/.test(source),
        false,
        'legacy absolute -top-7 labels are clipped by overflow-x-auto',
    );
});

test('mobile navigation preserves configured order and keeps four primary routes', () => {
    const visibleRoutes = ['setting', 'home', 'group', 'ops', 'model', 'analytics', 'log'];
    const { primary, more } = splitMobileNavItems(visibleRoutes);

    assert.equal(MOBILE_PRIMARY_NAV_COUNT, 4);
    assert.deepEqual(primary, ['setting', 'home', 'group', 'ops']);
    assert.deepEqual(more, ['model', 'analytics', 'log']);
});

test('mobile navigation uses a modal more sheet with accessible dismissal and focus return', () => {
    assert.match(source, /displayedRoutes = isMobile \? mobilePrimaryRoutes : orderedRoutes/);
    assert.match(source, /isMoreActive = isMobile && mobileMoreRouteSet\.has\(activeItem\)/);
    assert.match(source, /aria-haspopup="dialog"/);
    assert.match(source, /aria-expanded=\{moreOpen\}/);
    assert.match(source, /dialog\.showModal\(\)/);
    assert.match(source, /buttonRefs\.current\.get\(`\$\{MORE_SHEET_ID\}-\$\{activeItem\}`\)/);
    assert.match(source, /onCancel=\{\(event\) => \{[\s\S]*event\.preventDefault\(\)[\s\S]*closeMoreSheet\(\)/);
    assert.match(source, /moreButtonRef\.current\?\.focus\(\)/);
});

test('mobile routes and sheet controls meet touch-target and active-route requirements', () => {
    assert.match(source, /isMobile \? "size-11"/);
    assert.match(source, /flex size-11 shrink-0/);
    assert.match(source, /min-h-14/);
    assert.match(source, /aria-current=\{isActive \? "page" : undefined\}/);
    assert.match(source, /selectMobileRoute\(route\.id as NavItem\)/);
    assert.match(source, /setActiveItem\(item\)[\s\S]*closeMoreSheet\(\)/);
    assert.match(source, /env\(safe-area-inset-bottom,0px\)/);
});
