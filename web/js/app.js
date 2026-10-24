/** libreAI application shell: bootstrap, navigation, sidebar, settings, theme. */

import { api } from './api.js';
import { ChatView } from './chat.js';
import { Sidebar } from './sidebar.js';
import { $, confirmDialog, downloadURL, openMenu, promptDialog, showToast } from './ui.js';

const THEME_KEY = 'libreai-theme';
const SIDEBAR_KEY = 'libreai-sidebar-collapsed';

const state = {
  version: 'dev',
  retentionDays: 30,
  providers: [],
  projects: [],
  chats: [],
  defaults: { providerId: '', modelId: '' },
  hidden: new Set(),
  currentChat: null,
};

const els = {
  app: $('#app'),
  sidebar: $('#sidebar'),
  backdrop: $('#backdrop'),
  sidebarOpen: $('#sidebar-open'),
  sidebarClose: $('#sidebar-close'),
  search: $('#chat-search'),
  newChat: $('#new-chat'),
  newProject: $('#new-project'),
  projectList: $('#project-list'),
  chatList: $('#chat-list'),
  chatEmpty: $('#chat-empty'),
  settingsButton: $('#sidebar-settings'),
  themeButton: $('#theme-toggle'),
  title: $('#chat-title'),
  chatMenuButton: $('#chat-menu-button'),
  modelSelect: $('#model-select'),
  conversation: $('#conversation'),
  messages: $('#messages'),
  empty: $('#empty-state'),
  providerHint: $('#provider-hint'),
  composerForm: $('#composer-form'),
  composer: $('#composer-input'),
  send: $('#send-button'),
  stop: $('#stop-button'),
  retentionNote: $('#retention-note'),
  emptyRetentionNote: $('#empty-retention-note'),
  bootError: $('#boot-error'),
  bootErrorMessage: $('#boot-error-message'),
  retryBoot: $('#retry-boot'),
  settingsDialog: $('#settings-dialog'),
  defaultProvider: $('#default-provider'),
  defaultModel: $('#default-model'),
  refreshModels: $('#refresh-models'),
  refreshStatus: $('#model-refresh-status'),
  modelVisibility: $('#model-visibility'),
  providerStatus: $('#provider-status'),
  settingsRetention: $('#settings-retention'),
  appVersion: $('#app-version'),
  saveSettings: $('#save-settings'),
  themeChoices: document.querySelectorAll('input[name="theme-choice"]'),
  moveDialog: $('#move-dialog'),
  moveForm: $('#move-form'),
  moveTitle: $('#move-chat-title'),
  moveProject: $('#move-project'),
};

const chatView = new ChatView(
  {
    conversation: els.conversation,
    messages: els.messages,
    empty: els.empty,
    form: els.composerForm,
    input: els.composer,
    send: els.send,
    stop: els.stop,
    modelSelect: els.modelSelect,
    title: els.title,
  },
  {
    api,
    ensureChat: () => ensureChat(),
    onChatMeta: (chat) => handleChatMeta(chat),
    onGenerationChange: (streaming) => {
      els.chatMenuButton.disabled = streaming;
    },
  },
);

const sidebar = new Sidebar(
  {
    newChatBtn: els.newChat,
    newProjectBtn: els.newProject,
    settingsBtn: els.settingsButton,
    themeBtn: els.themeButton,
    search: els.search,
    projectList: els.projectList,
    chatList: els.chatList,
    empty: els.chatEmpty,
  },
  {
    onNewChat: (projectId) => newChat(projectId),
    onOpenChat: (chat) => openChat(chat.id),
    onChatMenu: (chat, anchor) => showChatMenu(chat, anchor),
    onProjectMenu: (project, anchor) => showProjectMenu(project, anchor),
    onNewProject: () => createProject(),
    onSettings: () => openSettings(),
    onThemeToggle: () => toggleTheme(),
  },
);

let settingsRendered = false;
const mobileQuery = window.matchMedia('(max-width: 820px)');

initTheme();
wireUI();
restoreSidebar();
boot();

