import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const card = readFileSync(new URL('./Card.tsx', import.meta.url), 'utf8');
const list = readFileSync(new URL('./GroupListItem.tsx', import.meta.url), 'utf8');
const panel = readFileSync(new URL('./AvailabilityResultsPanel.tsx', import.meta.url), 'utf8');
const shared = readFileSync(new URL('./EditDialogContent.tsx', import.meta.url), 'utf8');

test('availability results use a full flexible editor area instead of a narrow side rail', () => {
    for (const source of [card, list]) {
        assert.doesNotMatch(source, /max-h-\[40vh\]/);
        assert.doesNotMatch(source, /2xl:w-80/);
    }
    assert.match(panel, /flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden/);
    assert.match(panel, /lg:grid-cols-2/);
});

test('availability results are read-only and long error details stay collapsed', () => {
    assert.doesNotMatch(panel, /MemberList|onReorder|onRemove|Draggable/);
    assert.match(panel, /<details className=/);
    assert.match(panel, /max-h-52 overflow-y-auto/);
    assert.match(panel, /status === 'testing'/);
});

test('group editor header actions use matching quiet secondary styling', () => {
    for (const source of [card, list]) {
        const header = source.slice(source.indexOf('<header'), source.indexOf('</header>'));
        assert.equal((header.match(/variant="ghost"/g) ?? []).length, 2);
        assert.equal((header.match(/border-border\/60 bg-muted\/30/g) ?? []).length, 2);
        assert.equal((header.match(/h-11 max-w-full/g) ?? []).length, 2);
        assert.equal((header.match(/hover:translate-y-0/g) ?? []).length, 2);
        assert.equal((header.match(/sm:h-9/g) ?? []).length, 2);
        assert.doesNotMatch(header, /bg-primary\b|text-primary-foreground|variant="default"/);
        assert.match(header, /onClick=\{onTestAvailability\}/);
        assert.match(header, /onSuccess=\{\(\) => setIsOpen\(false\)\}/);
    }
});

test('group editor actions wrap below the title on mobile and leave a separate close target', () => {
    for (const source of [card, list]) {
        const header = source.slice(source.indexOf('<header'), source.indexOf('</header>'));
        assert.match(header, /grid-cols-\[minmax\(0,1fr\)_auto\]/);
        assert.match(header, /col-span-2 row-start-2 flex min-w-0 flex-wrap/);
        assert.match(header, /sm:col-span-1 sm:col-start-2 sm:row-start-1/);
        assert.match(header, /<MorphingDialogClose className="relative col-start-2 row-start-1/);
        assert.match(header, /sm:right-auto sm:top-auto/);
        assert.match(header, /truncate[^\n]*title=\{group.name\}/);
    }
});

test('shared editor view keeps form mounted while switching to results', () => {
    assert.match(shared, /aria-hidden=\{view !== 'edit'\}/);
    assert.match(shared, /inert=\{view !== 'edit'\}/);
    assert.match(shared, /resultsTab/);
    assert.match(shared, /setView\('results'\)/);
});
