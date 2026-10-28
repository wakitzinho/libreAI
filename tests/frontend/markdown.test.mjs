import test from 'node:test';
import assert from 'node:assert/strict';

import { escapeHTML, highlightCode, renderComposerMarkdown, renderInline, renderMarkdown } from '../../web/js/markdown.js';

function composerText(html) {
  return html
    .replace(/<span class="c-anchor">\u200b<\/span>/g, '')
    .replace(/<[^>]*>/g, '')
    .replace(/&#39;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&gt;/g, '>')
    .replace(/&lt;/g, '<')
    .replace(/&amp;/g, '&');
}

test('escapeHTML escapes all markup characters', () => {
  assert.equal(
    escapeHTML(`<script>"x" & 'y'</script>`),
    '&lt;script&gt;&quot;x&quot; &amp; &#39;y&#39;&lt;/script&gt;',
  );
});

test('raw HTML is always escaped', () => {
  const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>');
  assert.ok(!html.includes('<script>'));
  assert.ok(!html.includes('<img'));
  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(html.includes('&lt;img'));
});

test('unsafe link schemes are not turned into links', () => {
  for (const url of [
    'javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'https://user:pass@example.com/',
    '//example.com/path',
  ]) {
    const html = renderInline('[click](' + url + ')');
    assert.ok(!html.includes('<a '), 'unexpected link for ' + url + ': ' + html);
    assert.ok(html.includes('click'));
  }
});

test('safe links get noopener and friendly formatting', () => {
  const html = renderInline('[OpenAI](https://example.com/docs)');
  assert.ok(html.includes('<a href="https://example.com/docs"'));
  assert.ok(html.includes('target="_blank"'));
  assert.ok(html.includes('rel="noopener noreferrer nofollow"'));
  assert.ok(renderInline('[mail](mailto:user@example.com)').includes('href="mailto:user@example.com"'));
});

test('link labels cannot inject markup', () => {
  const html = renderInline('[<img src=x onerror=alert(1)>](https://example.com)');
  assert.ok(!html.includes('<img'));
  assert.ok(html.includes('&lt;img'));
});

test('common Markdown blocks render', () => {
  const html = renderMarkdown([
    '# Heading',
    '',
    'Paragraph with **bold**, *italic*, `code`, ~~gone~~ and a [link](https://example.com).',
    '',
    '- one',
    '- two',
    '',
    '1. first',
    '2. second',
    '',
    '> quoted **text**',
    '',
    '---',
    '',
    '```go',
    'fmt.Println("hi")',
    '```',
  ].join('\n'));
  assert.ok(html.includes('<h1>Heading</h1>'));
  assert.ok(html.includes('<strong>bold</strong>'));
  assert.ok(html.includes('<em>italic</em>'));
  assert.ok(html.includes('<code>code</code>'));
  assert.ok(html.includes('<del>gone</del>'));
  assert.ok(html.includes('<ul><li>one</li><li>two</li></ul>'));
  assert.ok(html.includes('<ol><li>first</li><li>second</li></ol>'));
  assert.ok(html.includes('<blockquote>'));
  assert.ok(html.includes('<hr>'));
  assert.ok(html.includes('class="code-block"'));
  assert.ok(html.includes('language-go'));
});

test('code blocks and copy controls are generated safely', () => {
  const html = renderMarkdown('```javascript\nconst x = "<img src=x onerror=alert(1)>";\n```');
  assert.ok(html.includes('data-copy-code'));
  assert.ok(html.includes('const'));
  assert.ok(!html.includes('<img'));
  assert.ok(html.includes('&lt;img'));
});

test('invalid or hostile language labels are sanitized', () => {
  const html = renderMarkdown('```"><script>alert(1)</script>\ncode\n```');
  assert.ok(!html.includes('<script>'));
  assert.ok(html.includes('language-text'));
});

test('highlighting escapes markup and marks tokens', () => {
  const html = highlightCode('const s = "<b>"; // comment\nif (x) return 42;', 'javascript');
  assert.ok(!html.includes('<b>'));
  assert.ok(html.includes('&lt;b&gt;'));
  assert.ok(html.includes('tok-keyword'));
  assert.ok(html.includes('tok-string'));
  assert.ok(html.includes('tok-comment'));
  assert.ok(html.includes('tok-number'));
});

test('unknown languages fall back to safe plain highlighting', () => {
  const html = highlightCode('<script>alert(1)</script>', 'not-a-language');
  assert.ok(!html.includes('<script>'));
  assert.ok(html.includes('&lt;script&gt;'));
});

test('unclosed code fences do not lose content', () => {
  const html = renderMarkdown('```python\nprint("hello")');
  assert.ok(html.includes('print'));
  assert.ok(html.includes('&quot;hello&quot;'));
});

test('empty and non-string input are safe', () => {
  assert.equal(renderMarkdown(''), '');
  assert.equal(renderMarkdown(null), '');
  assert.equal(renderInline(undefined), '');
});

test('backslash escapes markup markers', () => {
  const html = renderInline('\\*not italic\\* and \\[not a link\\]');
  assert.ok(html.includes('*not italic*'));
  assert.ok(!html.includes('<em>'));
});

test('composer rendering keeps the Markdown source recoverable', () => {
  const source = [
    '# Heading',
    '',
    '- one',
    '- two with **bold** and `code`',
    '',
    '> quoted *text*',
    '',
    '---',
    '',
    '```go',
    'if x < 1 {',
    '\tfmt.Println("<tag>")',
    '}',
    '```',
    '',
    '[docs](https://example.com) and ~~gone~~',
  ].join('\n');
  const html = renderComposerMarkdown(source);
  assert.equal(composerText(html), source);
  assert.ok(html.includes('<strong>bold</strong>'));
  assert.ok(html.includes('<em>text</em>'));
  assert.ok(html.includes('<code>code</code>'));
  assert.ok(html.includes('<del>gone</del>'));
  assert.ok(html.includes('c-h1'));
  assert.ok(html.includes('c-li'));
  assert.ok(html.includes('c-quote'));
  assert.ok(html.includes('c-code-line'));
  assert.ok(html.includes('c-link'));
  assert.ok(!html.includes('<tag>'));
});

test('composer rendering hides syntax markers but keeps them in the DOM', () => {
  const html = renderComposerMarkdown('**bold** and `code`');
  assert.ok(html.includes('<span class="c-mark">**</span>'));
  assert.ok(html.includes('<span class="c-mark">`</span>'));
  assert.ok(html.includes('c-anchor'));
});

test('composer rendering escapes hostile input and unsafe links', () => {
  const html = renderComposerMarkdown('<img src=x onerror=alert(1)> [x](javascript:alert(1))');
  assert.ok(!html.includes('<img'));
  assert.ok(html.includes('&lt;img'));
  assert.ok(!html.includes('class="c-link"'));
  assert.equal(renderComposerMarkdown(''), '');
  assert.equal(renderComposerMarkdown(null), '');
});