// ---------------------------------------------------------------------------
// Bootstrap and navigation

async function boot() {
  els.bootError.hidden = true;
  try {
    const data = await api.bootstrap();
    applyBootstrap(data);
    await routeFromLocation(false);
    kickModelDiscovery();
  } catch (error) {
    showBootError(error);
  }
}

function applyBootstrap(data) {
  state.version = data.version || 'dev';
  state.retentionDays = data.retention_days || 30;
  state.providers = data.providers || [];
  state.projects = data.projects || [];
  state.chats = data.chats || [];
  state.defaults = {
    providerId: data.defaults?.provider_id || '',
    modelId: data.defaults?.model_id || '',
  };
  state.hidden = new Set(data.hidden_models || []);
  els.retentionNote.textContent = String(state.retentionDays);
  els.emptyRetentionNote.textContent = String(state.retentionDays);
  els.settingsRetention.textContent = String(state.retentionDays);
  els.appVersion.textContent = state.version;
  els.providerHint.hidden = hasUsableProvider();
  sidebar.setData(state.projects, state.chats);
  chatView.setProviders(state.providers, state.hidden);
  chatView.setDefaults(state.defaults.providerId, state.defaults.modelId);
  updateDocumentTitle();
}

function hasUsableProvider() {
  return state.providers.some((provider) => provider.configured && (provider.models || []).length > 0);
}

function kickModelDiscovery() {
  // One opportunistic refresh of the server's in-memory discovery cache. This
  // is not polling; the Settings panel has an explicit refresh action.
  setTimeout(async () => {
    try {
      const data = await api.models();
      updateProviders(data.providers || []);
    } catch {
      // Discovery is optional; manual models and the settings refresh remain.
    }
  }, 1800);
}

async function routeFromLocation(push) {
  const id = chatIDFromHash();
  if (!id) {
    if (location.hash) history.replaceState(null, '', location.pathname + location.search);
    showEmpty();
    return;
  }
  const known = state.chats.some((chat) => chat.id === id);
  if (!known) {
    history.replaceState(null, '', location.pathname + location.search);
    showEmpty();
    return;
  }
  await openChat(id, { push });
}

function chatIDFromHash() {
  const match = /^#chat=([a-f0-9]{32})$/.exec(location.hash || '');
  return match ? match[1] : null;
}

async function openChat(id, { push = true } = {}) {
  if (state.currentChat && state.currentChat.id === id) {
    closeSidebarAfterNavigation();
    return;
  }
  try {
    const detail = await api.openChat(id);
    upsertChat(detail.chat);
    state.currentChat = detail.chat;
    chatView.setChat(detail.chat, detail.messages);
    sidebar.setActive(id);
    if (push) history.pushState({ chat: id }, '', '#chat=' + id);
    updateDocumentTitle();
    els.chatMenuButton.hidden = false;
    closeSidebarAfterNavigation();
  } catch (error) {
    if (error.status === 404) {
      state.chats = state.chats.filter((chat) => chat.id !== id);
      sidebar.setData(state.projects, state.chats);
      showToast('That chat no longer exists.', 'error');
      showEmpty();
      history.replaceState(null, '', location.pathname + location.search);
      return;
    }
    showToast(error.message || 'Could not open chat.', 'error');
  }
}

function showEmpty() {
  state.currentChat = null;
  chatView.clear();
  sidebar.setActive(null);
  els.chatMenuButton.hidden = true;
  updateDocumentTitle();
}

async function ensureChat() {
  if (state.currentChat) return state.currentChat;
  const chat = await createChatRecord(null);
  state.currentChat = chat;
  chatView.adoptChat(chat);
  history.pushState({ chat: chat.id }, '', '#chat=' + chat.id);
  els.chatMenuButton.hidden = false;
  updateDocumentTitle();
  return chat;
}

