class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

async function call(method, path, body) {
  let response;
  try {
    response = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, '网络连接失败');
  }
  let data = null;
  try { data = await response.json(); } catch { /* empty or non-JSON body */ }
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event('ledger:unauthorized'));
    throw new ApiError(response.status, (data && data.error) || `请求失败（${response.status}）`);
  }
  return data;
}

const query = (params) => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') search.set(key, value);
  }
  const text = search.toString();
  return text ? `?${text}` : '';
};

export const api = {
  config: () => call('GET', '/api/config'),
  me: () => call('GET', '/api/me'),
  login: (username, password) => call('POST', '/api/login', { username, password }),
  signup: (username, password) => call('POST', '/api/signup', { username, password }),
  logout: () => call('POST', '/api/logout'),
  changePassword: (oldPassword, newPassword) =>
    call('PUT', '/api/me/password', { old_password: oldPassword, new_password: newPassword }),

  users: () => call('GET', '/api/users'),
  createUser: (body) => call('POST', '/api/users', body),
  renameUser: (id, username) => call('PUT', `/api/users/${id}`, { username }),
  resetPassword: (id, password) => call('PUT', `/api/users/${id}/password`, { password }),
  deleteUser: (id) => call('DELETE', `/api/users/${id}`),

  transactions: (params) => call('GET', `/api/transactions${query(params)}`),
  moves: (month, userId, shared) => call('GET', `/api/transactions${query({ month, user_id: userId, shared: shared ? 1 : undefined, moves: 1 })}`),
  createTransaction: (body) => call('POST', '/api/transactions', body),
  updateTransaction: (id, body) => call('PUT', `/api/transactions/${id}`, body),
  deleteTransaction: (id) => call('DELETE', `/api/transactions/${id}`),

  summary: (month, userId, shared, activityId) => call('GET', `/api/summary${query({ month, user_id: userId, shared: shared ? 1 : undefined, activity_id: activityId })}`),
  yearSummary: (year, userId, shared, activityId) => call('GET', `/api/summary${query({ year, user_id: userId, shared: shared ? 1 : undefined, activity_id: activityId })}`),
  yearTrend: (year, userId, shared, activityId) => call('GET', `/api/trend${query({ year, user_id: userId, shared: shared ? 1 : undefined, activity_id: activityId })}`),
  allSummary: (userId, shared, activityId) => call('GET', `/api/summary${query({ all: 1, user_id: userId, shared: shared ? 1 : undefined, activity_id: activityId })}`),
  allTrend: (userId, shared, activityId) => call('GET', `/api/trend${query({ all: 1, user_id: userId, shared: shared ? 1 : undefined, activity_id: activityId })}`),

  activities: () => call('GET', '/api/activities'),
  createActivity: (body) => call('POST', '/api/activities', body),
  updateActivity: (id, body) => call('PUT', `/api/activities/${id}`, body),
  reorderActivities: (body) => call('PUT', '/api/activities/order', body),
  deleteActivity: (id) => call('DELETE', `/api/activities/${id}`),
  activityMonths: (month, userId, shared) => call('GET', `/api/activities/months${query({ month, user_id: userId, shared: shared ? 1 : undefined })}`),
  searchNotes: (q, userId, shared) => call('GET', `/api/search${query({ q, user_id: userId, shared: shared ? 1 : undefined })}`),

  categories: () => call('GET', '/api/categories'),
  createCategory: (body) => call('POST', '/api/categories', body),
  updateCategory: (id, body) => call('PUT', `/api/categories/${id}`, body),
  reorderCategories: (body) => call('PUT', '/api/categories/order', body),
  deleteCategory: (id) => call('DELETE', `/api/categories/${id}`),

  cards: () => call('GET', '/api/cards'),
  createCard: (body) => call('POST', '/api/cards', body),
  updateCard: (id, body) => call('PUT', `/api/cards/${id}`, body),
  reorderCards: (body) => call('PUT', '/api/cards/order', body),
  deleteCard: (id) => call('DELETE', `/api/cards/${id}`),

  recognize: (body) => call('POST', '/api/recognize', body),
};
