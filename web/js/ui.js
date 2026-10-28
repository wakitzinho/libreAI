/** Shared DOM, dialog, menu, clipboard, and formatting helpers. */

const SVG_NS = 'http://www.w3.org/2000/svg';

export function $(selector, root = document) {
  return root.querySelector(selector);
}

export function $$(selector, root = document) {
  return Array.from(root.querySelectorAll(selector));
}

export function icon(name, className = 'icon') {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', className);
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('viewBox', '0 0 24 24');
  const use = document.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#icon-' + name);
  svg.appendChild(use);
  return svg;
}

export function showToast(message, kind = 'info', timeout = 5200) {
  const container = document.getElementById('toasts');
  if (!container) return;
  const toast = document.createElement('div');
  toast.className = 'toast toast-' + kind;
  toast.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  toast.textContent = String(message);
  container.appendChild(toast);
  const remove = () => {
    toast.classList.add('toast-out');
    setTimeout(() => toast.remove(), 180);
  };
  const timer = setTimeout(remove, timeout);
  toast.addEventListener('click', () => {
    clearTimeout(timer);
    remove();
  });
}

function closeMenu() {
  const menu = document.querySelector('.menu');
  if (menu) menu.remove();
  document.removeEventListener('pointerdown', onOutside, true);
  document.removeEventListener('keydown', onKey, true);
  window.removeEventListener('resize', closeMenu);
  window.removeEventListener('scroll', closeMenu, true);
}

function onOutside(event) {
  if (!event.target.closest('.menu')) closeMenu();
}

function onKey(event) {
  if (event.key === 'Escape') closeMenu();
}

export function openMenu(anchor, items) {
  closeMenu();
  const menu = document.createElement('div');
  menu.className = 'menu';
  menu.setAttribute('role', 'menu');
  for (const item of items) {
    if (item.separator) {
      const sep = document.createElement('div');
      sep.className = 'menu-separator';
      menu.appendChild(sep);
      continue;
    }
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'menu-item' + (item.danger ? ' danger' : '');
    button.setAttribute('role', 'menuitem');
    button.textContent = item.label;
    button.addEventListener('click', () => {
      closeMenu();
      setTimeout(() => item.onSelect(), 0);
    });
    menu.appendChild(button);
  }
  document.body.appendChild(menu);

  const rect = anchor.getBoundingClientRect();
  const menuRect = menu.getBoundingClientRect();
  const padding = 8;
  let left = Math.min(rect.left, window.innerWidth - menuRect.width - padding);
  left = Math.max(padding, left);
  let top = rect.bottom + 6;
  if (top + menuRect.height > window.innerHeight - padding) {
    top = Math.max(padding, rect.top - menuRect.height - 6);
  }
  menu.style.left = left + 'px';
  menu.style.top = top + 'px';

  document.addEventListener('pointerdown', onOutside, true);
  document.addEventListener('keydown', onKey, true);
  window.addEventListener('resize', closeMenu);
  window.addEventListener('scroll', closeMenu, true);
  const first = menu.querySelector('button');
  if (first) first.focus();
}

export function promptDialog({ title, label = '', value = '', maxLength = 200, confirmLabel = 'Save' }) {
  const dialog = document.getElementById('prompt-dialog');
  const form = document.getElementById('prompt-form');
  const titleEl = document.getElementById('prompt-title');
  const labelEl = document.getElementById('prompt-label');
  const input = document.getElementById('prompt-input');
  const ok = document.getElementById('prompt-ok');
  titleEl.textContent = title;
  labelEl.textContent = label;
  labelEl.hidden = !label;
  input.value = value;
  input.maxLength = maxLength;
  ok.textContent = confirmLabel;
  return new Promise((resolve) => {
    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      form.onsubmit = null;
      dialog.querySelectorAll('[data-close]').forEach((button) => { button.onclick = null; });
      dialog.oncancel = null;
      dialog.onclose = null;
      resolve(result);
    };
    form.onsubmit = (event) => {
      event.preventDefault();
      const value = input.value.trim();
      if (!value) {
        input.focus();
        return;
      }
      dialog.close();
      finish(value);
    };
    dialog.querySelectorAll('[data-close]').forEach((button) => {
      button.onclick = () => {
        dialog.close();
        finish(null);
      };
    });
    dialog.oncancel = (event) => {
      event.preventDefault();
      dialog.close();
      finish(null);
    };
    dialog.showModal();
    input.focus();
    input.select();
  });
}

export function confirmDialog({ title = 'Confirm', message, confirmLabel = 'Delete', danger = true }) {
  const dialog = document.getElementById('confirm-dialog');
  const titleEl = document.getElementById('confirm-title');
  const messageEl = document.getElementById('confirm-message');
  const ok = document.getElementById('confirm-ok');
  titleEl.textContent = title;
  messageEl.textContent = message;
  ok.textContent = confirmLabel;
  ok.className = danger ? 'btn danger-solid' : 'btn primary';
  return new Promise((resolve) => {
    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      ok.onclick = null;
      dialog.querySelectorAll('[data-close]').forEach((button) => { button.onclick = null; });
      dialog.oncancel = null;
      dialog.onclose = null;
      resolve(result);
    };
    ok.onclick = () => {
      dialog.close();
      finish(true);
    };
    dialog.querySelectorAll('[data-close]').forEach((button) => {
      button.onclick = () => {
        dialog.close();
        finish(false);
      };
    });
    dialog.oncancel = (event) => {
      event.preventDefault();
      dialog.close();
      finish(false);
    };
    dialog.showModal();
    ok.focus();
  });
}

export function formatTimestamp(iso) {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const now = new Date();
  const sameDay = date.toDateString() === now.toDateString();
  if (sameDay) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' +
    date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export async function copyText(text) {
  const value = String(text ?? '');
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const area = document.createElement('textarea');
  area.value = value;
  area.setAttribute('readonly', '');
  area.className = 'copy-fallback';
  document.body.appendChild(area);
  area.select();
  const ok = document.execCommand('copy');
  area.remove();
  if (!ok) throw new Error('Copy failed');
}

export function flashButton(button, label = 'Copied') {
  if (!button) return;
  if (button.dataset.flashing === '1') return;
  const original = button.innerHTML;
  button.dataset.flashing = '1';
  button.textContent = label;
  setTimeout(() => {
    button.innerHTML = original;
    delete button.dataset.flashing;
  }, 1300);
}

export function downloadURL(url) {
  const link = document.createElement('a');
  link.href = url;
  link.rel = 'noopener';
  link.className = 'hidden-download';
  document.body.appendChild(link);
  link.click();
  link.remove();
}

export function debounce(fn, wait = 100) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), wait);
  };
}

export function autoGrow(element, maxHeight = 200) {
  if (!element) return;
  element.style.height = 'auto';
  element.style.height = Math.min(element.scrollHeight, maxHeight) + 'px';
  if (element.isContentEditable) {
    element.style.overflowY = element.scrollHeight > maxHeight ? 'auto' : 'hidden';
  }
}