async function newChat(projectId = null) {
  if (chatView.hasEmptyChat() && !projectId) {
    if (mobileQuery.matches) setSidebarOpen(false);
    chatView.focusComposer();
    return;
  }
  try {
    const chat = await createChatRecord(projectId);
    state.currentChat = chat;
    chatView.setChat(chat, []);
    history.pushState({ chat: chat.id }, '', '#chat=' + chat.id);
    sidebar.setActive(chat.id);
    els.chatMenuButton.hidden = false;
    updateDocumentTitle();
    closeSidebarAfterNavigation();
  } catch (error) {
    showToast(error.message || 'Could not create a chat.', 'error');
  }
}

async function createChatRecord(projectId) {
  const selection = chatView.getSelection();
  const providerId = selection.providerId || state.defaults.providerId;
  const modelId = selection.modelId || state.defaults.modelId;
  const body = { project_id: projectId || null };
  if (providerId && modelId) {
    body.provider_id = providerId;
    body.model_id = modelId;
  }
  const result = await api.createChat(body);
  upsertChat(result.chat);
  sidebar.setData(state.projects, state.chats);
  return result.chat;
}

function upsertChat(chat) {
  const index = state.chats.findIndex((item) => item.id === chat.id);
  if (index === -1) state.chats.unshift(chat);
  else state.chats[index] = { ...state.chats[index], ...chat };
  state.chats.sort((a, b) => new Date(b.last_activity_at) - new Date(a.last_activity_at));
  if (state.currentChat && state.currentChat.id === chat.id) state.currentChat = chat;
}

function handleChatMeta(chat) {
  upsertChat(chat);
  sidebar.setData(state.projects, state.chats);
  sidebar.setActive(chat.id);
  updateDocumentTitle();
}

function updateDocumentTitle() {
  document.title = state.currentChat ? state.currentChat.title + ' · libreAI' : 'libreAI';
}

function updateProviders(providers) {
  state.providers = providers;
  chatView.setProviders(state.providers, state.hidden);
  els.providerHint.hidden = hasUsableProvider();
  if (!state.defaults.providerId) {
    const first = firstVisibleModel();
    if (first) {
      state.defaults = first;
      chatView.setDefaults(first.providerId, first.modelId);
    }
  }
  if (settingsRendered) renderSettingsContent();
}

function firstVisibleModel() {
  for (const provider of state.providers) {
    if (!provider.configured) continue;
    for (const model of provider.models || []) {
      if (!state.hidden.has(provider.id + '/' + model.id)) {
        return { providerId: provider.id, modelId: model.id };
      }
    }
  }
  return null;
}

// ---------------------------------------------------------------------------
// Sidebar menus and CRUD

function showChatMenu(chat, anchor) {
  openMenu(anchor, [
    { label: 'Rename', onSelect: () => renameChat(chat) },
    { label: 'Move to project', onSelect: () => moveChat(chat) },
    { separator: true },
    { label: 'Export Markdown', onSelect: () => downloadURL(api.exportURL(chat.id, 'markdown')) },
    { label: 'Export JSON', onSelect: () => downloadURL(api.exportURL(chat.id, 'json')) },
    { separator: true },
    { label: 'Delete chat', danger: true, onSelect: () => deleteChat(chat) },
  ]);
}

function showProjectMenu(project, anchor) {
  openMenu(anchor, [
    { label: 'New chat in project', onSelect: () => newChat(project.id) },
    { label: 'Rename project', onSelect: () => renameProject(project) },
    { separator: true },
    { label: 'Delete project', danger: true, onSelect: () => deleteProject(project) },
  ]);
}

async function renameChat(chat) {
  const title = await promptDialog({ title: 'Rename chat', label: 'Chat title', value: chat.title, maxLength: 200 });
  if (!title || title === chat.title) return;
  try {
    const result = await api.patchChat(chat.id, { title });
    upsertChat(result.chat);
    sidebar.setData(state.projects, state.chats);
    sidebar.setActive(state.currentChat?.id || null);
    if (state.currentChat?.id === chat.id) chatView.applyChatMetadata(result.chat);
    updateDocumentTitle();
  } catch (error) {
    showToast(error.message || 'Could not rename chat.', 'error');
  }
}

