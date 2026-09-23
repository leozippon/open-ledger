import { api } from './api.js';
import { currentMonth, currentYear, monthOf } from './fmt.js';
import { watchTheme } from './theme.js';
import { toast } from './ui.js';
import * as homeView from './views/home.js';
import * as settingsView from './views/settings.js';
import { openEntry } from './views/entry.js';
import { openSearch, refreshActivitySheet } from './views/ledger.js';

watchTheme();

const bootEl = document.getElementById('boot');
const loginEl = document.getElementById('login');
const appEl = document.getElementById('app');
const viewEl = document.getElementById('view');
const tabbarEl = document.getElementById('tabbar');
const loginForm = document.getElementById('login-form');
const usernameEl = document.getElementById('login-username');
const passwordEl = document.getElementById('login-password');
const loginErrorEl = document.getElementById('login-error');
const loginSubmitEl = document.getElementById('login-submit');
const loginHintEl = document.getElementById('login-hint');
const loginSwitchEl = document.getElementById('login-switch');
let registerMode = false;

const views = { home: homeView, settings: settingsView };

const state = {
  tab: 'home',
  pane: 'ledger',
  month: currentMonth(),
  statsYear: currentYear(),
  statsGrain: 'month',
  statsKind: 'expense',
  // statsMember is 0 for the whole family, otherwise one member's personal book.
  // statsShared selects the shared book, which is exclusive of any member.
  statsMember: 0,
  statsShared: false,
  statsActivity: 0,
  ledgerActivity: 0,
  me: null,
  users: [],
  categories: [],
  cards: [],
  activities: [],
  activityMonths: [],
  moves: [],
  summary: emptySummary(currentMonth()),
  statsSummary: null,
  statsTrend: [],
  // memberSummary holds the selected book's month on the ledger.
  memberSummary: null,
};

// ctx is the only thing views receive, which keeps them free of imports from here.
const ctx = {
  state,
  render,
  reload,
  setMonth,
  setYear,
  setGrain,
  setStatsPeriod,
  setTab,
  setPane,
  setStatsFilter,
  setStatsActivity,
  afterSave,
  logout,
  openEntry: (tx) => openEntry(ctx, tx),
  openSearch: () => openSearch(ctx),
};

let renderedTab = null;
let renderedPane = null;

function emptySummary(month) {
  return { month, income: 0, expense: 0, budget: 0, balance: 0, balances: [], currencies: [], expense_by_category: [], income_by_category: [], expense_by_card: [], income_by_card: [], days: [] };
}

async function reload() {
  try {
    const member = state.statsMember;
    const shared = state.statsShared;
    const scoped = member || shared;
    const userId = member || undefined;
    const activityId = state.statsActivity || undefined;
    const [me, categories, cards, users, activities, activityMonths, moves, summary, memberSummary, statsPack] = await Promise.all([
      api.me(),
      api.categories(),
      api.cards(),
      api.users(),
      api.activities(),
      api.activityMonths(state.month, userId, shared),
      api.moves(state.month, userId, shared),
      api.summary(state.month),
      scoped ? api.summary(state.month, userId, shared) : null,
      state.pane === 'stats' ? loadStats(userId, shared, activityId) : Promise.resolve({ statsSummary: null, statsTrend: [] }),
    ]);
    Object.assign(state, { me, categories, cards, users, activities, activityMonths, moves, summary, memberSummary }, statsPack);
    if (state.ledgerActivity && !activities.some((item) => item.id === state.ledgerActivity)) {
      state.ledgerActivity = 0;
    }
    if (state.statsActivity && !activities.some((item) => item.id === state.statsActivity)) {
      state.statsActivity = 0;
    }
    if (member && !users.some((user) => user.id === member)) {
      Object.assign(state, { statsMember: 0, statsShared: false, memberSummary: null });
    }
    render();
    await refreshActivitySheet(ctx);
  } catch (error) {
    if (error.status !== 401) toast(error.message, true);
  }
}

function render() {
  for (const button of tabbarEl.querySelectorAll('.tab')) {
    button.setAttribute('aria-selected', button.dataset.tab === state.tab ? 'true' : 'false');
  }
  if (renderedTab === 'home' && state.tab === 'home') {
    const samePane = renderedPane === state.pane;
    const offset = viewEl.scrollTop;
    homeView.refresh(viewEl, ctx);
    renderedPane = state.pane;
    viewEl.scrollTop = samePane ? offset : 0;
    return;
  }
  const tabChanged = renderedTab !== null && renderedTab !== state.tab;
  const sameTab = renderedTab === state.tab;
  const offset = viewEl.scrollTop;
  viewEl.replaceChildren(views[state.tab].render(ctx));
  if (tabChanged) {
    viewEl.classList.remove('view-enter');
    void viewEl.offsetWidth;
    viewEl.classList.add('view-enter');
  }
  renderedTab = state.tab;
  renderedPane = state.pane;
  viewEl.scrollTop = sameTab ? offset : 0;
}

