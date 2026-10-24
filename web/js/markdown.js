/**
 * Small, dependency-free, security-first Markdown renderer for libreAI.
 *
 * Raw HTML is never trusted. Every text fragment is escaped before it reaches
 * the DOM; only a fixed set of tags generated here is emitted. Links are
 * restricted to http, https, and mailto with no embedded credentials.
 */

const KEYWORDS = {
  go: new Set(('break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var true false nil iota').split(' ')),
  javascript: new Set(('await async break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof let new of return static super switch this throw try typeof var void while with yield true false null undefined').split(' ')),
  typescript: new Set(('abstract any as asserts async await boolean break case catch class const constructor continue declare default delete do else enum export extends false finally for from function get if implements import in infer instanceof interface is keyof let module namespace never new null number object of private protected public readonly require return set static string super switch symbol this throw true try type typeof undefined unique unknown var void while yield').split(' ')),
  python: new Set(('and as assert async await break class continue def del elif else except False finally for from global if import in is lambda None nonlocal not or pass raise return True try while with yield match case').split(' ')),
  json: new Set(('true false null').split(' ')),
  bash: new Set(('if then else elif fi for while in do done case esac function select until time coproc break continue return exit export local readonly declare unset shift source alias echo cd pwd set trap true false').split(' ')),
  sql: new Set(('select from where and or not null insert into values update delete create table alter drop index join left right inner outer on group by order having limit offset as distinct union all case when then else end primary key foreign references default check unique begin commit rollback').split(' ')),
  rust: new Set(('as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while').split(' ')),
  java: new Set(('abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while true false null').split(' ')),
  c: new Set(('auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while true false NULL').split(' ')),
  csharp: new Set(('abstract as base bool break byte case catch char checked class const continue decimal default delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new null object operator out override params private protected public readonly ref return sbyte sealed short sizeof stackalloc static string struct switch this throw true try typeof uint ulong unchecked unsafe ushort using virtual void volatile while async await var record').split(' ')),
  ruby: new Set(('alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield').split(' ')),
  php: new Set(('abstract and array as break callable case catch class clone const continue declare default die do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile eval exit extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new or print private protected public readonly require require_once return static switch throw trait try unset use var while xor yield true false null').split(' ')),
  yaml: new Set(('true false null yes no on off').split(' ')),
  toml: new Set(('true false').split(' ')),
  css: new Set(('important media supports keyframes import charset font-face from to').split(' ')),
  docker: new Set(('FROM RUN CMD LABEL EXPOSE ENV ADD COPY ENTRYPOINT VOLUME USER WORKDIR ARG ONBUILD STOPSIGNAL HEALTHCHECK SHELL').split(' ')),
};

const LANGUAGE_ALIASES = {
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  ts: 'typescript', tsx: 'typescript',
  py: 'python', python3: 'python',
  sh: 'bash', shell: 'bash', zsh: 'bash', console: 'bash',
  golang: 'go', rs: 'rust', 'c++': 'cpp', cc: 'cpp', h: 'c', hpp: 'cpp',
  cs: 'csharp', 'c#': 'csharp', yml: 'yaml', jsonc: 'json',
  html: 'html', xml: 'xml', svg: 'html', md: 'markdown',
  dockerfile: 'docker', make: 'bash', makefile: 'bash',
};