async function deleteChat(chat) {
  const ok = await confirmDialog({
    title: 'Delete chat?',
    message: '“' + chat.title + '” and all of its messages will be permanently removed. This cannot be undone.',
    confirmLabel: 'Delete chat',
  });
  if (!ok) return;
  try {
    await api.deleteChat(chat.id);
    state.chats = state.chats.filter((item) => item.id !== chat.id);
    sidebar.setData(state.projects, state.chats);
    if (state.currentChat?.id === chat.id) {
      showEmpty();
      history.replaceState(null, '', location.pathname + location.search);
    }
    showToast('Chat deleted.', 'info', 2500);
  } catch (error) {
    showToast(error.message || 'Could not delete chat.', 'error');
  }
}

async function moveChat(chat) {
  const projectId = await showMoveDialog(chat);
  if (projectId === undefined) return;
  try {
    const result = await api.moveChat(chat.id, projectId);
    upsertChat(result.chat);
    sidebar.setData(state.projects, state.chats);
    sidebar.setActive(state.currentChat?.id || null);
    if (state.currentChat?.id === chat.id) chatView.applyChatMetadata(result.chat);
    showToast(projectId ? 'Chat moved.' : 'Chat moved to Chats.', 'info', 2500);
  } catch (error) {
    showToast(error.message || 'Could not move chat.', 'error');
  }
}

async function createProject() {
  const name = await promptDialog({ title: 'New project', label: 'Project name', maxLength: 100, confirmLabel: 'Create' });
  if (!name) return;
  try {
    const result = await api.createProject(name);
    state.projects.push(result.project);
    state.projects.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    sidebar.setData(state.projects, state.chats);
    showToast('Project created.', 'info', 2500);
  } catch (error) {
    showToast(error.message || 'Could not create project.', 'error');
  }
}

async function renameProject(project) {
  const name = await promptDialog({ title: 'Rename project', label: 'Project name', value: project.name, maxLength: 100 });
  if (!name || name === project.name) return;
  try {
    const result = await api.renameProject(project.id, name);
    state.projects = state.projects.map((item) => item.id === project.id ? result.project : item);
    state.projects.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    sidebar.setData(state.projects, state.chats);
  } catch (error) {
    showToast(error.message || 'Could not rename project.', 'error');
  }
}

async function deleteProject(project) {
  const count = state.chats.filter((chat) => chat.project_id === project.id).length;
  const ok = await confirmDialog({
    title: 'Delete project?',
    message: '“' + project.name + '” will be deleted. ' + (count ? count + ' chat(s) will move to Chats; no chats will be deleted.' : 'It contains no chats.'),
    confirmLabel: 'Delete project',
  });
  if (!ok) return;
  try {
    await api.deleteProject(project.id);
    state.projects = state.projects.filter((item) => item.id !== project.id);
    state.chats = state.chats.map((chat) => chat.project_id === project.id ? { ...chat, project_id: null } : chat);
    if (state.currentChat?.project_id === project.id) {
      const updated = { ...state.currentChat, project_id: null };
      state.currentChat = updated;
      chatView.applyChatMetadata(updated);
    }
    sidebar.setData(state.projects, state.chats);
    showToast('Project deleted. Its chats were moved to Chats.', 'info', 3500);
  } catch (error) {
    showToast(error.message || 'Could not delete project.', 'error');
  }
}

function showMoveDialog(chat) {
  const dialog = els.moveDialog;
  els.moveTitle.textContent = chat.title;
  els.moveProject.replaceChildren();
  const none = document.createElement('option');
  none.value = '';
  none.textContent = 'Chats (no project)';
  els.moveProject.appendChild(none);
  for (const project of state.projects) {
    const option = document.createElement('option');
    option.value = project.id;
    option.textContent = project.name;
    els.moveProject.appendChild(option);
  }
  els.moveProject.value = chat.project_id || '';
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      els.moveForm.onsubmit = null;
      dialog.querySelectorAll('[data-close]').forEach((button) => { button.onclick = null; });
      dialog.oncancel = null;
      resolve(value);
    };
    els.moveForm.onsubmit = (event) => {
      event.preventDefault();
      const value = els.moveProject.value;
      dialog.close();
      finish(value ? value : null);
    };
    dialog.querySelectorAll('[data-close]').forEach((button) => {
      button.onclick = () => {
        dialog.close();
        finish(undefined);
      };
    });
    dialog.oncancel = (event) => {
      event.preventDefault();
      dialog.close();
      finish(undefined);
    };
    dialog.showModal();
    els.moveProject.focus();
  });
}

