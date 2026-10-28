/** Live Markdown composer backed by a contenteditable element. */

import { renderComposerMarkdown } from './markdown.js';

function normalizeText(value) {
  return String(value ?? '').replace(/\r\n?/g, '\n').replace(/\u00a0/g, ' ').replace(/\u200b/g, '');
}

/** Removes marker runs left behind after deleting all text from a construct. */
function stripEmptyEmphasis(value) {
  const cleaned = value
    .replace(/\*{6}|_{6}/g, '')
    .replace(/(?:^|[^*])\*{4}(?:[^*]|$)/g, (match) => match.replace(/\*{4}/, ''))
    .replace(/(?:^|[^_])_{4}(?:[^_]|$)/g, (match) => match.replace(/_{4}/, ''))
    .replace(/~{4}/g, '');
  return cleaned.split('\n').map((line) => {
    const trimmed = line.trim();
    if (/^\*{2}$/.test(trimmed) || /^`{2,}$/.test(trimmed) || /^\[\]\([^)]*\)$/.test(trimmed)) {
      return line.replace(trimmed, '');
    }
    return line;
  }).join('\n');
}

function sourceOffsetBefore(root, node) {
  const range = document.createRange();
  range.selectNodeContents(root);
  range.setEndBefore(node);
  return range.toString().replace(/\u200b/g, '').length;
}

function sourceOffsetAfter(root, node) {
  const range = document.createRange();
  range.selectNodeContents(root);
  range.setEndAfter(node);
  return range.toString().replace(/\u200b/g, '').length;
}

function domPointAt(root, index) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let node;
  let pos = 0;
  while ((node = walker.nextNode())) {
    const parent = node.parentElement;
    if (parent && parent.closest('.c-anchor')) continue;
    if (parent && parent.closest('.c-mark')) {
      pos += node.data.length;
      continue;
    }
    if (index < pos + node.data.length) return { node, offset: index - pos };
    pos += node.data.length;
  }
  return null;
}

function formattingAncestor(root, node) {
  let element = node.parentElement;
  let match = null;
  while (element && element !== root) {
    if (element.matches('strong, em, code, del, .c-link')) match = element;
    if (element.classList.contains('c-line')) break;
    element = element.parentElement;
  }
  return match;
}

function offsetAt(root, container, offset) {
  const range = document.createRange();
  range.selectNodeContents(root);
  range.setEnd(container, offset);
  return range.toString().replace(/\u200b/g, '').length;
}

function locate(root, offset) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let node;
  let pos = 0;

  while ((node = walker.nextNode())) {
    const parent = node.parentElement;
    const mark = parent ? parent.closest('.c-mark') : null;
    const anchor = parent ? parent.closest('.c-anchor') : null;

    if (anchor) {
      if (offset === pos) return { node, offset: 0 };
      continue;
    }

    if (mark) {
      if (offset <= pos + node.data.length) {
        const markParent = mark.parentNode;
        const index = Array.prototype.indexOf.call(markParent.childNodes, mark);
        const sibling = mark.nextSibling;
        if (offset > pos && sibling && sibling.nodeType === Node.ELEMENT_NODE &&
          sibling.classList.contains('c-anchor') && sibling.firstChild) {
          return { node: sibling.firstChild, offset: 0 };
        }
        if (offset <= pos) return { node: markParent, offset: index };
        return { node: markParent, offset: index + 1 };
      }
      pos += node.data.length;
      continue;
    }

    if (offset <= pos + node.data.length) {
      return { node, offset: Math.max(0, Math.min(offset - pos, node.data.length)) };
    }
    pos += node.data.length;
  }

  return { node: root, offset: root.childNodes.length };
}

export class MarkdownComposer {
  constructor(element) {
    this.element = element;
    this.lastValue = '';
    this.composing = false;
    this.compositionValue = null;
    this.compositionRange = null;
    this.history = [];
    this.redoStack = [];
    this.bind();
  }

  get value() {
    return normalizeText(this.element.textContent);
  }

  setValue(value, options = {}) {
    const text = normalizeText(value);
    this.lastValue = text;
    this.render(text);
    if (options.focus) this.element.focus();
    if (options.caretAtEnd || document.activeElement === this.element) {
      this.setSelection(text.length, text.length);
    }
  }

  focus() {
    this.element.focus();
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0 || !this.element.contains(selection.anchorNode)) {
      this.setSelection(this.value.length, this.value.length);
    }
  }

  insertText(text) {
    this.replaceSelection(text);
  }

  replaceSelection(text) {
    const offsets = this.selectionOffsets();
    const value = this.value;
    const start = offsets ? offsets.start : value.length;
    const end = offsets ? offsets.end : value.length;
    if (text === '' && offsets && start < end) {
      const construct = this.constructAt(start);
      if (construct && start === construct.contentStart && end === construct.contentEnd) {
        const next = stripEmptyEmphasis(value.slice(0, construct.start) + value.slice(construct.end));
        this.applyValue(next, Math.min(construct.start, next.length));
        return;
      }
    }
    const next = value.slice(0, start) + text + value.slice(end);
    if (next === value) return;
    this.applyValue(next, start + text.length);
  }

  deleteBackward() {
    const offsets = this.selectionOffsets();
    if (!offsets) return;
    if (offsets.start !== offsets.end) {
      this.replaceSelection('');
      return;
    }
    const value = this.value;
    if (offsets.start === 0) return;
    const ranges = this.markRanges();
    let index = offsets.start - 1;
    while (index >= 0 && this.indexHidden(index, ranges)) index--;
    if (index < 0) {
      this.applyValue(stripEmptyEmphasis(value.slice(offsets.start)), 0);
      return;
    }
    const construct = this.singleConstructRange(index);
    if (construct) {
      const [start, end] = construct;
      const next = stripEmptyEmphasis(value.slice(0, start) + value.slice(end));
      this.applyValue(next, Math.min(start, next.length));
      return;
    }
    const next = stripEmptyEmphasis(value.slice(0, index) + value.slice(index + 1));
    this.applyValue(next, Math.min(index, next.length));
  }

  deleteForward() {
    const offsets = this.selectionOffsets();
    if (!offsets) return;
    if (offsets.start !== offsets.end) {
      this.replaceSelection('');
      return;
    }
    const value = this.value;
    if (offsets.start >= value.length) return;
    const ranges = this.markRanges();
    let index = offsets.start;
    while (index < value.length && this.indexHidden(index, ranges)) index++;
    if (index >= value.length) return;
    const construct = this.singleConstructRange(index);
    if (construct) {
      const [start, end] = construct;
      const next = stripEmptyEmphasis(value.slice(0, start) + value.slice(end));
      this.applyValue(next, Math.min(offsets.start, next.length));
      return;
    }
    const next = stripEmptyEmphasis(value.slice(0, index) + value.slice(index + 1));
    this.applyValue(next, Math.min(offsets.start, next.length));
  }

  singleConstructRange(index) {
    const construct = this.constructAt(index);
    if (!construct || !construct.single) return null;
    return [construct.start, construct.end];
  }

  constructAt(index) {
    const point = domPointAt(this.element, index);
    if (!point) return null;
    const construct = formattingAncestor(this.element, point.node);
    if (!construct) return null;
    const open = construct.previousSibling;
    const close = construct.nextSibling;
    if (!open || !close || !open.classList || !open.classList.contains('c-mark') ||
      !close.classList || !close.classList.contains('c-mark')) {
      return null;
    }
    return {
      start: sourceOffsetBefore(this.element, open),
      end: sourceOffsetAfter(this.element, close),
      contentStart: sourceOffsetBefore(this.element, construct),
      contentEnd: sourceOffsetAfter(this.element, construct),
      single: construct.textContent.replace(/\u200b/g, '').length === 1,
    };
  }

  applyValue(next, caret) {
    if (next !== this.lastValue) {
      this.history.push(this.lastValue);
      if (this.history.length > 200) this.history.shift();
      this.redoStack.length = 0;
    }
    this.lastValue = next;
    this.render(next);
    this.setSelection(caret, caret);
    this.element.dispatchEvent(new Event('input', { bubbles: true }));
  }

  markRanges() {
    const ranges = [];
    const walker = document.createTreeWalker(this.element, NodeFilter.SHOW_TEXT);
    let node;
    let pos = 0;
    while ((node = walker.nextNode())) {
      const parent = node.parentElement;
      if (parent && parent.closest('.c-anchor')) continue;
      if (parent && parent.closest('.c-mark')) ranges.push([pos, pos + node.data.length]);
      pos += node.data.length;
    }
    return ranges;
  }

  indexHidden(index, ranges) {
    return ranges.some(([start, end]) => index >= start && index < end);
  }

  undo() {
    if (this.history.length === 0) return;
    this.redoStack.push(this.lastValue);
    const value = this.history.pop();
    this.lastValue = value;
    this.render(value);
    this.setSelection(value.length, value.length);
  }

  redo() {
    if (this.redoStack.length === 0) return;
    this.history.push(this.lastValue);
    const value = this.redoStack.pop();
    this.lastValue = value;
    this.render(value);
    this.setSelection(value.length, value.length);
  }

  bind() {
    const element = this.element;

    element.addEventListener('input', () => this.sync());
    element.addEventListener('compositionstart', () => {
      this.composing = true;
      this.compositionValue = this.value;
      this.compositionRange = this.selectionOffsets();
    });
    element.addEventListener('compositionend', (event) => {
      this.composing = false;
      const base = this.compositionValue;
      const range = this.compositionRange;
      const data = typeof event.data === 'string' ? event.data : null;
      this.compositionValue = null;
      this.compositionRange = null;
      if (base === null || data === null) {
        this.sync();
        return;
      }
      const start = range ? range.start : base.length;
      const end = range ? range.end : start;
      const next = base.slice(0, start) + data + base.slice(end);
      if (next === base) {
        this.lastValue = base;
        this.render(base);
        this.setSelection(start, start);
        return;
      }
      this.applyValue(next, start + data.length);
    });
    element.addEventListener('paste', (event) => {
      event.preventDefault();
      const text = event.clipboardData ? event.clipboardData.getData('text/plain') : '';
      if (text) this.insertText(text);
    });
    element.addEventListener('copy', (event) => {
      const offsets = this.selectionOffsets();
      if (!offsets || offsets.start === offsets.end || !event.clipboardData) return;
      event.preventDefault();
      event.clipboardData.setData('text/plain', this.value.slice(offsets.start, offsets.end));
    });
    element.addEventListener('cut', (event) => {
      const offsets = this.selectionOffsets();
      if (!offsets || offsets.start === offsets.end) return;
      event.preventDefault();
      if (event.clipboardData) {
        event.clipboardData.setData('text/plain', this.value.slice(offsets.start, offsets.end));
      }
      this.replaceSelection('');
    });
    element.addEventListener('drop', (event) => event.preventDefault());
    element.addEventListener('beforeinput', (event) => {
      if (event.isComposing) return;
      if (event.inputType === 'insertText' && typeof event.data === 'string') {
        event.preventDefault();
        this.replaceSelection(event.data);
        return;
      }
      if (event.inputType === 'deleteContentBackward') {
        event.preventDefault();
        this.deleteBackward();
        return;
      }
      if (event.inputType === 'deleteContentForward') {
        event.preventDefault();
        this.deleteForward();
        return;
      }
      if (event.inputType === 'insertParagraph' || event.inputType === 'insertLineBreak') {
        event.preventDefault();
        this.insertText('\n');
      }
    });
    element.addEventListener('keydown', (event) => {
      if (event.isComposing || !(event.ctrlKey || event.metaKey)) return;
      const key = event.key.toLowerCase();
      if (key === 'a') {
        event.preventDefault();
        const range = document.createRange();
        range.selectNodeContents(element);
        const selection = window.getSelection();
        if (selection) {
          selection.removeAllRanges();
          selection.addRange(range);
        }
      } else if (key === 'z') {
        event.preventDefault();
        if (event.shiftKey) this.redo();
        else this.undo();
      } else if (key === 'y') {
        event.preventDefault();
        this.redo();
      }
    });
  }

  render(value) {
    this.element.innerHTML = renderComposerMarkdown(value);
  }

  sync() {
    if (this.composing) return;
    const value = this.value;
    const offsets = this.selectionOffsets();
    if (value !== this.lastValue) {
      this.history.push(this.lastValue);
      if (this.history.length > 200) this.history.shift();
      this.redoStack.length = 0;
    }
    this.lastValue = value;
    this.render(value);
    if (offsets) this.setSelection(offsets.start, offsets.end);
  }

  selectionOffsets() {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0) return null;
    const range = selection.getRangeAt(0);
    if (!this.element.contains(range.startContainer)) return null;
    return {
      start: offsetAt(this.element, range.startContainer, range.startOffset),
      end: offsetAt(this.element, range.endContainer, range.endOffset),
    };
  }

  setSelection(start, end = start) {
    const selection = window.getSelection();
    if (!selection) return;
    const range = document.createRange();
    const first = locate(this.element, start);
    if (!first) {
      range.selectNodeContents(this.element);
      range.collapse(false);
    } else {
      range.setStart(first.node, first.offset);
      const second = locate(this.element, end);
      if (second) range.setEnd(second.node, second.offset);
      else range.collapse(false);
    }
    selection.removeAllRanges();
    selection.addRange(range);
  }
}