const LANG_CONFIGS = {
  go: { line: '//', block: ['/*', '*/'], strings: '"\'`', keywords: KEYWORDS.go },
  javascript: { line: '//', block: ['/*', '*/'], strings: '"\'`', keywords: KEYWORDS.javascript, template: true },
  typescript: { line: '//', block: ['/*', '*/'], strings: '"\'`', keywords: KEYWORDS.typescript, template: true },
  python: { line: '#', strings: '"\'', keywords: KEYWORDS.python, triple: true },
  json: { strings: '"', keywords: KEYWORDS.json },
  bash: { line: '#', strings: '"\'`', keywords: KEYWORDS.bash },
  sql: { line: '--', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.sql },
  rust: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.rust },
  java: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.java },
  c: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.c },
  cpp: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.c },
  csharp: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.csharp },
  ruby: { line: '#', strings: '"\'', keywords: KEYWORDS.ruby },
  php: { line: '//', block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.php },
  yaml: { line: '#', strings: '"\'', keywords: KEYWORDS.yaml },
  toml: { line: '#', strings: '"\'', keywords: KEYWORDS.toml },
  css: { block: ['/*', '*/'], strings: '"\'', keywords: KEYWORDS.css },
  html: { block: ['<!--', '-->'], strings: '"\'', keywords: new Set() },
  xml: { block: ['<!--', '-->'], strings: '"\'', keywords: new Set() },
  markdown: { strings: '`', keywords: new Set() },
  docker: { line: '#', strings: '"\'', keywords: KEYWORDS.docker },
  text: { strings: '', keywords: new Set() },
};

const DEFAULT_LANG = { line: '//', block: ['/*', '*/'], strings: '"\'`', keywords: new Set() };

export function escapeHTML(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function safeURL(raw) {
  const value = String(raw).trim();
  if (!/^(https?:|mailto:)/i.test(value)) return null;
  try {
    const parsed = new URL(value);
    if (!['http:', 'https:', 'mailto:'].includes(parsed.protocol)) return null;
    if (parsed.username || parsed.password) return null;
    return parsed.href;
  } catch {
    return null;
  }
}

function isWordChar(ch) {
  return !!ch && /[A-Za-z0-9_]/.test(ch);
}

function findClosing(text, marker, from) {
  let index = text.indexOf(marker, from);
  while (index !== -1) {
    let backslashes = 0;
    for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) backslashes++;
    if (backslashes % 2 === 0) return index;
    index = text.indexOf(marker, index + marker.length);
  }
  return -1;
}