// ---------------------------------------------------------------------------
// Topbar menu

function showCurrentChatMenu(anchor) {
  if (!state.currentChat) return;
  showChatMenu(state.currentChat, anchor);
}

// ---------------------------------------------------------------------------
// Settings

function openSettings() {
  renderSettingsContent();
  settingsRendered = true;
  els.settingsDialog.showModal();
}

function renderSettingsContent() {
  // Theme.
  const preference = themePreference();
  for (const radio of els.themeChoices) radio.checked = radio.value === preference;

  // Defaults.
  els.defaultProvider.replaceChildren();
  const noDefault = document.createElement('option');
  noDefault.value = '';
  noDefault.textContent = 'No default';
  els.defaultProvider.appendChild(noDefault);
  for (const provider of state.providers) {
    if (!provider.configured) continue;
    const option = document.createElement('option');
    option.value = provider.id;
    option.textContent = provider.name;
    els.defaultProvider.appendChild(option);
  }
  els.defaultProvider.value = state.defaults.providerId || '';
  renderDefaultModels(state.defaultProvider.value, state.defaults.modelId);

  // Model visibility.
  els.modelVisibility.replaceChildren();
  let visibleProviders = 0;
  for (const provider of state.providers) {
    if (!provider.configured || !(provider.models || []).length) continue;
    visibleProviders++;
    const details = document.createElement('details');
    details.className = 'settings-model-group';
    const summary = document.createElement('summary');
    const title = document.createElement('span');
    title.textContent = provider.name;
    const count = document.createElement('span');
    const hiddenCount = provider.models.filter((model) => state.hidden.has(provider.id + '/' + model.id)).length;
    count.className = 'muted-count';
    count.textContent = provider.models.length + ' models' + (hiddenCount ? ' · ' + hiddenCount + ' hidden' : '');
    summary.append(title, count);
    details.appendChild(summary);
    const list = document.createElement('div');
    list.className = 'visibility-list';
    for (const model of provider.models) {
      const key = provider.id + '/' + model.id;
      const label = document.createElement('label');
      label.className = 'visibility-row';
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = !state.hidden.has(key);
      checkbox.dataset.modelKey = key;
      const name = document.createElement('span');
      name.textContent = model.name || model.id;
      if (model.manual) {
        const badge = document.createElement('em');
        badge.textContent = 'manual';
        name.appendChild(document.createTextNode(' '));
        name.appendChild(badge);
      }
      label.append(checkbox, name);
      list.appendChild(label);
    }
    details.appendChild(list);
    els.modelVisibility.appendChild(details);
  }
  if (!visibleProviders) {
    const empty = document.createElement('p');
    empty.className = 'hint';
    empty.textContent = 'No models are available. Configure a provider in .env, restart libreAI, then refresh models.';
    els.modelVisibility.appendChild(empty);
  }

  // Provider status (never credentials or endpoints).
  els.providerStatus.replaceChildren();
  if (!state.providers.length) {
    const empty = document.createElement('p');
    empty.className = 'hint';
    empty.textContent = 'No providers are configured. Copy .env.example to .env, add a key, and restart libreAI.';
    els.providerStatus.appendChild(empty);
  }
  for (const provider of state.providers) {
    const row = document.createElement('div');
    row.className = 'provider-row';
    const dot = document.createElement('span');
    dot.className = 'provider-dot ' + (provider.configured ? 'ok' : 'warn');
    const text = document.createElement('div');
    const name = document.createElement('strong');
    name.textContent = provider.name;
    const status = document.createElement('span');
    status.textContent = providerStatusText(provider);
    text.append(name, status);
    if (provider.discovery_error) {
      const err = document.createElement('span');
      err.className = 'provider-error';
      err.textContent = 'Discovery: ' + provider.discovery_error;
      text.appendChild(err);
    }
    row.append(dot, text);
    els.providerStatus.appendChild(row);
  }

  els.settingsRetention.textContent = String(state.retentionDays);
  els.appVersion.textContent = state.version;
}

