import test from 'node:test';
import assert from 'node:assert/strict';

import { writeClipboardText } from './clipboard.ts';

type ClipboardOptions = NonNullable<Parameters<typeof writeClipboardText>[1]>;
type ClipboardLike = NonNullable<ClipboardOptions['clipboard']>;
type DocumentLike = NonNullable<ClipboardOptions['document']>;

type TextAreaLike = Pick<HTMLTextAreaElement, 'value' | 'setAttribute' | 'select'> & {
    style: Partial<CSSStyleDeclaration>;
};

test('writeClipboardText falls back to execCommand when clipboard permission is denied', async () => {
    const appended: Node[] = [];
    const removed: Node[] = [];
    let selected = false;

    const documentLike: DocumentLike = {
        body: {
            appendChild: <NodeType extends Node>(node: NodeType): NodeType => {
                appended.push(node);
                return node;
            },
            removeChild: <NodeType extends Node>(node: NodeType): NodeType => {
                removed.push(node);
                return node;
            },
        },
        // This partial DOM fixture only supports the textarea used by the fallback.
        createElement: ((tag: string) => {
            assert.equal(tag, 'textarea');
            const textArea: TextAreaLike = {
                value: '',
                style: {},
                setAttribute: () => {},
                select: () => {
                    selected = true;
                },
            };
            return textArea as HTMLTextAreaElement;
        }) as DocumentLike['createElement'],
        execCommand: (command) => {
            assert.equal(command, 'copy');
            return true;
        },
    };

    const clipboardLike: ClipboardLike = {
        writeText: async () => {
            throw new Error(`Failed to execute 'writeText' on 'Clipboard': Write permission denied.`);
        },
    };

    await assert.doesNotReject(() => writeClipboardText('sk-octopus-test', {
        clipboard: clipboardLike,
        document: documentLike,
    }));
    assert.equal(appended.length, 1);
    assert.equal(removed.length, 1);
    assert.equal(appended[0], removed[0]);
    assert.ok('value' in appended[0]);
    assert.equal(appended[0].value, 'sk-octopus-test');
    assert.equal(selected, true);
});

test('writeClipboardText surfaces the original clipboard error when no fallback is available', async () => {
    const expected = new Error(`Failed to execute 'writeText' on 'Clipboard': Write permission denied.`);
    const clipboardLike: ClipboardLike = {
        writeText: async () => {
            throw expected;
        },
    };

    await assert.rejects(
        () => writeClipboardText('sk-octopus-test', { clipboard: clipboardLike }),
        expected
    );
});
