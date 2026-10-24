/** Sidebar rendering: chats, projects, search, and item action menus. */

import { debounce, icon } from './ui.js';

const COLLAPSED_KEY = 'libreai-collapsed-projects';

export class Sidebar {
  constructor(elements, handlers) {
    this.elements = elements;
    this.handlers = handlers;
    this.projects = [];
    this.chats = [];
    this.activeChatId = null;
    this.query = '';
    this.collapsed = new Set(this.loadCollapsed());

    elements.newChatBtn.addEventListener('click', () => handlers.onNewChat(null));
    elements.newProjectBtn.addEventListener('click', () => handlers.onNewProject());
    elements.settingsBtn.addEventListener('click', () => handlers.onSettings());
    elements.themeBtn.addEventListener('click', () => handlers.onThemeToggle());
    elements.search.addEventListener('input', debounce(() => {
      this.query = elements.search.value.trim();
      this.render();
    }, 70));

    elements.projectList.addEventListener('click', (event) => {
      const more = event.target.closest('[data-project-more]');
      if (more) {
        const project = this.projects.find((p) => p.id === more.dataset.projectMore);
        if (project) handlers.onProjectMenu(project, more);
        return;
      }
      const toggle = event.target.closest('[data-project-toggle]');
      if (toggle) {
        const id = toggle.dataset.projectToggle;
        if (this.collapsed.has(id)) this.collapsed.delete(id);
        else this.collapsed.add(id);
        this.saveCollapsed();
        this.render();
      }
    });

    elements.chatList.addEventListener('click', (event) => {
      const more = event.target.closest('[data-chat-more]');
      if (more) {
        const chat = this.chats.find((c) => c.id === more.dataset.chatMore);
        if (chat) handlers.onChatMenu(chat, more);
        return;
      }
      const link = event.target.closest('[data-chat-link]');
      if (link) {
        const chat = this.chats.find((c) => c.id === link.dataset.chatLink);
        if (chat) handlers.onOpenChat(chat);
      }
    });
  }

  setData(projects, chats) {
    this.projects = projects;
    this.chats = chats;
    this.render();
  }

  setActive(chatId) {
    this.activeChatId = chatId;
    this.render();
  }

  setQuery(query) {
    this.query = query;
    this.elements.search.value = query;
    this.render();
  }

  focusSearch() {
    this.elements.search.focus();
  }

  loadCollapsed() {
    try {
      const value = JSON.parse(localStorage.getItem(COLLAPSED_KEY) || '[]');
      return Array.isArray(value) ? value.filter((id) => typeof id === 'string') : [];
    } catch {
      return [];
    }
  }

  saveCollapsed() {
    try {
      localStorage.setItem(COLLAPSED_KEY, JSON.stringify(Array.from(this.collapsed)));
    } catch {
      // Local storage can be unavailable in privacy mode; collapsing is cosmetic.
    }
  }

  chatRow(chat) {
    const li = document.createElement('li');
    li.className = 'chat-item';
    const row = document.createElement('div');
    row.className = 'item-row' + (chat.id === this.activeChatId ? ' active' : '');

    const link = document.createElement('button');
    link.type = 'button';
    link.className = 'item-link chat-link';
    link.dataset.chatLink = chat.id;
    link.title = chat.title;
    if (chat.id === this.activeChatId) link.setAttribute('aria-current', 'page');
    const label = document.createElement('span');
    label.className = 'item-label';
    label.textContent = chat.title;
    link.appendChild(label);
    row.appendChild(link);

    const more = document.createElement('button');
    more.type = 'button';
    more.className = 'item-more';
    more.dataset.chatMore = chat.id;
    more.setAttribute('aria-label', 'Chat options for ' + chat.title);
    more.appendChild(icon('dots'));
    row.appendChild(more);
    li.appendChild(row);
    return li;
  }

  projectRow(project, chats) {
    const li = document.createElement('li');
    li.className = 'project-item';
    const expanded = !this.collapsed.has(project.id) || !!this.query;

    const row = document.createElement('div');
    row.className = 'item-row project-row';

    const toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'item-link project-toggle';
    toggle.dataset.projectToggle = project.id;
    toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    const chevron = icon('chevron-right', 'icon chevron' + (expanded ? ' expanded' : ''));
    toggle.appendChild(chevron);
    const folder = icon('folder', 'icon folder-icon');
    toggle.appendChild(folder);
    const label = document.createElement('span');
    label.className = 'item-label';
    label.textContent = project.name;
    toggle.appendChild(label);
    const count = document.createElement('span');
    count.className = 'item-count';
    count.textContent = String(chats.length);
    toggle.appendChild(count);
    row.appendChild(toggle);

    const more = document.createElement('button');
    more.type = 'button';
    more.className = 'item-more';
    more.dataset.projectMore = project.id;
    more.setAttribute('aria-label', 'Project options for ' + project.name);
    more.appendChild(icon('dots'));
    row.appendChild(more);
    li.appendChild(row);

    if (expanded) {
      const list = document.createElement('ul');
      list.className = 'project-chats';
      if (chats.length === 0) {
        const empty = document.createElement('li');
        empty.className = 'project-empty';
        empty.textContent = 'No chats';
        list.appendChild(empty);
      } else {
        for (const chat of chats) list.appendChild(this.chatRow(chat));
      }
      li.appendChild(list);
    }
    return li;
  }

  render() {
    const query = this.query.toLowerCase();
    const matches = (chat) => !query || chat.title.toLowerCase().includes(query);
    const filteredChats = this.chats.filter(matches);

    const byProject = new Map();
    for (const chat of filteredChats) {
      if (!chat.project_id) continue;
      if (!byProject.has(chat.project_id)) byProject.set(chat.project_id, []);
      byProject.get(chat.project_id).push(chat);
    }

    const projectFragment = document.createDocumentFragment();
    let visibleProjects = 0;
    for (const project of this.projects) {
      const chats = byProject.get(project.id) || [];
      const allChats = this.chats.filter((c) => c.project_id === project.id);
      const nameMatches = !query || project.name.toLowerCase().includes(query);
      if (query && !nameMatches && chats.length === 0) continue;
      visibleProjects++;
      projectFragment.appendChild(this.projectRow(project, nameMatches && query ? allChats : chats));
    }
    this.elements.projectList.replaceChildren(projectFragment);

    const unfiled = filteredChats.filter((chat) => !chat.project_id);
    const chatFragment = document.createDocumentFragment();
    for (const chat of unfiled) chatFragment.appendChild(this.chatRow(chat));
    this.elements.chatList.replaceChildren(chatFragment);

    const totalMatching = filteredChats.length + (query ? this.projects.filter((p) => p.name.toLowerCase().includes(query)).length : 0);
    if (query && totalMatching === 0) {
      this.elements.empty.textContent = 'No matching chats.';
      this.elements.empty.hidden = false;
    } else if (this.chats.length === 0 && this.projects.length === 0) {
      this.elements.empty.textContent = 'No chats yet.';
      this.elements.empty.hidden = false;
    } else if (unfiled.length === 0 && visibleProjects === 0) {
      this.elements.empty.textContent = 'No chats yet.';
      this.elements.empty.hidden = false;
    } else {
      this.elements.empty.hidden = true;
    }
  }
}
