/** libreAI application shell: bootstrap, navigation, sidebar, settings, theme. */

import { api } from './api.js';
import { ChatView } from './chat.js';
import { Sidebar } from './sidebar.js';
import { $, confirmDialog, downloadURL, icon, openMenu, promptDialog, showToast } from './ui.js';

const THEME_KEY = 'libreai-theme';
const SIDEBAR_KEY = 'libreai-sidebar-collapsed';
const TABS_KEY = 'libreai-open-tabs';
const MESH_KEY = 'libreai-mesh';

const state = {
  version: 'dev',
  retentionDays: 30,
  providers: [],
  projects: [],
  chats: [],
  defaults: { providerId: '', modelId: '' },
  hidden: new Set(),
  currentChat: null,
  openTabs: [],
  activeTab: null,
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
  chatTabs: $('#chat-tabs'),
  newTabButton: $('#chat-new-tab'),
  chatMenuButton: $('#chat-menu-button'),
  modelSelect: $('#model-select'),
  conversation: $('#conversation'),
  messages: $('#messages'),
  empty: $('#empty-state'),
  main: document.querySelector('.main'),
  providerHint: $('#provider-hint'),
  composerForm: $('#composer-form'),
  composer: $('#composer-input'),
  send: $('#send-button'),
  stop: $('#stop-button'),
  searchToggle: $('#web-search-toggle'),
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
  envEditor: $('#env-editor'),
  envPath: $('#env-path'),
  envSave: $('#env-save'),
  envReload: $('#env-reload'),
  envStatus: $('#env-status'),
  envSecretPlaceholder: $('#env-secret-placeholder'),
  themeChoices: document.querySelectorAll('input[name="theme-choice"]'),
  moveDialog: $('#move-dialog'),
  moveForm: $('#move-form'),
  moveTitle: $('#move-chat-title'),
  moveProject: $('#move-project'),
  meshBackground: $('#mesh-gradient'),
  meshPreview: $('#mesh-preview'),
  meshColor: $('#mesh-color'),
  meshBase: $('#mesh-base'),
  meshSize: $('#mesh-size'),
  meshSizeValue: $('#mesh-size-value'),
  meshPalette: $('#mesh-palette'),
  meshPresets: $('#mesh-presets'),
  meshAdd: $('#mesh-add'),
  meshRemove: $('#mesh-remove'),
  meshReset: $('#mesh-reset'),
  meshStatus: $('#mesh-status'),
};

