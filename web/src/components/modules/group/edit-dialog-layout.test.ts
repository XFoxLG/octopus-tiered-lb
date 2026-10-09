import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const card = readFileSync(new URL('./Card.tsx', import.meta.url), 'utf8');
const list = readFileSync(new URL('./GroupListItem.tsx', import.meta.url), 'utf8');
const panel = readFileSync(new URL('./AvailabilityResultsPanel.tsx', import.meta.url), 'utf8');
const shared = readFileSync(new URL('./EditDialogContent.tsx', import.meta.url), 'utf8');
const editor = readFileSync(new URL('./Editor.tsx', import.meta.url), 'utf8');
const create = readFileSync(new URL('./Create.tsx', import.meta.url), 'utf8');
const index = readFileSync(new URL('./index.tsx', import.meta.url), 'utf8');
const toolbar = readFileSync(new URL('../toolbar/index.tsx', import.meta.url), 'utf8');

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

test('model picker bounds its own scroll area in stacked layouts', () => {
    const picker = editor.slice(editor.indexOf('function ModelPickerSection'), editor.indexOf('function SortSection'));
    const scrollClasses = picker.match(/<div className="([^"]*overflow-y-auto[^"]*)">/)?.[1].split(/\s+/);

    assert.ok(scrollClasses, 'model picker must have an internal scroll area');
    assert.ok(scrollClasses.includes('max-h-[28rem]'), 'stacked desktop and mobile layouts need a height limit');
    assert.ok(scrollClasses.includes('2xl:max-h-none'), 'side-by-side layout must fill the height set by the configuration panel');
    assert.ok(scrollClasses.includes('min-h-0'));
    assert.ok(scrollClasses.includes('flex-1'));
    assert.match(picker, /flex min-h-\[22rem\] flex-col/);
    assert.doesNotMatch(picker, /\b(?:lg|xl):(?:h-full|min-h-0)/);
});

test('wide model panels stretch to the configuration panel without sizing the grid row from their contents', () => {
    assert.match(editor, /2xl:items-stretch/);
    assert.match(editor, /2xl:min-h-0 2xl:flex-1 2xl:\[contain:size\]/);
    assert.match(editor, /bg-card shadow-sm 2xl:min-h-0/);
    assert.match(editor, /min-h-\[28rem\][^"\n]*2xl:min-h-0/);
    assert.match(editor, /min-h-\[34rem\][^"\n]*2xl:min-h-0/);
    assert.doesNotMatch(editor, /\b(?:lg|xl):(?:h-full|min-h-0|flex-1)/);
});

test('wide dialogs hug the editor content instead of reserving viewport-height whitespace', () => {
    for (const source of [card, list, index]) {
        const dialogClasses = source.match(/<MorphingDialogContent className="([^"]*)"/)?.[1].split(/\s+/);
        assert.ok(dialogClasses?.includes('2xl:h-auto'), 'wide dialog must size itself to its content');
    }
    const groupCreateClasses = toolbar.match(/if \(activeItem === 'group'\) \{\s*return '([^']*)'/)?.[1].split(/\s+/);
    assert.ok(groupCreateClasses?.includes('2xl:h-auto'), 'toolbar group creation must also hug its content');
    for (const source of [card, list, create]) {
        assert.match(source, /max-w-full flex-1 flex-col 2xl:h-auto/);
    }
    assert.match(editor, /overflow-hidden 2xl:h-auto/);
    assert.doesNotMatch(editor, /min-h-full|mt-auto/);
    assert.match(editor, /shrink-0 pr-1 pt-4/);
});

test('shared editor view keeps form mounted while switching to results', () => {
    assert.match(shared, /aria-hidden=\{view !== 'edit'\}/);
    assert.match(shared, /inert=\{view !== 'edit'\}/);
    assert.match(shared, /resultsTab/);
    assert.match(shared, /setView\('results'\)/);
});
