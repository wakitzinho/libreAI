import test from 'node:test';
import assert from 'node:assert/strict';

import { ApiError, parseSSEEvent } from '../../web/js/api.js';

test('parses named SSE events with JSON data', () => {
  const parsed = parseSSEEvent('event: delta\ndata: {"text":"hello"}');
  assert.deepEqual(parsed, { event: 'delta', data: { text: 'hello' } });
});

test('joins multiple data lines', () => {
  const parsed = parseSSEEvent('event: message\ndata: {"a":\ndata: 1}');
  assert.equal(parsed.event, 'message');
  assert.equal(parsed.data.a, 1);
});

test('ignores comments and unknown fields', () => {
  const parsed = parseSSEEvent(': keep-alive\nid: 4\nretry: 500\nevent: done\ndata: {"ok":true}');
  assert.deepEqual(parsed, { event: 'done', data: { ok: true } });
});

test('handles a bare data field and default event name', () => {
  const parsed = parseSSEEvent('data: {"v":2}');
  assert.deepEqual(parsed, { event: 'message', data: { v: 2 } });
});

test('empty blocks and event-only blocks are ignored', () => {
  assert.equal(parseSSEEvent(''), null);
  assert.equal(parseSSEEvent('  \n\n'), null);
  assert.equal(parseSSEEvent('event: done'), null);
  assert.equal(parseSSEEvent('data:'), null);
});

test('malformed JSON raises a typed stream error', () => {
  assert.throws(
    () => parseSSEEvent('data: {oops}'),
    (error) => error instanceof ApiError && error.code === 'bad_stream' && error.status === 0,
  );
});
