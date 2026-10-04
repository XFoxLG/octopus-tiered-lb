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

test('shared editor view keeps form mounted while switching to results', () => {
    assert.match(shared, /aria-hidden=\{view !== 'edit'\}/);
    assert.match(shared, /inert=\{view !== 'edit'\}/);
    assert.match(shared, /resultsTab/);
    assert.match(shared, /setView\('results'\)/);
});
