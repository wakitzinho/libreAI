/** Same-origin JSON API client and SSE consumer. */

export class ApiError extends Error {
  constructor(message, status = 0, code = 'error') {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

async function parseError(response) {
  let code = 'http_error';
  let message = 'Request failed (HTTP ' + response.status + ').';
  try {
    const data = await response.json();
    if (data && data.error) {
      code = data.error.code || code;
      message = data.error.message || message;
    }
  } catch {
    // Ignore malformed error bodies.
  }
  return new ApiError(message, response.status, code);
}

async function request(method, path, { body, signal } = {}) {
  const headers = { Accept: 'application/json' };
  const options = {
    method,
    headers,
    credentials: 'same-origin',
    cache: 'no-store',
    signal,
  };
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body);
  }
  let response;
  try {
    response = await fetch(path, options);
  } catch (error) {
    if (error.name === 'AbortError') throw error;
    throw new ApiError('Could not reach the libreAI server.', 0, 'network_error');
  }
  if (!response.ok) throw await parseError(response);
  if (response.status === 204) return null;
  const contentType = response.headers.get('content-type') || '';
  if (contentType.includes('application/json')) return response.json();
  return response.text();
}

export function parseSSEEvent(block) {
  if (!block || !block.trim()) return null;
  let event = 'message';
  const data = [];
  for (const rawLine of block.split('\n')) {
    if (!rawLine || rawLine.startsWith(':')) continue;
    const colon = rawLine.indexOf(':');
    const field = colon === -1 ? rawLine : rawLine.slice(0, colon);
    let value = colon === -1 ? '' : rawLine.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') event = value;
    else if (field === 'data') data.push(value);
  }
  if (data.length === 0) return null;
  const text = data.join('\n');
  if (!text) return null;
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    throw new ApiError('The server sent a malformed stream event.', 0, 'bad_stream');
  }
  return { event, data: payload };
}

export const api = {
  bootstrap: () => request('GET', '/api/bootstrap'),
  models: () => request('GET', '/api/models'),
  refreshModels: () => request('POST', '/api/models/refresh'),
  settings: () => request('GET', '/api/settings'),
  saveSettings: (body) => request('PUT', '/api/settings', { body }),

  createProject: (name) => request('POST', '/api/projects', { body: { name } }),
  renameProject: (id, name) => request('PATCH', '/api/projects/' + encodeURIComponent(id), { body: { name } }),
  deleteProject: (id) => request('DELETE', '/api/projects/' + encodeURIComponent(id)),

  createChat: (body) => request('POST', '/api/chats', { body }),
  getChat: (id) => request('GET', '/api/chats/' + encodeURIComponent(id)),
  openChat: (id) => request('POST', '/api/chats/' + encodeURIComponent(id) + '/open'),
  patchChat: (id, body) => request('PATCH', '/api/chats/' + encodeURIComponent(id), { body }),
  deleteChat: (id) => request('DELETE', '/api/chats/' + encodeURIComponent(id)),
  moveChat: (id, projectId) => request('POST', '/api/chats/' + encodeURIComponent(id) + '/move', { body: { project_id: projectId } }),
  exportURL: (id, format) => '/api/chats/' + encodeURIComponent(id) + '/export?format=' + encodeURIComponent(format),

  async sendMessage(chatId, payload, { signal, onEvent } = {}) {
    let response;
    try {
      response = await fetch('/api/chats/' + encodeURIComponent(chatId) + '/messages', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Accept: 'text/event-stream',
        },
        body: JSON.stringify(payload),
        credentials: 'same-origin',
        cache: 'no-store',
        signal,
      });
    } catch (error) {
      if (error.name === 'AbortError') throw error;
      throw new ApiError('Could not reach the libreAI server.', 0, 'network_error');
    }
    if (!response.ok) throw await parseError(response);
    if (!response.body) throw new ApiError('Streaming is not supported by this browser.', 0, 'no_stream');

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    const consume = (block) => {
      const parsed = parseSSEEvent(block);
      if (parsed) onEvent(parsed.event, parsed.data);
    };
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        buffer = buffer.replace(/\r\n/g, '\n');
        let boundary = buffer.indexOf('\n\n');
        while (boundary !== -1) {
          consume(buffer.slice(0, boundary));
          buffer = buffer.slice(boundary + 2);
          boundary = buffer.indexOf('\n\n');
        }
      }
      buffer += decoder.decode();
      buffer = buffer.replace(/\r\n/g, '\n');
      if (buffer.trim()) consume(buffer);
    } catch (error) {
      if (error.name === 'AbortError') throw error;
      throw new ApiError('The response stream ended unexpectedly.', 0, 'stream_error');
    } finally {
      reader.releaseLock();
    }
  },
};