const chatView = new ChatView(
  {
    conversation: els.conversation,
    messages: els.messages,
    empty: els.empty,
    main: els.main,
    form: els.composerForm,
    input: els.composer,
    send: els.send,
    stop: els.stop,
    searchToggle: els.searchToggle,
    modelSelect: els.modelSelect,
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

// ---------------------------------------------------------------------------
// Bootstrap and navigation

async function boot() {
  els.bootError.hidden = true;
  try {
    const data = await api.bootstrap();
    applyBootstrap(data);
    await restoreTabs();
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
  els.settingsRetention.textContent = String(state.retentionDays);
  els.appVersion.textContent = state.version;
  els.providerHint.hidden = hasUsableProvider();
  sidebar.setData(state.projects, state.chats);
  chatView.setProviders(state.providers, state.hidden);
  chatView.setDefaults(state.defaults.providerId, state.defaults.modelId);
  chatView.setSearchEnabled(!!(data.search && data.search.enabled));
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

async function restoreTabs() {
  const stored = loadOpenTabs();
  const known = new Set(state.chats.map((chat) => chat.id));
  state.openTabs = Array.from(new Set(stored.tabs.filter((id) => known.has(id))));
  let active = stored.active && state.openTabs.includes(stored.active) ? stored.active : null;
  if (!active && state.openTabs.length) active = state.openTabs[state.openTabs.length - 1];

  const hashId = chatIDFromHash();
  if (hashId && known.has(hashId)) {
    if (!state.openTabs.includes(hashId)) state.openTabs.push(hashId);
    active = hashId;
  } else if (location.hash) {
    history.replaceState(null, '', location.pathname + location.search);
  }

  state.activeTab = active;
  renderTabs();
  saveTabs();
  if (active) {
    await openChat(active, { push: false });
    history.replaceState({ chat: active }, '', '#chat=' + active);
  } else {
    showEmpty();
  }
}

function loadOpenTabs() {
  try {
    const raw = JSON.parse(localStorage.getItem(TABS_KEY) || 'null');
    if (raw && Array.isArray(raw.tabs)) {
      return {
        tabs: raw.tabs.filter((id) => typeof id === 'string'),
        active: typeof raw.active === 'string' ? raw.active : null,
      };
    }
  } catch {
    // Ignore storage errors; tabs can start empty.
  }
  return { tabs: [], active: null };
}

function saveTabs() {
  try {
    localStorage.setItem(TABS_KEY, JSON.stringify({ tabs: state.openTabs, active: state.activeTab }));
  } catch {
    // Tab persistence is cosmetic.
  }
}

function renderTabs() {
  const fragment = document.createDocumentFragment();
  for (const id of state.openTabs) {
    const chat = state.chats.find((item) => item.id === id);
    if (!chat) continue;
    const tab = document.createElement('div');
    tab.className = 'chat-tab' + (id === state.activeTab ? ' active' : '');
    tab.setAttribute('role', 'presentation');

    const label = document.createElement('button');
    label.type = 'button';
    label.className = 'chat-tab-label';
    label.dataset.chatTab = id;
    label.setAttribute('role', 'tab');
    label.setAttribute('aria-selected', id === state.activeTab ? 'true' : 'false');
    label.title = chat.title;
    label.textContent = chat.title;

    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'chat-tab-close';
    close.dataset.chatTabClose = id;
    close.setAttribute('aria-label', 'Close ' + chat.title);
    close.appendChild(icon('close'));

    tab.append(label, close);
    fragment.appendChild(tab);
  }
  els.chatTabs.replaceChildren(fragment);
}

async function closeTab(id) {
  const index = state.openTabs.indexOf(id);
  if (index === -1) return;
  const wasActive = state.activeTab === id || state.currentChat?.id === id;
  state.openTabs.splice(index, 1);

  if (wasActive) {
    if (state.currentChat?.id === id) {
      state.currentChat = null;
      chatView.clear();
      sidebar.setActive(null);
      els.chatMenuButton.hidden = true;
    }
    state.activeTab = null;
    const next = state.openTabs[index] || state.openTabs[index - 1] || null;
    if (next) {
      await openChat(next, { push: false });
      history.replaceState({ chat: next }, '', '#chat=' + next);
    } else {
      renderTabs();
      saveTabs();
      showEmpty();
      history.replaceState(null, '', location.pathname + location.search);
    }
  } else {
    if (!state.activeTab || !state.openTabs.includes(state.activeTab)) {
      state.activeTab = state.openTabs[0] || null;
    }
    renderTabs();
    saveTabs();
  }
}

async function routeFromLocation(push) {
  const id = chatIDFromHash();
  const activeTab = () => {
    if (state.activeTab && state.openTabs.includes(state.activeTab)) return state.activeTab;
    return state.openTabs[state.openTabs.length - 1] || null;
  };
  if (!id) {
    if (location.hash) history.replaceState(null, '', location.pathname + location.search);
    const fallback = activeTab();
    if (fallback) await openChat(fallback, { push: false });
    else showEmpty();
    return;
  }
  const known = state.chats.some((chat) => chat.id === id);
  if (!known) {
    history.replaceState(null, '', location.pathname + location.search);
    const fallback = activeTab();
    if (fallback) await openChat(fallback, { push: false });
    else showEmpty();
    return;
  }
  await openChat(id, { push });
}

function chatIDFromHash() {
  const match = /^#chat=([a-f0-9]{32})$/.exec(location.hash || '');
  return match ? match[1] : null;
}

async function openChat(id, { push = true } = {}) {
  if (!state.openTabs.includes(id)) state.openTabs.push(id);
  state.activeTab = id;
  renderTabs();
  saveTabs();
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
      const index = state.openTabs.indexOf(id);
      if (index !== -1) state.openTabs.splice(index, 1);
      if (state.currentChat?.id === id) {
        state.currentChat = null;
        chatView.clear();
        sidebar.setActive(null);
        els.chatMenuButton.hidden = true;
      }
      if (state.activeTab === id) state.activeTab = null;
      const next = state.openTabs[index] || state.openTabs[index - 1] || null;
      if (next) {
        await openChat(next, { push: false });
        history.replaceState({ chat: next }, '', '#chat=' + next);
      } else {
        renderTabs();
        saveTabs();
        showEmpty();
        history.replaceState(null, '', location.pathname + location.search);
      }
      showToast('That chat no longer exists.', 'error');
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
  if (!state.openTabs.includes(chat.id)) state.openTabs.push(chat.id);
  state.activeTab = chat.id;
  state.currentChat = chat;
  chatView.adoptChat(chat);
  renderTabs();
  saveTabs();
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
    if (!state.openTabs.includes(chat.id)) state.openTabs.push(chat.id);
    state.activeTab = chat.id;
    state.currentChat = chat;
    chatView.setChat(chat, []);
    renderTabs();
    saveTabs();
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
  renderTabs();
  saveTabs();
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
    if (state.openTabs.includes(chat.id)) {
      if (state.currentChat?.id === chat.id) {
        state.currentChat = null;
        chatView.clear();
        sidebar.setActive(null);
        els.chatMenuButton.hidden = true;
      }
      await closeTab(chat.id);
    } else if (state.currentChat?.id === chat.id) {
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
  renderMeshEditor();
  settingsRendered = true;
  if (els.settingsDialog.open) {
    els.envEditor.focus();
    return;
  }
  els.settingsDialog.showModal();
  loadEnvEditor();
}

let envLoading = false;

async function loadEnvEditor() {
  if (envLoading) return;
  envLoading = true;
  els.envEditor.disabled = true;
  els.envSave.disabled = true;
  els.envReload.disabled = true;
  els.envPath.textContent = 'Loading configuration…';
  els.envStatus.textContent = '';
  try {
    const data = await api.envFile();
    els.envEditor.value = data.contents || '';
    els.envPath.textContent = data.path || '.env';
    els.envPath.title = data.path || '';
    if (data.secret_placeholder) els.envSecretPlaceholder.textContent = data.secret_placeholder;
    els.envEditor.disabled = false;
    els.envSave.disabled = false;
    els.envStatus.textContent = data.exists ? '' : 'The file does not exist yet; saving creates it.';
  } catch (error) {
    els.envEditor.value = '';
    els.envPath.textContent = 'Configuration editor unavailable';
    els.envPath.title = '';
    els.envStatus.textContent = error.message || 'Could not load the configuration file.';
    els.envEditor.disabled = true;
    els.envSave.disabled = true;
  } finally {
    els.envReload.disabled = false;
    envLoading = false;
  }
}

async function saveEnvFile() {
  if (els.envEditor.disabled) return;
  els.envSave.disabled = true;
  els.envReload.disabled = true;
  els.envStatus.textContent = 'Saving…';
  try {
    const result = await api.saveEnvFile(els.envEditor.value);
    if (result.providers) updateProviders(result.providers);
    await loadEnvEditor();
    if (result.restart_required) {
      els.envStatus.textContent = 'Saved. Restart libreAI to apply server options.';
      showToast('Configuration saved. Restart libreAI to apply server options.', 'info', 5000);
    } else {
      els.envStatus.textContent = 'Saved. Provider changes applied.';
      showToast('Configuration saved. Provider changes applied.', 'info', 3000);
    }
    if (Array.isArray(result.warnings) && result.warnings.length) {
      els.envStatus.textContent += ' ' + result.warnings.join(' ');
    }
    await refreshModels({ quiet: true });
  } catch (error) {
    els.envStatus.textContent = error.message || 'Could not save the configuration file.';
    showToast(error.message || 'Could not save the configuration file.', 'error');
  } finally {
    els.envSave.disabled = els.envEditor.disabled;
    els.envReload.disabled = false;
  }
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
  renderDefaultModels(els.defaultProvider.value, state.defaults.modelId);

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
    empty.textContent = 'No models are available. Configure a provider in .env, save it, then refresh models.';
    els.modelVisibility.appendChild(empty);
  }

  // Provider status (never credentials or endpoints).
  els.providerStatus.replaceChildren();
  if (!state.providers.length) {
    const empty = document.createElement('p');
    empty.className = 'hint';
    empty.textContent = 'No providers are configured. Open the .env editor above, add a provider, and save.';
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

async function refreshModels({ quiet = false } = {}) {
  els.refreshModels.disabled = true;
  els.refreshStatus.textContent = 'Refreshing…';
  try {
    const data = await api.refreshModels();
    updateProviders(data.providers || []);
    els.refreshStatus.textContent = 'Model lists updated.';
    if (!quiet) showToast('Model discovery refreshed.', 'info', 2600);
  } catch (error) {
    els.refreshStatus.textContent = '';
    if (!quiet) showToast(error.message || 'Could not refresh models.', 'error');
  } finally {
    els.refreshModels.disabled = false;
  }
}

// ---------------------------------------------------------------------------
// Theme and layout

function themePreference() {
  try {
    let value = localStorage.getItem(THEME_KEY);
    if (value === 'dark') value = 'graphite';
    if (value === 'light') value = 'latte';
    if (value === 'system' || value === 'graphite' || value === 'mocha' || value === 'latte') return value;
  } catch {
    // Ignore storage errors.
  }
  return 'graphite';
}

function resolvedTheme(preference) {
  if (preference === 'system') {
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'latte' : 'graphite';
  }
  return preference === 'latte' || preference === 'mocha' ? preference : 'graphite';
}

function themeColor(theme) {
  if (theme === 'latte') return '#eff1f5';
  if (theme === 'mocha') return '#1e1e2e';
  return '#242428';
}

function applyTheme(preference, { persist = true } = {}) {
  const resolved = resolvedTheme(preference);
  const light = resolved === 'latte';
  document.documentElement.dataset.theme = resolved;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', themeColor(resolved));
  const use = els.themeButton?.querySelector('use');
  if (use) use.setAttribute('href', light ? '#icon-moon' : '#icon-sun');
  if (els.themeButton) {
    els.themeButton.setAttribute('aria-label', 'Switch to ' + (light ? 'dark' : 'light') + ' theme');
    els.themeButton.title = 'Switch to ' + (light ? 'dark' : 'light') + ' theme';
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
  applyTheme(current === 'latte' ? 'graphite' : 'latte');
  if (settingsRendered) {
    for (const radio of els.themeChoices) radio.checked = radio.value === themePreference();
  }
}

// ---------------------------------------------------------------------------
// Mesh gradient

const DEFAULT_MESH = {
  base: '#141417',
  points: [
    { x: 16, y: 18, color: '#2c2c33', size: 68 },
    { x: 84, y: 14, color: '#1f2126', size: 62 },
    { x: 48, y: 88, color: '#26262c', size: 76 },
    { x: 88, y: 80, color: '#2b2433', size: 58 },
  ],
};

const MESH_NEW_COLORS = ['#89b4fa', '#f5c2e7', '#94e2d5', '#f9e2af', '#cba6f7', '#a6e3a1'];

const MESH_PALETTE = [
  { name: 'Mocha base', color: '#1e1e2e' },
  { name: 'Mocha surface', color: '#313244' },
  { name: 'Mocha overlay', color: '#45475a' },
  { name: 'Mocha text', color: '#cdd6f4' },
  { name: 'Mocha pink', color: '#f5c2e7' },
  { name: 'Mocha red', color: '#f38ba8' },
  { name: 'Mocha peach', color: '#fab387' },
  { name: 'Mocha yellow', color: '#f9e2af' },
  { name: 'Mocha green', color: '#a6e3a1' },
  { name: 'Mocha teal', color: '#94e2d5' },
  { name: 'Mocha blue', color: '#89b4fa' },
  { name: 'Mocha mauve', color: '#cba6f7' },
  { name: 'Latte base', color: '#eff1f5' },
  { name: 'Latte surface', color: '#ccd0da' },
  { name: 'Latte text', color: '#4c4f69' },
  { name: 'Latte pink', color: '#ea76cb' },
  { name: 'Latte red', color: '#d20f39' },
  { name: 'Latte peach', color: '#fe640b' },
  { name: 'Latte yellow', color: '#df8e1d' },
  { name: 'Latte green', color: '#40a02b' },
  { name: 'Latte teal', color: '#179299' },
  { name: 'Latte blue', color: '#1e66f5' },
  { name: 'Latte mauve', color: '#8839ef' },
  { name: 'Ink', color: '#0b0b0d' },
  { name: 'Graphite', color: '#242428' },
  { name: 'Slate', color: '#3d3d46' },
  { name: 'Paper', color: '#e5e5eb' },
];

const MESH_PRESETS = [
  {
    name: 'Graphite',
    base: '#141417',
    points: [
      { x: 16, y: 18, color: '#2c2c33', size: 68 },
      { x: 84, y: 14, color: '#1f2126', size: 62 },
      { x: 48, y: 88, color: '#26262c', size: 76 },
      { x: 88, y: 80, color: '#2b2433', size: 58 },
    ],
  },
  {
    name: 'Catppuccin Mocha',
    base: '#11111b',
    points: [
      { x: 12, y: 20, color: '#1e1e2e', size: 72 },
      { x: 84, y: 16, color: '#45475a', size: 60 },
      { x: 22, y: 86, color: '#313244', size: 70 },
      { x: 86, y: 82, color: '#cba6f7', size: 54 },
    ],
  },
  {
    name: 'Catppuccin Latte',
    base: '#eff1f5',
    points: [
      { x: 14, y: 16, color: '#dce0e8', size: 70 },
      { x: 86, y: 20, color: '#ccd0da', size: 62 },
      { x: 70, y: 86, color: '#bcc0cc', size: 72 },
      { x: 18, y: 84, color: '#e6e9ef', size: 60 },
    ],
  },
  {
    name: 'Aurora',
    base: '#0b1020',
    points: [
      { x: 15, y: 20, color: '#1e3a8a', size: 66 },
      { x: 85, y: 18, color: '#7c3aed', size: 60 },
      { x: 20, y: 85, color: '#0f766e', size: 70 },
      { x: 82, y: 82, color: '#be185d', size: 58 },
    ],
  },
  {
    name: 'Sunset',
    base: '#1b1020',
    points: [
      { x: 18, y: 22, color: '#7c2d12', size: 68 },
      { x: 80, y: 16, color: '#be123c', size: 60 },
      { x: 45, y: 88, color: '#c2410c', size: 74 },
      { x: 86, y: 78, color: '#f59e0b', size: 52 },
    ],
  },
  {
    name: 'Ocean',
    base: '#06131f',
    points: [
      { x: 16, y: 20, color: '#0c4a6e', size: 70 },
      { x: 84, y: 16, color: '#0e7490', size: 62 },
      { x: 40, y: 88, color: '#1e3a8a', size: 76 },
      { x: 88, y: 80, color: '#164e63', size: 58 },
    ],
  },
  {
    name: 'Candy',
    base: '#1a1025',
    points: [
      { x: 18, y: 18, color: '#be185d', size: 64 },
      { x: 82, y: 18, color: '#7e22ce', size: 60 },
      { x: 30, y: 86, color: '#db2777', size: 70 },
      { x: 82, y: 82, color: '#2563eb', size: 58 },
    ],
  },
  {
    name: 'Forest',
    base: '#0a140f',
    points: [
      { x: 16, y: 20, color: '#14532d', size: 70 },
      { x: 84, y: 16, color: '#166534', size: 60 },
      { x: 45, y: 88, color: '#365314', size: 74 },
      { x: 86, y: 80, color: '#0f766e', size: 58 },
    ],
  },
];

let meshState = cloneMesh(DEFAULT_MESH);
let meshSelected = 0;
let meshDrag = null;

function cloneMesh(mesh) {
  return { base: mesh.base, points: mesh.points.map((point) => ({ ...point })) };
}

function clampNumber(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

function normalizeColor(value, fallback) {
  return /^#[0-9a-f]{6}$/i.test(String(value || '')) ? String(value).toLowerCase() : fallback;
}

function isLightColor(value) {
  const color = normalizeColor(value, '');
  if (!color) return false;
  const r = parseInt(color.slice(1, 3), 16);
  const g = parseInt(color.slice(3, 5), 16);
  const b = parseInt(color.slice(5, 7), 16);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 160;
}

function normalizeMesh(raw) {
  const source = raw && typeof raw === 'object' ? raw : {};
  const base = normalizeColor(source.base, DEFAULT_MESH.base);
  const points = Array.isArray(source.points)
    ? source.points.slice(0, 12).map((point) => {
      if (!point || typeof point !== 'object') return null;
      const x = Number(point.x);
      const y = Number(point.y);
      if (!Number.isFinite(x) || !Number.isFinite(y)) return null;
      const size = Number(point.size);
      return {
        x: clampNumber(x, 0, 100),
        y: clampNumber(y, 0, 100),
        color: normalizeColor(point.color, '#89b4fa'),
        size: clampNumber(Number.isFinite(size) ? size : 60, 10, 100),
      };
    }).filter(Boolean)
    : [];
  if (!points.length) return cloneMesh(DEFAULT_MESH);
  return { base, points };
}

function loadMesh() {
  try {
    const raw = JSON.parse(localStorage.getItem(MESH_KEY) || 'null');
    if (raw) return normalizeMesh(raw);
  } catch {
    // Ignore storage errors and use the default gradient.
  }
  return cloneMesh(DEFAULT_MESH);
}

function saveMesh() {
  try {
    localStorage.setItem(MESH_KEY, JSON.stringify(meshState));
  } catch {
    // Cosmetic preference only.
  }
}

function rgba(hex, alpha) {
  const match = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!match) return 'rgba(0, 0, 0, ' + alpha + ')';
  const value = parseInt(match[1], 16);
  return 'rgba(' + ((value >> 16) & 255) + ', ' + ((value >> 8) & 255) + ', ' + (value & 255) + ', ' + alpha + ')';
}

function meshCSS(mesh) {
  const layers = mesh.points.map((point) => {
    const x = Math.round(point.x * 10) / 10;
    const y = Math.round(point.y * 10) / 10;
    const size = Math.round(point.size * 10) / 10;
    return 'radial-gradient(circle at ' + x + '% ' + y + '%, ' + point.color + ' 0%, ' + rgba(point.color, 0) + ' ' + size + '%)';
  });
  layers.push(mesh.base);
  return layers.join(', ');
}

function applyMesh() {
  if (els.meshBackground) els.meshBackground.style.background = meshCSS(meshState);
}

function initMesh() {
  meshState = loadMesh();
  meshSelected = 0;
  applyMesh();
}

function selectedMeshPoint() {
  return meshState.points[meshSelected] || null;
}

function selectMeshPoint(index) {
  meshSelected = index;
  els.meshPreview.querySelectorAll('.mesh-node').forEach((node) => {
    node.classList.toggle('selected', Number(node.dataset.meshNode) === index);
  });
  updateMeshControls();
}

function renderMeshNodes() {
  els.meshPreview.querySelectorAll('.mesh-node').forEach((node) => node.remove());
  meshState.points.forEach((point, index) => {
    const node = document.createElement('button');
    node.type = 'button';
    node.className = 'mesh-node' + (index === meshSelected ? ' selected' : '');
    node.dataset.meshNode = String(index);
    node.style.left = point.x + '%';
    node.style.top = point.y + '%';
    node.style.background = point.color;
    node.title = 'Color point ' + (index + 1) + ' — drag to move';
    node.setAttribute('aria-label', 'Color point ' + (index + 1));
    els.meshPreview.appendChild(node);
  });
}

function updateMeshControls() {
  const point = selectedMeshPoint();
  els.meshColor.value = point ? point.color : '#89b4fa';
  els.meshColor.disabled = !point;
  els.meshBase.value = meshState.base;
  els.meshSize.value = String(Math.round(point ? point.size : 60));
  els.meshSizeValue.textContent = String(Math.round(point ? point.size : 60));
  els.meshSize.disabled = !point;
  els.meshRemove.disabled = meshState.points.length <= 1;
}

function updatePresetSelection() {
  const current = JSON.stringify(meshState);
  els.meshPresets.querySelectorAll('[data-mesh-preset]').forEach((button) => {
    const preset = MESH_PRESETS.find((item) => item.name === button.dataset.meshPreset);
    button.classList.toggle('active', !!preset && JSON.stringify(normalizeMesh(preset)) === current);
  });
}

function renderMeshEditor() {
  els.meshPreview.style.background = meshCSS(meshState);
  renderMeshNodes();
  updateMeshControls();
  updatePresetSelection();
}

function commitMesh() {
  applyMesh();
  if (els.meshPreview) els.meshPreview.style.background = meshCSS(meshState);
  saveMesh();
  renderMeshNodes();
  updateMeshControls();
  updatePresetSelection();
  els.meshStatus.textContent = 'Saved locally.';
}

function applyPaletteColor(color) {
  const point = selectedMeshPoint();
  if (!point) return;
  point.color = normalizeColor(color, '#89b4fa');
  els.meshColor.value = point.color;
  commitMesh();
}

function useMeshPreset(preset) {
  meshState = cloneMesh(normalizeMesh(preset));
  meshSelected = 0;
  commitMesh();
}

function addMeshPoint(x, y) {
  if (meshState.points.length >= 12) {
    showToast('A mesh gradient can have up to 12 points.', 'info', 2500);
    return;
  }
  meshState.points.push({
    x: clampNumber(x, 0, 100),
    y: clampNumber(y, 0, 100),
    color: MESH_NEW_COLORS[meshState.points.length % MESH_NEW_COLORS.length],
    size: 60,
  });
  meshSelected = meshState.points.length - 1;
  commitMesh();
}

function removeMeshPoint() {
  if (meshState.points.length <= 1) return;
  meshState.points.splice(meshSelected, 1);
  meshSelected = Math.min(meshSelected, meshState.points.length - 1);
  commitMesh();
}

function resetMesh() {
  meshState = cloneMesh(DEFAULT_MESH);
  meshSelected = 0;
  commitMesh();
}

function meshPointFromEvent(event) {
  const rect = els.meshPreview.getBoundingClientRect();
  if (!rect.width || !rect.height) return { x: 50, y: 50 };
  return {
    x: clampNumber(((event.clientX - rect.left) / rect.width) * 100, 0, 100),
    y: clampNumber(((event.clientY - rect.top) / rect.height) * 100, 0, 100),
  };
}

function onMeshDoubleClick(event) {
  if (event.target.closest('.mesh-node')) return;
  const point = meshPointFromEvent(event);
  addMeshPoint(point.x, point.y);
}

function onMeshPointerDown(event) {
  const node = event.target.closest('.mesh-node');
  if (!node || event.button !== 0) return;
  const index = Number(node.dataset.meshNode);
  if (!Number.isInteger(index) || !meshState.points[index]) return;
  selectMeshPoint(index);
  meshDrag = { index, pointerId: event.pointerId };
  try { node.setPointerCapture(event.pointerId); } catch { /* optional */ }
  event.preventDefault();
}

function onMeshPointerMove(event) {
  if (!meshDrag || event.pointerId !== meshDrag.pointerId) return;
  const point = meshState.points[meshDrag.index];
  if (!point) return;
  const next = meshPointFromEvent(event);
  point.x = next.x;
  point.y = next.y;
  const node = els.meshPreview.querySelector('[data-mesh-node="' + meshDrag.index + '"]');
  if (node) {
    node.style.left = point.x + '%';
    node.style.top = point.y + '%';
  }
  applyMesh();
}

function endMeshDrag(event) {
  if (!meshDrag || event.pointerId !== meshDrag.pointerId) return;
  meshDrag = null;
  saveMesh();
  els.meshStatus.textContent = 'Saved locally.';
}

function onMeshKeyDown(event) {
  const node = event.target.closest('.mesh-node');
  if (!node) return;
  const index = Number(node.dataset.meshNode);
  const point = meshState.points[index];
  if (!point) return;
  const step = event.shiftKey ? 10 : 1;
  let handled = true;
  if (event.key === 'ArrowLeft') point.x = clampNumber(point.x - step, 0, 100);
  else if (event.key === 'ArrowRight') point.x = clampNumber(point.x + step, 0, 100);
  else if (event.key === 'ArrowUp') point.y = clampNumber(point.y - step, 0, 100);
  else if (event.key === 'ArrowDown') point.y = clampNumber(point.y + step, 0, 100);
  else handled = false;
  if (!handled) return;
  event.preventDefault();
  node.style.left = point.x + '%';
  node.style.top = point.y + '%';
  applyMesh();
  saveMesh();
  els.meshStatus.textContent = 'Saved locally.';
}

function initMeshEditor() {
  const paletteFragment = document.createDocumentFragment();
  for (const item of MESH_PALETTE) {
    const swatch = document.createElement('button');
    swatch.type = 'button';
    swatch.className = 'mesh-swatch';
    swatch.style.background = item.color;
    swatch.title = item.name + ' ' + item.color;
    swatch.setAttribute('aria-label', 'Use ' + item.name);
    swatch.addEventListener('click', () => applyPaletteColor(item.color));
    paletteFragment.appendChild(swatch);
  }
  els.meshPalette.replaceChildren(paletteFragment);

  const presetFragment = document.createDocumentFragment();
  for (const preset of MESH_PRESETS) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'mesh-preset' + (isLightColor(preset.base) ? ' light' : '');
    button.dataset.meshPreset = preset.name;
    button.style.background = meshCSS(normalizeMesh(preset));
    button.textContent = preset.name;
    button.addEventListener('click', () => useMeshPreset(preset));
    presetFragment.appendChild(button);
  }
  els.meshPresets.replaceChildren(presetFragment);

  els.meshColor.addEventListener('input', () => {
    const point = selectedMeshPoint();
    if (!point) return;
    point.color = normalizeColor(els.meshColor.value, '#89b4fa');
    commitMesh();
  });
  els.meshBase.addEventListener('input', () => {
    meshState.base = normalizeColor(els.meshBase.value, DEFAULT_MESH.base);
    commitMesh();
  });
  els.meshSize.addEventListener('input', () => {
    const point = selectedMeshPoint();
    if (!point) return;
    point.size = clampNumber(Number(els.meshSize.value), 10, 100);
    els.meshSizeValue.textContent = String(Math.round(point.size));
    commitMesh();
  });
  els.meshAdd.addEventListener('click', () => addMeshPoint(50, 50));
  els.meshRemove.addEventListener('click', removeMeshPoint);
  els.meshReset.addEventListener('click', resetMesh);
  els.meshPreview.addEventListener('dblclick', onMeshDoubleClick);
  els.meshPreview.addEventListener('pointerdown', onMeshPointerDown);
  els.meshPreview.addEventListener('pointermove', onMeshPointerMove);
  els.meshPreview.addEventListener('pointerup', endMeshDrag);
  els.meshPreview.addEventListener('pointercancel', endMeshDrag);
  els.meshPreview.addEventListener('keydown', onMeshKeyDown);
  renderMeshEditor();
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
  els.newTabButton.addEventListener('click', () => newChat(null));
  els.chatTabs.addEventListener('click', (event) => {
    const close = event.target.closest('[data-chat-tab-close]');
    if (close) {
      event.preventDefault();
      closeTab(close.dataset.chatTabClose);
      return;
    }
    const tab = event.target.closest('[data-chat-tab]');
    if (tab) openChat(tab.dataset.chatTab);
  });
  els.chatTabs.addEventListener('auxclick', (event) => {
    if (event.button !== 1) return;
    const tab = event.target.closest('[data-chat-tab]');
    if (tab) {
      event.preventDefault();
      closeTab(tab.dataset.chatTab);
    }
  });
  els.chatTabs.addEventListener('contextmenu', (event) => {
    const tab = event.target.closest('[data-chat-tab]');
    if (!tab) return;
    const chat = state.chats.find((item) => item.id === tab.dataset.chatTab);
    if (!chat) return;
    event.preventDefault();
    showChatMenu(chat, tab);
  });
  els.retryBoot.addEventListener('click', () => boot());

  els.saveSettings.addEventListener('click', saveSettings);
  els.refreshModels.addEventListener('click', () => refreshModels());
  els.envSave.addEventListener('click', saveEnvFile);
  els.envReload.addEventListener('click', loadEnvEditor);
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

  initMeshEditor();
}

initTheme();
initMesh();
wireUI();
restoreSidebar();
boot();
