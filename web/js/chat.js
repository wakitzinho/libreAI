/** Conversation view, SSE streaming, composer, edit/regenerate, and Markdown actions. */

import { renderMarkdown } from './markdown.js';
import { autoGrow, copyText, flashButton, formatTimestamp, icon, showToast } from './ui.js';

function makeButton(className, label, iconName) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = className;
  if (iconName) button.appendChild(icon(iconName));
  const span = document.createElement('span');
  span.textContent = label;
  button.appendChild(span);
  return button;
}

export class ChatView {
  constructor(elements, options) {
    this.elements = elements;
    this.api = options.api;
    this.ensureChat = options.ensureChat;
    this.onChatMeta = options.onChatMeta || (() => {});
    this.onGenerationChange = options.onGenerationChange || (() => {});

    this.providers = [];
    this.hidden = new Set();
    this.defaults = { providerId: '', modelId: '' };
    this.chat = null;
    this.messages = [];
    this.selected = { providerId: '', modelId: '' };
    this.stream = null;
    this.generationSeq = 0;
    this.renderQueued = false;
    this.messageEls = new Map();
    this.updateTimer = null;

    this.bind();
  }

  bind() {
    const e = this.elements;
    e.form.addEventListener('submit', (event) => {
      event.preventDefault();
      this.submit();
    });
    e.input.addEventListener('input', () => {
      autoGrow(e.input);
      this.updateSendState();
    });
    e.input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
        event.preventDefault();
        if (!e.send.disabled) e.form.requestSubmit();
      }
    });
    e.stop.addEventListener('click', () => this.stop());
    e.modelSelect.addEventListener('change', () => this.modelChanged());
    e.messages.addEventListener('click', (event) => this.messageAction(event));
    e.empty.addEventListener('click', (event) => {
      const suggestion = event.target.closest('[data-suggestion]');
      if (suggestion) {
        e.input.value = suggestion.dataset.suggestion;
        autoGrow(e.input);
        this.updateSendState();
        e.input.focus();
      }
    });
    this.updateSendState();
  }

  setProviders(providers, hidden) {
    this.providers = providers || [];
    this.hidden = hidden || new Set();
    this.renderModelSelect();
  }

  setDefaults(providerId, modelId) {
    this.defaults = { providerId: providerId || '', modelId: modelId || '' };
    if (!this.chat || (!this.chat.provider_id && !this.chat.model_id)) {
      if (!this.selected.providerId && this.defaults.providerId) {
        this.selected = { ...this.defaults };
        this.renderModelSelect();
      }
    }
  }

  getSelection() {
    return { ...this.selected };
  }

  setSelection(providerId, modelId) {
    this.selected = { providerId: providerId || '', modelId: modelId || '' };
    this.renderModelSelect();
  }

  hasSelection() {
    return !!(this.selected.providerId && this.selected.modelId);
  }

  currentChat() {
    return this.chat;
  }

  isStreaming() {
    return !!this.stream;
  }

  hasEmptyChat() {
    return !!this.chat && this.messages.length === 0;
  }

  adoptChat(chat) {
    this.chat = chat;
    this.messages = [];
    if (chat.provider_id && chat.model_id) {
      this.selected = { providerId: chat.provider_id, modelId: chat.model_id };
    }
    this.updateTitle();
    this.renderModelSelect();
    this.renderAll(true);
  }

  setChat(chat, messages) {
    this.cancelActive();
    this.chat = chat;
    this.messages = Array.isArray(messages) ? messages : [];
    if (chat && chat.provider_id && chat.model_id) {
      this.selected = { providerId: chat.provider_id, modelId: chat.model_id };
    } else if (this.defaults.providerId) {
      this.selected = { ...this.defaults };
    }
    this.updateTitle();
    this.renderModelSelect();
    this.renderAll(true);
    if (window.matchMedia('(min-width: 821px)').matches) this.focusComposer();
  }

  applyChatMetadata(chat) {
    if (!this.chat || this.chat.id !== chat.id) return;
    this.chat = { ...this.chat, ...chat };
    if (chat.provider_id && chat.model_id) {
      this.selected = { providerId: chat.provider_id, modelId: chat.model_id };
    }
    this.updateTitle();
    this.renderModelSelect();
    this.onChatMeta(this.chat);
  }

  clear() {
    this.cancelActive();
    this.chat = null;
    this.messages = [];
    this.selected = { ...this.defaults };
    this.updateTitle();
    this.renderModelSelect();
    this.renderAll(false);
  }

  cancelActive() {
    this.generationSeq++;
    if (this.stream) {
      try { this.stream.controller.abort(); } catch { /* already closed */ }
      this.stream = null;
    }
    if (this.updateTimer) {
      clearTimeout(this.updateTimer);
      this.updateTimer = null;
    }
    this.setStreamingUI(false);
  }

  updateTitle() {
    const title = this.chat ? this.chat.title : 'libreAI';
    this.elements.title.textContent = title;
    this.elements.title.title = title;
  }

  focusComposer() {
    this.elements.input.focus();
  }

  updateSendState() {
    const empty = this.elements.input.value.trim() === '';
    this.elements.send.disabled = !!this.stream || empty;
  }

  setStreamingUI(streaming) {
    const e = this.elements;
    e.send.hidden = streaming;
    e.send.disabled = streaming || e.input.value.trim() === '';
    e.stop.hidden = !streaming;
    e.stop.disabled = false;
    e.stop.textContent = 'Stop';
    e.modelSelect.disabled = streaming;
    e.messages.setAttribute('aria-busy', streaming ? 'true' : 'false');
    e.form.classList.toggle('streaming', streaming);
    this.onGenerationChange(streaming);
  }

  modelLabel(providerId, modelId) {
    const provider = this.providers.find((p) => p.id === providerId);
    return (provider ? provider.name : providerId || 'Unknown provider') + ' — ' + (modelId || 'No model');
  }

  modelKey(providerId, modelId) {
    return providerId + '/' + modelId;
  }

  renderModelSelect() {
    const select = this.elements.modelSelect;
    const previous = this.selected;
    select.replaceChildren();
    let count = 0;
    for (const provider of this.providers) {
      if (!provider.configured) continue;
      const models = (provider.models || []).filter((m) => !this.hidden.has(this.modelKey(provider.id, m.id)));
      if (models.length === 0) continue;
      const group = document.createElement('optgroup');
      group.label = provider.name;
      for (const model of models) {
        const option = document.createElement('option');
        option.value = JSON.stringify([provider.id, model.id]);
        option.textContent = model.name || model.id;
        group.appendChild(option);
        count++;
      }
      select.appendChild(group);
    }

    const currentKey = previous.providerId + '/' + previous.modelId;
    const hasCurrent = Array.from(select.options).some((option) => {
      try {
        const [p, m] = JSON.parse(option.value);
        return p + '/' + m === currentKey;
      } catch {
        return false;
      }
    });
    if (previous.providerId && previous.modelId && !hasCurrent) {
      const option = document.createElement('option');
      option.value = JSON.stringify([previous.providerId, previous.modelId]);
      option.textContent = this.modelLabel(previous.providerId, previous.modelId) + ' (unavailable)';
      select.insertBefore(option, select.firstChild);
      count++;
    }

    if (count === 0) {
      const option = document.createElement('option');
      option.value = '';
      option.textContent = 'No models available';
      option.disabled = true;
      select.appendChild(option);
      select.disabled = true;
    } else {
      select.disabled = !!this.stream;
      if (previous.providerId && previous.modelId) {
        select.value = JSON.stringify([previous.providerId, previous.modelId]);
      }
    }
  }

  async modelChanged() {
    if (this.stream) return;
    let value;
    try {
      value = JSON.parse(this.elements.modelSelect.value);
    } catch {
      return;
    }
    if (!Array.isArray(value) || value.length !== 2) return;
    const next = { providerId: String(value[0]), modelId: String(value[1]) };
    const previous = { ...this.selected };
    this.selected = next;
    if (!this.chat) return;
    if (this.chat.provider_id === next.providerId && this.chat.model_id === next.modelId) return;
    try {
      const result = await this.api.patchChat(this.chat.id, {
        provider_id: next.providerId,
        model_id: next.modelId,
      });
      this.chat = result.chat;
      this.onChatMeta(this.chat);
      showToast('Model updated for this chat.', 'info', 2500);
    } catch (error) {
      this.selected = previous;
      this.renderModelSelect();
      showToast(error.message || 'Could not change the model.', 'error');
    }
  }

  async submit() {
    if (this.stream) return;
    const content = this.elements.input.value.trim();
    if (!content) return;
    if (!this.chat) {
      try {
        const chat = await this.ensureChat();
        if (!chat) return;
        this.adoptChat(chat);
      } catch (error) {
        showToast(error.message || 'Could not create a chat.', 'error');
        return;
      }
    }
    const payload = { content };
    if (this.selected.providerId && this.selected.modelId) {
      payload.provider_id = this.selected.providerId;
      payload.model_id = this.selected.modelId;
    }
    await this.runGeneration(payload, { newContent: content });
  }

  async regenerate() {
    if (this.stream || !this.chat) return;
    await this.runGeneration({ regenerate: true }, { regenerate: true });
  }

  async saveEdit(message, content) {
    if (this.stream || !this.chat) return;
    const value = content.trim();
    if (!value) return;
    await this.runGeneration({ content: value, edit_message_id: message.id }, { editId: message.id, editContent: value });
  }

  async runGeneration(payload, options = {}) {
    if (this.stream || !this.chat) return;
    const seq = ++this.generationSeq;
    const chatId = this.chat.id;
    const controller = new AbortController();
    const now = new Date().toISOString();
    let optimisticUserId = null;
    let restoreInput = null;

    if (options.newContent !== undefined) {
      optimisticUserId = 'temp-user-' + seq;
      this.messages.push({
        id: optimisticUserId,
        role: 'user',
        content: options.newContent,
        status: 'completed',
        error: '',
        created_at: now,
      });
      restoreInput = options.newContent;
      this.elements.input.value = '';
      autoGrow(this.elements.input);
      this.updateSendState();
    } else if (options.editId !== undefined) {
      const index = this.messages.findIndex((m) => String(m.id) === String(options.editId));
      if (index === -1) return;
      this.messages = this.messages.slice(0, index + 1);
      this.messages[index] = {
        ...this.messages[index],
        content: options.editContent,
        status: 'completed',
        error: '',
      };
    } else if (options.regenerate) {
      while (this.messages.length && this.messages[this.messages.length - 1].role === 'assistant') {
        this.messages.pop();
      }
      if (!this.messages.some((m) => m.role === 'user')) return;
    }

    const assistant = {
      id: 'temp-assistant-' + seq,
      role: 'assistant',
      content: '',
      status: 'streaming',
      error: '',
      created_at: now,
    };
    this.messages.push(assistant);
    const stream = {
      seq,
      chatId,
      controller,
      assistant,
      receivedMeta: false,
      finished: false,
      stoppedByUser: false,
    };
    this.stream = stream;
    this.setStreamingUI(true);
    this.renderAll(true);

    const onEvent = (event, data) => {
      if (this.stream !== stream || seq !== this.generationSeq) return;
      if (event === 'meta') {
        stream.receivedMeta = true;
        assistant.id = data.assistant_message_id;
        if (data.title && this.chat) {
          this.chat.title = data.title;
          this.updateTitle();
          this.onChatMeta(this.chat);
        }
        this.renderAll(false);
      } else if (event === 'delta') {
        if (typeof data.text !== 'string') return;
        assistant.content += data.text;
        this.scheduleAssistantUpdate(assistant, seq);
      } else if (event === 'done') {
        stream.finished = true;
        assistant.status = 'completed';
        assistant.error = '';
        if (data.title && this.chat) {
          this.chat.title = data.title;
          this.updateTitle();
          this.onChatMeta(this.chat);
        }
      } else if (event === 'error') {
        stream.finished = true;
        assistant.status = data.status === 'cancelled' ? 'cancelled' : 'error';
        assistant.error = data.message || 'The response failed.';
        if (data.message_id) assistant.id = data.message_id;
        if (assistant.status !== 'cancelled') {
          showToast(assistant.error, 'error');
        }
        this.scheduleAssistantUpdate(assistant, seq);
      }
    };

    try {
      await this.api.sendMessage(chatId, payload, { signal: controller.signal, onEvent });
      if (!stream.finished && !controller.signal.aborted) {
        assistant.status = 'error';
        assistant.error = 'The response stream ended unexpectedly. The saved reply may be incomplete.';
        showToast(assistant.error, 'error');
      }
    } catch (error) {
      if (error.name === 'AbortError') {
        assistant.status = 'cancelled';
        assistant.error = 'Generation stopped.';
      } else {
        const message = error.message || 'The response failed.';
        if (!stream.receivedMeta) {
          this.messages = this.messages.filter((m) => m !== assistant);
          if (optimisticUserId) {
            this.messages = this.messages.filter((m) => m.id !== optimisticUserId);
          }
          if (restoreInput !== null && this.elements.input.value === '') {
            this.elements.input.value = restoreInput;
            autoGrow(this.elements.input);
            this.updateSendState();
          }
        } else {
          assistant.status = 'error';
          assistant.error = message;
        }
        showToast(message, 'error');
      }
    } finally {
      if (seq !== this.generationSeq || !this.chat || this.chat.id !== chatId) {
        return;
      }
      this.stream = null;
      this.setStreamingUI(false);
      if (this.updateTimer) {
        clearTimeout(this.updateTimer);
        this.updateTimer = null;
      }
      await this.reconcile(chatId, seq, restoreInput);
    }
  }

  async reconcile(chatId, seq, restoreInput) {
    let detail = null;
    try {
      detail = await this.api.getChat(chatId);
      const last = detail.messages[detail.messages.length - 1];
      if (last && last.role === 'assistant' && last.status === 'streaming') {
        await new Promise((resolve) => setTimeout(resolve, 350));
        detail = await this.api.getChat(chatId);
      }
    } catch {
      detail = null;
    }
    if (seq !== this.generationSeq || !this.chat || this.chat.id !== chatId) return;
    if (detail) {
      this.chat = detail.chat;
      this.messages = detail.messages;
      if (detail.chat.provider_id && detail.chat.model_id) {
        this.selected = { providerId: detail.chat.provider_id, modelId: detail.chat.model_id };
      }
      this.updateTitle();
      this.renderModelSelect();
      this.renderAll(false);
      this.onChatMeta(detail.chat);
      if (restoreInput !== null) {
        const lastUser = [...this.messages].reverse().find((m) => m.role === 'user');
        if (lastUser && lastUser.content === restoreInput && this.elements.input.value === restoreInput) {
          this.elements.input.value = '';
          autoGrow(this.elements.input);
          this.updateSendState();
        }
      }
    } else {
      this.renderAll(false);
      this.updateSendState();
      if (restoreInput !== null && this.elements.input.value === '') {
        this.elements.input.value = restoreInput;
        autoGrow(this.elements.input);
        this.updateSendState();
      }
    }
  }

  stop() {
    if (!this.stream) return;
    this.stream.stoppedByUser = true;
    try { this.stream.controller.abort(); } catch { /* already closed */ }
    this.elements.stop.disabled = true;
    this.elements.stop.textContent = 'Stopping…';
  }

  scheduleAssistantUpdate(assistant, seq) {
    if (this.updateTimer) return;
    this.updateTimer = setTimeout(() => {
      this.updateTimer = null;
      if (seq !== this.generationSeq || this.stream?.assistant !== assistant) return;
      const near = this.isNearBottom();
      this.updateAssistantElement(assistant);
      if (near) this.scrollToBottom();
    }, 70);
  }

  isNearBottom() {
    const el = this.elements.conversation;
    return el.scrollHeight - el.scrollTop - el.clientHeight < 140;
  }

  scrollToBottom() {
    const el = this.elements.conversation;
    el.scrollTop = el.scrollHeight;
  }

  renderAll(forceScroll) {
    const near = forceScroll || this.isNearBottom();
    this.elements.messages.replaceChildren();
    this.messageEls.clear();
    const fragment = document.createDocumentFragment();
    this.messages.forEach((message, index) => {
      const node = this.createMessageElement(message, index);
      fragment.appendChild(node.root);
      this.messageEls.set(String(message.id), node);
    });
    this.elements.messages.appendChild(fragment);
    const empty = this.messages.length === 0;
    this.elements.empty.hidden = !empty;
    this.elements.messages.hidden = empty;
    if (near) this.scrollToBottom();
  }

  createMessageElement(message, index) {
    const article = document.createElement('article');
    article.className = 'message message-' + message.role;
    article.dataset.messageId = String(message.id);
    if (message.status === 'streaming') article.classList.add('streaming');

    const head = document.createElement('div');
    head.className = 'message-head';
    const avatar = document.createElement('span');
    avatar.className = 'avatar';
    avatar.textContent = message.role === 'assistant' ? 'AI' : 'You';
    const role = document.createElement('span');
    role.className = 'message-role';
    role.textContent = message.role === 'assistant' ? 'libreAI' : 'You';
    const time = document.createElement('span');
    time.className = 'message-time';
    time.textContent = formatTimestamp(message.created_at);
    head.append(avatar, role, time);
    article.appendChild(head);

    const body = document.createElement('div');
    body.className = 'message-body markdown';
    body.innerHTML = this.bodyHTML(message);
    article.appendChild(body);

    const status = document.createElement('div');
    status.className = 'message-status';
    this.fillStatus(status, message);
    if (status.childNodes.length) article.appendChild(status);

    const actions = document.createElement('div');
    actions.className = 'message-actions';
    const copy = makeButton('message-action', 'Copy', 'copy');
    copy.dataset.action = 'copy-message';
    copy.setAttribute('aria-label', 'Copy message');
    actions.appendChild(copy);

    if (message.role === 'user' && message.status === 'completed' && !this.stream) {
      const edit = makeButton('message-action', 'Edit', 'edit');
      edit.dataset.action = 'edit-message';
      edit.setAttribute('aria-label', 'Edit and resend message');
      actions.appendChild(edit);
    }
    const isLast = index === this.messages.length - 1;
    if (message.role === 'assistant' && isLast && message.status !== 'streaming' && !this.stream) {
      const regen = makeButton('message-action', 'Regenerate', 'refresh');
      regen.dataset.action = 'regenerate';
      regen.setAttribute('aria-label', 'Regenerate response');
      actions.appendChild(regen);
    }
    article.appendChild(actions);

    return { root: article, body, status, message };
  }

  bodyHTML(message) {
    if (message.status === 'streaming' && !message.content) {
      return '<div class="thinking" aria-label="Waiting for the provider"><span></span><span></span><span></span><em>Thinking</em></div>';
    }
    if (!message.content) {
      return '<p class="empty-message">No content.</p>';
    }
    return renderMarkdown(message.content);
  }

  fillStatus(element, message) {
    element.replaceChildren();
    if (message.status === 'streaming' || message.status === 'completed') return;
    const label = document.createElement('strong');
    const text = document.createElement('span');
    if (message.status === 'cancelled') {
      label.textContent = 'Stopped';
      text.textContent = 'Generation was stopped.';
      element.classList.add('status-cancelled');
    } else if (message.status === 'interrupted') {
      label.textContent = 'Interrupted';
      text.textContent = message.error || 'The server stopped before this response finished.';
      element.classList.add('status-interrupted');
    } else {
      label.textContent = 'Response failed';
      text.textContent = message.error || 'The provider request failed.';
      element.classList.add('status-error');
    }
    element.append(label, document.createTextNode(' '), text);
  }

  updateAssistantElement(message) {
    const node = this.messageEls.get(String(message.id));
    if (!node) {
      this.renderAll(false);
      return;
    }
    const near = this.isNearBottom();
    node.body.innerHTML = this.bodyHTML(message);
    node.root.classList.toggle('streaming', message.status === 'streaming');
    this.fillStatus(node.status, message);
    if (node.status.childNodes.length && !node.status.isConnected) {
      node.root.insertBefore(node.status, node.root.querySelector('.message-actions'));
    } else if (!node.status.childNodes.length && node.status.isConnected) {
      node.status.remove();
    }
    if (near) this.scrollToBottom();
  }

  async messageAction(event) {
    const copyCode = event.target.closest('[data-copy-code]');
    if (copyCode) {
      const code = copyCode.closest('.code-block')?.querySelector('code');
      if (!code) return;
      try {
        await copyText(code.textContent);
        flashButton(copyCode, 'Copied');
      } catch {
        showToast('Could not copy code.', 'error');
      }
      return;
    }
    const button = event.target.closest('[data-action]');
    if (!button) return;
    const article = button.closest('.message');
    if (!article) return;
    const message = this.messages.find((m) => String(m.id) === article.dataset.messageId);
    if (!message) return;
    const action = button.dataset.action;
    if (action === 'copy-message') {
      try {
        await copyText(message.content);
        flashButton(button, 'Copied');
      } catch {
        showToast('Could not copy the message.', 'error');
      }
    } else if (action === 'edit-message') {
      this.beginEdit(message);
    } else if (action === 'regenerate') {
      await this.regenerate();
    }
  }

  beginEdit(message) {
    if (this.stream) return;
    const node = this.messageEls.get(String(message.id));
    if (!node) return;
    const wrapper = document.createElement('div');
    wrapper.className = 'edit-wrapper';
    const area = document.createElement('textarea');
    area.className = 'edit-area';
    area.value = message.content;
    area.rows = 3;
    area.setAttribute('aria-label', 'Edit message');
    wrapper.appendChild(area);

    const actions = document.createElement('div');
    actions.className = 'edit-actions';
    const save = makeButton('btn primary small', 'Save & resend', null);
    const cancel = makeButton('btn ghost small', 'Cancel', null);
    actions.append(save, cancel);
    wrapper.appendChild(actions);

    node.body.replaceChildren(wrapper);
    node.root.querySelector('.message-actions')?.remove();
    autoGrow(area, 320);
    area.focus();
    area.setSelectionRange(area.value.length, area.value.length);
    area.addEventListener('input', () => autoGrow(area, 320));

    const close = () => this.renderAll(false);
    cancel.addEventListener('click', close);
    area.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') close();
      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        save.click();
      }
    });
    save.addEventListener('click', async () => {
      const value = area.value.trim();
      if (!value) {
        area.focus();
        return;
      }
      save.disabled = true;
      cancel.disabled = true;
      await this.saveEdit(message, value);
    });
  }
}