function renderDefaultModels(providerId, selectedModel) {
  els.defaultModel.replaceChildren();
  const provider = state.providers.find((item) => item.id === providerId);
  const models = provider ? provider.models || [] : [];
  if (!provider || models.length === 0) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = provider ? 'No models available' : 'Choose a provider first';
    els.defaultModel.appendChild(option);
    els.defaultModel.disabled = true;
    return;
  }
  els.defaultModel.disabled = false;
  for (const model of models) {
    const option = document.createElement('option');
    option.value = model.id;
    option.textContent = model.name || model.id;
    els.defaultModel.appendChild(option);
  }
  if (selectedModel && models.some((model) => model.id === selectedModel)) {
    els.defaultModel.value = selectedModel;
  }
}

function providerStatusText(provider) {
  const count = (provider.models || []).length;
  if (provider.status === 'ready') return 'Ready · ' + count + ' known model' + (count === 1 ? '' : 's');
  if (provider.status === 'disabled') return 'Disabled in .env';
  if (provider.status === 'api_key_missing') return 'API key missing in .env';
  if (provider.status === 'base_url_missing') return 'Base URL missing in .env';
  return provider.status || 'Unknown status';
}

async function saveSettings() {
  const providerId = els.defaultProvider.value;
  const modelId = providerId ? els.defaultModel.value : '';
  if (providerId && !modelId) {
    showToast('Choose a default model or choose “No default”.', 'error');
    return;
  }
  const checked = new Set();
  const rendered = new Set();
  els.modelVisibility.querySelectorAll('input[data-model-key]').forEach((checkbox) => {
    rendered.add(checkbox.dataset.modelKey);
    if (checkbox.checked) checked.add(checkbox.dataset.modelKey);
  });
  const hidden = new Set(Array.from(state.hidden).filter((key) => !rendered.has(key)));
  for (const key of rendered) if (!checked.has(key)) hidden.add(key);

  els.saveSettings.disabled = true;
  try {
    const result = await api.saveSettings({
      defaults: { provider_id: providerId, model_id: modelId },
      hidden_models: Array.from(hidden),
    });
    state.defaults = {
      providerId: result.defaults?.provider_id || '',
      modelId: result.defaults?.model_id || '',
    };
    state.hidden = new Set(result.hidden_models || []);
    chatView.setProviders(state.providers, state.hidden);
    chatView.setDefaults(state.defaults.providerId, state.defaults.modelId);
    if (!state.currentChat) chatView.setSelection(state.defaults.providerId, state.defaults.modelId);
    els.settingsDialog.close();
    showToast('Settings saved.', 'info', 2500);
  } catch (error) {
    showToast(error.message || 'Could not save settings.', 'error');
  } finally {
    els.saveSettings.disabled = false;
  }
}

async function refreshModels() {
  els.refreshModels.disabled = true;
  els.refreshStatus.textContent = 'Refreshing…';
  try {
    const data = await api.refreshModels();
    updateProviders(data.providers || []);
    els.refreshStatus.textContent = 'Model lists updated.';
    showToast('Model discovery refreshed.', 'info', 2600);
  } catch (error) {
    els.refreshStatus.textContent = '';
    showToast(error.message || 'Could not refresh models.', 'error');
  } finally {
    els.refreshModels.disabled = false;
  }
}

// ---------------------------------------------------------------------------
// Theme and layout

function themePreference() {
  try {
    const value = localStorage.getItem(THEME_KEY);
    if (value === 'dark' || value === 'light' || value === 'system') return value;
  } catch {
    // Ignore storage errors.
  }
  return 'system';
}