export function renderInline(source, depth = 0) {
  if (typeof source !== 'string') return '';
  if (depth > 12) return escapeHTML(source);
  let out = '';
  let plain = '';
  const flush = () => {
    if (plain) {
      out += escapeHTML(plain);
      plain = '';
    }
  };
  let i = 0;
  while (i < source.length) {
    const ch = source[i];

    if (ch === '\\' && i + 1 < source.length && /[\\`*_{}\[\]()#+\-.!~>]/.test(source[i + 1])) {
      flush();
      out += escapeHTML(source[i + 1]);
      i += 2;
      continue;
    }

    if (ch === '`') {
      let ticks = 1;
      while (source[i + ticks] === '`') ticks++;
      const marker = '`'.repeat(ticks);
      const close = source.indexOf(marker, i + ticks);
      if (close !== -1) {
        flush();
        let code = source.slice(i + ticks, close);
        if (code.startsWith(' ') && code.endsWith(' ') && code.trim() !== '') code = code.slice(1, -1);
        out += '<code>' + escapeHTML(code) + '</code>';
        i = close + ticks;
        continue;
      }
    }

    if (ch === '[') {
      const labelEnd = source.indexOf('](', i + 1);
      if (labelEnd !== -1) {
        const urlEnd = source.indexOf(')', labelEnd + 2);
        if (urlEnd !== -1) {
          const label = source.slice(i + 1, labelEnd);
          const url = safeURL(source.slice(labelEnd + 2, urlEnd));
          if (url) {
            flush();
            out += '<a href="' + escapeHTML(url) + '" target="_blank" rel="noopener noreferrer nofollow">' +
              renderInline(label, depth + 1) + '</a>';
            i = urlEnd + 1;
            continue;
          }
        }
      }
    }

    if (ch === '*' || ch === '_' || ch === '~') {
      let count = 1;
      while (source[i + count] === ch && count < 3) count++;
      const marker = ch.repeat(count);
      if (ch === '_' && count === 1 && (isWordChar(source[i - 1]) || isWordChar(source[i + 1]))) {
        plain += ch;
        i++;
        continue;
      }
      if ((ch === '~' && count < 2) || (count === 3 && ch !== '*')) {
        plain += marker;
        i += count;
        continue;
      }
      const close = findClosing(source, marker, i + count);
      if (close !== -1 && close > i + count) {
        flush();
        const inner = renderInline(source.slice(i + count, close), depth + 1);
        if (count === 2) out += ch === '~' ? '<del>' + inner + '</del>' : '<strong>' + inner + '</strong>';
        else if (count === 3) out += '<strong><em>' + inner + '</em></strong>';
        else out += '<em>' + inner + '</em>';
        i = close + count;
        continue;
      }
    }

    if (ch === '\n') {
      flush();
      out += '<br>';
      i++;
      continue;
    }

    if (ch === '<' || ch === '>' || ch === '&' || ch === '"' || ch === "'") {
      flush();
      out += escapeHTML(ch);
      i++;
      continue;
    }

    plain += ch;
    i++;
  }
  flush();
  return out;
}

function isFence(line) {
  return /^ {0,3}(`{3,}|~{3,})/.test(line);
}

function fenceInfo(line) {
  const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
  if (!match) return { marker: '```', info: '' };
  return { marker: match[1], info: match[2].trim() };
}

function isBlockStart(lines, index) {
  const line = lines[index];
  return isFence(line) ||
    /^ {0,3}#{1,6}\s+/.test(line) ||
    /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/.test(line) ||
    /^ {0,3}>\s?/.test(line) ||
    /^ {0,3}([-+*]|\d+[.)])\s+/.test(line);
}

function renderCodeBlock(code, rawInfo) {
  const first = String(rawInfo || '').trim().split(/\s+/)[0] || 'text';
  const lang = /^[a-zA-Z0-9+#._-]{0,30}$/.test(first) ? first : 'text';
  const display = lang === 'text' ? 'code' : lang;
  return '<div class="code-block"><div class="code-block-head"><span class="code-lang">' +
    escapeHTML(display) + '</span><button type="button" class="copy-code" data-copy-code>Copy</button></div>' +
    '<pre><code class="language-' + escapeHTML(lang) + '">' + highlightCode(code, lang) + '</code></pre></div>';
}

function renderList(lines, start) {
  const ordered = /^\s*\d+[.)]\s+/.test(lines[start]);
  const items = [];
  let i = start;
  while (i < lines.length) {
    const match = ordered
      ? /^\s*\d+[.)]\s+(.*)$/.exec(lines[i])
      : /^\s*[-+*]\s+(.*)$/.exec(lines[i]);
    if (!match) break;
    items.push('<li>' + renderInline(match[1]) + '</li>');
    i++;
  }
  const firstNumber = ordered ? parseInt(lines[start], 10) : 1;
  const tag = ordered ? 'ol' : 'ul';
  const startAttr = ordered && firstNumber !== 1 ? ' start="' + firstNumber + '"' : '';
  return { html: '<' + tag + startAttr + '>' + items.join('') + '</' + tag + '>', next: i };
}