function setTab(tab) {
  state.tab = tab;
  render();
}

function setPane(pane) {
  if (pane === state.pane && state.tab === 'home') return;
  const load = pane === 'stats' && state.pane !== 'stats';
  state.pane = pane;
  if (load) reload();
  else render();
}

function setMonth(month) {
  state.month = month;
  reload();
}

function setYear(year) {
  if (!/^\d{4}$/.test(year)) return;
  state.statsYear = year;
  reload();
}

function setGrain(grain) {
  setStatsPeriod({ grain });
}

function setStatsPeriod({ grain, month, year }) {
  let changed = false;
  if (grain && grain !== state.statsGrain) {
    state.statsGrain = grain;
    if (grain === 'year' && !year) state.statsYear = state.month.slice(0, 4);
    changed = true;
  }
  if (month && month !== state.month) {
    state.month = month;
    changed = true;
  }
  if (year && year !== state.statsYear) {
    state.statsYear = year;
    changed = true;
  }
  if (changed) reload();
}

function setStatsFilter(id, shared) {
  state.statsMember = shared ? 0 : id;
  state.statsShared = !!shared;
  reload();
}

function setStatsActivity(id) {
  if (id === state.statsActivity) return;
  state.statsActivity = id;
  reload();
}

async function loadStats(userId, shared, activityId) {
  if (state.statsGrain === 'year') {
    const [statsSummary, statsTrend] = await Promise.all([
      api.yearSummary(state.statsYear, userId, shared, activityId),
      api.yearTrend(state.statsYear, userId, shared, activityId),
    ]);
    return { statsSummary, statsTrend };
  }
  if (state.statsGrain === 'all') {
    const [statsSummary, statsTrend] = await Promise.all([
      api.allSummary(userId, shared, activityId),
      api.allTrend(userId, shared, activityId),
    ]);
    return { statsSummary, statsTrend };
  }
  return {
    statsSummary: await api.summary(state.month, userId, shared, activityId),
    statsTrend: [],
  };
}

async function afterSave(saved) {
  state.month = monthOf(saved.date);
  state.tab = 'home';
  state.pane = 'ledger';
  await reload();
}

async function logout() {
  try {
    await api.logout();
  } catch (error) {
    if (error.status !== 401) toast(error.message, true);
  }
  showLogin();
}

function showApp() {
  bootEl.hidden = true;
  loginEl.hidden = true;
  appEl.hidden = false;
}

function showLogin() {
  bootEl.hidden = true;
  appEl.hidden = true;
  loginEl.hidden = false;
  state.me = null;
  setRegisterMode(false);
  (usernameEl.value ? passwordEl : usernameEl).focus({ preventScroll: true });
}

function setRegisterMode(on) {
  registerMode = on;
  loginHintEl.textContent = on ? '注册，开一本自己的账' : '登录自己的账本';
  loginSubmitEl.textContent = on ? '注册' : '登录';
  passwordEl.autocomplete = on ? 'new-password' : 'current-password';
  loginSwitchEl.textContent = on ? '已有账本？去登录' : '注册，开一本自己的账';
}

tabbarEl.addEventListener('click', (event) => {
  const button = event.target.closest('.tab');
  if (!button) return;
  if (button.dataset.tab === 'add') openEntry(ctx);
  else setTab(button.dataset.tab);
});

loginSwitchEl.addEventListener('click', () => {
  loginErrorEl.hidden = true;
  setRegisterMode(!registerMode);
  (usernameEl.value ? passwordEl : usernameEl).focus({ preventScroll: true });
});

loginForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  loginErrorEl.hidden = true;
  loginSubmitEl.disabled = true;
  try {
    state.me = registerMode
      ? await api.signup(usernameEl.value, passwordEl.value)
      : await api.login(usernameEl.value, passwordEl.value);
    passwordEl.value = '';
    state.tab = 'home';
    state.pane = 'ledger';
    showApp();
    await reload();
  } catch (error) {
    loginErrorEl.textContent = error.message;
    loginErrorEl.hidden = false;
  } finally {
    loginSubmitEl.disabled = false;
  }
});

window.addEventListener('ledger:unauthorized', showLogin);

(async function boot() {
  try {
    const cfg = await api.config();
    loginSwitchEl.hidden = !cfg.signup;
  } catch {
    loginSwitchEl.hidden = true;
  }
  try {
    state.me = await api.me();
    showApp();
    await reload();
  } catch (error) {
    if (error.status !== 401) toast(error.message, true);
    showLogin();
  }
}());