function resolvedTheme(preference) {
  if (preference === 'dark' || preference === 'light') return preference;
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function applyTheme(preference, { persist = true } = {}) {
  const resolved = resolvedTheme(preference);
  document.documentElement.dataset.theme = resolved === 'light' ? 'latte' : 'mocha';
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', resolved === 'light' ? '#eff1f5' : '#1e1e2e');
  const use = els.themeButton?.querySelector('use');
  if (use) use.setAttribute('href', resolved === 'light' ? '#icon-moon' : '#icon-sun');
  if (els.themeButton) {
    els.themeButton.setAttribute('aria-label', 'Switch to ' + (resolved === 'light' ? 'dark' : 'light') + ' theme');
    els.themeButton.title = 'Switch to ' + (resolved === 'light' ? 'dark' : 'light') + ' theme';
  }
  for (const radio of els.themeChoices) radio.checked = radio.value === preference;
  if (persist) {
    try { localStorage.setItem(THEME_KEY, preference); } catch { /* cosmetic */ }
  }
}

function initTheme() {
  applyTheme(themePreference(), { persist: false });
  const query = window.matchMedia('(prefers-color-scheme: light)');
  const onChange = () => {
    if (themePreference() === 'system') applyTheme('system', { persist: false });
  };
  if (query.addEventListener) query.addEventListener('change', onChange);
}

function toggleTheme() {
  const current = resolvedTheme(themePreference());
  applyTheme(current === 'dark' ? 'light' : 'dark');
  if (settingsRendered) {
    for (const radio of els.themeChoices) radio.checked = radio.value === themePreference();
  }
}

function isMobile() {
  return mobileQuery.matches;
}

function restoreSidebar() {
  let collapsed = false;
  try { collapsed = localStorage.getItem(SIDEBAR_KEY) === '1'; } catch { /* cosmetic */ }
  if (!isMobile()) els.app.classList.toggle('collapsed', collapsed);
}

function setSidebarOpen(open) {
  if (isMobile()) {
    els.app.classList.toggle('sidebar-open', open);
    els.backdrop.hidden = !open;
  } else {
    els.app.classList.toggle('collapsed', !open);
    try { localStorage.setItem(SIDEBAR_KEY, open ? '0' : '1'); } catch { /* cosmetic */ }
  }
}

function closeSidebarAfterNavigation() {
  if (isMobile()) setSidebarOpen(false);
}

function showBootError(error) {
  els.bootError.hidden = false;
  els.bootErrorMessage.textContent = error?.message || 'The libreAI server could not be reached.';
  els.bootError.querySelector('button')?.focus();
}

// ---------------------------------------------------------------------------
// Wiring

function wireUI() {
  els.sidebarOpen.addEventListener('click', () => {
    if (isMobile()) setSidebarOpen(true);
    else setSidebarOpen(els.app.classList.contains('collapsed'));
  });
  els.sidebarClose.addEventListener('click', () => setSidebarOpen(false));
  els.backdrop.addEventListener('click', () => setSidebarOpen(false));
  els.chatMenuButton.addEventListener('click', () => showCurrentChatMenu(els.chatMenuButton));
  els.retryBoot.addEventListener('click', () => boot());

  els.saveSettings.addEventListener('click', saveSettings);
  els.refreshModels.addEventListener('click', refreshModels);
  els.defaultProvider.addEventListener('change', () => {
    renderDefaultModels(els.defaultProvider.value, '');
  });
  for (const radio of els.themeChoices) {
    radio.addEventListener('change', () => {
      if (radio.checked) applyTheme(radio.value);
    });
  }
  els.settingsDialog.querySelectorAll('[data-close]').forEach((button) => {
    button.addEventListener('click', () => els.settingsDialog.close());
  });
  els.settingsDialog.addEventListener('click', (event) => {
    if (event.target === els.settingsDialog) els.settingsDialog.close();
  });

  window.addEventListener('popstate', () => {
    routeFromLocation(false);
  });
  mobileQuery.addEventListener('change', () => {
    els.app.classList.remove('sidebar-open');
    els.backdrop.hidden = true;
    if (isMobile()) els.app.classList.remove('collapsed');
  });
}