export function renderMarkdown(source) {
  if (typeof source !== 'string' || source === '') return '';
  const lines = source.replace(/\r\n?/g, '\n').split('\n');
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === '') {
      i++;
      continue;
    }

    const fence = fenceInfo(line);
    if (isFence(line)) {
      const markerChar = fence.marker[0];
      const closeRe = new RegExp('^ {0,3}' + (markerChar === '`' ? '`' : '~') + '{' + fence.marker.length + ',}\\s*$');
      const code = [];
      i++;
      while (i < lines.length && !closeRe.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++;
      out.push(renderCodeBlock(code.join('\n'), fence.info));
      continue;
    }

    const heading = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (heading) {
      const level = heading[1].length;
      out.push('<h' + level + '>' + renderInline(heading[2]) + '</h' + level + '>');
      i++;
      continue;
    }

    if (/^ {0,3}([-*_])(?:\s*\1){2,}\s*$/.test(line)) {
      out.push('<hr>');
      i++;
      continue;
    }

    if (/^ {0,3}>\s?/.test(line)) {
      const quote = [];
      while (i < lines.length && /^ {0,3}>\s?/.test(lines[i])) {
        quote.push(lines[i].replace(/^ {0,3}>\s?/, ''));
        i++;
      }
      out.push('<blockquote>' + renderMarkdown(quote.join('\n')) + '</blockquote>');
      continue;
    }

    if (/^ {0,3}([-+*]|\d+[.)])\s+/.test(line)) {
      const list = renderList(lines, i);
      out.push(list.html);
      i = list.next;
      continue;
    }

    const paragraph = [line];
    i++;
    while (i < lines.length && lines[i].trim() !== '' && !isBlockStart(lines, i)) {
      paragraph.push(lines[i]);
      i++;
    }
    out.push('<p>' + renderInline(paragraph.join('\n')) + '</p>');
  }
  return out.join('\n');
}

function normalizeLang(lang) {
  const cleaned = String(lang || '').trim().toLowerCase().replace(/^language-/, '');
  return LANGUAGE_ALIASES[cleaned] || cleaned;
}

export function highlightCode(code, language) {
  const lang = normalizeLang(language);
  const cfg = LANG_CONFIGS[lang] || DEFAULT_LANG;
  const text = String(code);
  const keywords = cfg.keywords || new Set();
  const strings = cfg.strings || '';
  let out = '';
  let i = 0;

  const span = (cls, value) => '<span class="tok-' + cls + '">' + escapeHTML(value) + '</span>';

  while (i < text.length) {
    const ch = text[i];

    if (cfg.block) {
      const [open, close] = cfg.block;
      if (text.startsWith(open, i)) {
        const end = text.indexOf(close, i + open.length);
        const stop = end === -1 ? text.length : end + close.length;
        out += span('comment', text.slice(i, stop));
        i = stop;
        continue;
      }
    }

    if (cfg.line && text.startsWith(cfg.line, i)) {
      const end = text.indexOf('\n', i);
      const stop = end === -1 ? text.length : end;
      out += span('comment', text.slice(i, stop));
      i = stop;
      continue;
    }

    if (cfg.triple && (text.startsWith('"""', i) || text.startsWith("'''", i))) {
      const marker = text.slice(i, i + 3);
      const end = text.indexOf(marker, i + 3);
      const stop = end === -1 ? text.length : end + 3;
      out += span('string', text.slice(i, stop));
      i = stop;
      continue;
    }

    if (strings.includes(ch)) {
      const quote = ch;
      let j = i + 1;
      let escaped = false;
      while (j < text.length) {
        const c = text[j];
        if (escaped) {
          escaped = false;
        } else if (c === '\\') {
          escaped = true;
        } else if (c === quote) {
          j++;
          break;
        } else if (c === '\n' && quote !== '`') {
          break;
        }
        j++;
      }
      out += span('string', text.slice(i, j));
      i = j;
      continue;
    }

    if (/[0-9]/.test(ch)) {
      const match = /^(?:0[xX][0-9a-fA-F]+|0[bB][01]+|\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(text.slice(i));
      if (match) {
        out += span('number', match[0]);
        i += match[0].length;
        continue;
      }
    }

    if (/[A-Za-z_$]/.test(ch)) {
      let j = i + 1;
      while (j < text.length && /[A-Za-z0-9_$]/.test(text[j])) j++;
      const word = text.slice(i, j);
      if (keywords.has(word) || keywords.has(word.toLowerCase())) {
        out += span('keyword', word);
      } else if (text[j] === '(') {
        out += span('function', word);
      } else {
        out += escapeHTML(word);
      }
      i = j;
      continue;
    }

    out += escapeHTML(ch);
    i++;
  }
  return out;
}
