import { el, frag, segmented } from '../dom.js';
import { monthSwitch, openSheet, openStatsPeriodPicker, yearSwitch } from '../ui.js';
import * as ledgerView from './ledger.js';
import * as statsView from './stats.js';

const PANES = [
  { value: 'ledger', label: '明细' },
  { value: 'stats', label: '统计' },
];

const GRAINS = [
  { value: 'month', label: '月' },
  { value: 'year', label: '年' },
  { value: 'all', label: '全部' },
];

export function render(ctx) {
  const pane = ctx.state.pane;
  return frag([
    el('div', { class: 'home-switch' }, segmented(PANES, pane, ctx.setPane)),
    periodBlock(ctx),
    filterBlock(ctx),
    el('div', { class: 'home-pane', 'data-pane': pane }, paneBody(ctx)),
  ]);
}

export function refresh(root, ctx) {
  if (!root.querySelector('.home-pane')) {
    root.replaceChildren(render(ctx));
    return;
  }
  const pane = ctx.state.pane;
  for (const button of root.querySelectorAll('.home-switch [role="tab"]')) {
    button.setAttribute('aria-selected', button.dataset.value === pane ? 'true' : 'false');
  }
  const periodEl = root.querySelector('.period-block') || root.querySelector('.month-switch');
  if (!samePeriod(periodEl, ctx)) periodEl.replaceWith(periodBlock(ctx));
  const filtersEl = root.querySelector('.home-filters');
  const nextFilters = filterBlock(ctx);
  const after = root.querySelector('.period-block') || root.querySelector('.month-switch');
  if (filtersEl && nextFilters) filtersEl.replaceWith(nextFilters);
  else if (filtersEl) filtersEl.remove();
  else if (nextFilters) after.after(nextFilters);
  const paneEl = root.querySelector('.home-pane');
  const changed = paneEl.dataset.pane !== pane;
  paneEl.replaceChildren(paneBody(ctx));
  if (changed) {
    paneEl.dataset.pane = pane;
    paneEl.classList.remove('pane-fwd', 'pane-back');
    void paneEl.offsetWidth;
    paneEl.classList.add(pane === 'stats' ? 'pane-fwd' : 'pane-back');
  }
}

function paneBody(ctx) {
  return ctx.state.pane === 'stats' ? statsView.render(ctx) : ledgerView.render(ctx);
}

function searchMark() {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'period-search-icon');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.innerHTML = '<circle cx="11" cy="11" r="6.5"/><path d="M16 16l5 5"/>';
  return svg;
}

function periodBlock(ctx) {
  if (ctx.state.pane !== 'stats') {
    return el('div', {
      class: 'period-block ledger-period',
      'data-month': ctx.state.month,
    }, [
      monthSwitch(ctx.state.month, ctx.setMonth),
      el('button', { class: 'period-search', type: 'button', 'aria-label': '搜索', onclick: ctx.openSearch }, searchMark()),
    ]);
  }

  const { statsGrain, month, statsYear, statsActivity } = ctx.state;
  const open = () => openStatsPeriodPicker({
    grain: statsGrain,
    month,
    year: statsYear,
    onMonth: (value) => ctx.setStatsPeriod({ grain: 'month', month: value }),
    onYear: (value) => ctx.setStatsPeriod({ grain: 'year', year: value }),
  });

  return el('div', {
    class: 'period-block',
    'data-grain': statsGrain,
    'data-month': month,
    'data-year': statsYear,
    'data-activity': String(statsActivity || 0),
  }, [
    statsGrain === 'all'
      ? el('div', { class: 'month-switch' }, [
        el('span', { class: 'month-nav-hole', 'aria-hidden': 'true' }),
        el('button', { class: 'month-pick', type: 'button', text: '全部时间' }),
        el('span', { class: 'month-nav-hole', 'aria-hidden': 'true' }),
      ])
      : statsGrain === 'year'
        ? yearSwitch(statsYear, ctx.setYear, open)
        : monthSwitch(month, ctx.setMonth, open),
  ]);
}

function pickActivity(ctx) {
  const list = ctx.state.activities || [];
  const selected = ctx.state.statsActivity;
  const row = (name, id, sub) => el('div', {
    class: 'row tappable',
    onclick: () => {
      sheet.close();
      ctx.setStatsActivity(id);
    },
  }, [
    el('div', { class: 'row-main' }, [
      el('span', { class: 'row-title', text: name }),
      sub ? el('span', { class: 'row-sub', text: sub }) : null,
    ]),
    id === selected ? el('div', { class: 'row-sub', text: '当前' }) : null,
  ]);
  const sheet = openSheet({
    title: '活动',
    body: [el('div', { class: 'card' }, [
      row('全部活动', 0),
      ...list.map((item) => row(item.name, item.id, item.is_default ? '默认' : '')),
    ])],
  });
}

function samePeriod(el, ctx) {
  if (ctx.state.pane === 'stats') {
    return el.classList.contains('period-block')
      && el.dataset.grain === ctx.state.statsGrain
      && el.dataset.month === ctx.state.month
      && el.dataset.year === ctx.state.statsYear
      && el.dataset.activity === String(ctx.state.statsActivity || 0);
  }
  return el.classList.contains('ledger-period') && el.dataset.month === ctx.state.month;
}

function filterBlock(ctx) {
  const rows = [];
  const books = memberChips(ctx);
  if (books) rows.push(books);
  if (ctx.state.pane === 'stats') {
    rows.push(activityChips(ctx));
    rows.push(grainChips(ctx));
  }
  if (!rows.length) return null;
  return el('div', { class: 'home-filters' }, rows);
}

const ACTIVITY_PREVIEW = 3;

function activityChips(ctx) {
  const list = ctx.state.activities || [];
  const selected = ctx.state.statsActivity;
  let shown = list.slice(0, ACTIVITY_PREVIEW);
  if (selected && !shown.some((item) => item.id === selected)) {
    const extra = list.find((item) => item.id === selected);
    if (extra) shown = [...shown.slice(0, ACTIVITY_PREVIEW - 1), extra];
  }
  const chip = (label, value) => el('button', {
    class: 'chip', type: 'button', text: label,
    'aria-selected': value === selected ? 'true' : 'false',
    onclick: () => {
      if (value === selected) return;
      ctx.setStatsActivity(value);
    },
  });
  return el('div', { class: 'chips' }, [
    chip('全部', 0),
    ...shown.map((item) => chip(item.name, item.id)),
    el('button', {
      class: 'chip', type: 'button',
      'aria-haspopup': 'listbox',
      onclick: () => pickActivity(ctx),
    }, [
      el('span', { text: '更多' }),
      el('span', { class: 'chip-caret', text: '▾' }),
    ]),
  ]);
}

function grainChips(ctx) {
  const selected = ctx.state.statsGrain;
  return el('div', { class: 'chips' }, GRAINS.map((item) => el('button', {
    class: 'chip', type: 'button', text: item.label,
    'aria-selected': item.value === selected ? 'true' : 'false',
    onclick: () => {
      if (item.value === selected) return;
      ctx.setGrain(item.value);
    },
  })));
}

// memberChips is the book switch: 全部, 共同, then each member. A single
// member has only one book, so the row stays hidden.
function memberChips(ctx) {
  const { users, statsMember, statsShared } = ctx.state;
  if (users.length < 2) return null;
  const selected = statsShared ? 'shared' : statsMember;
  const chip = (label, value) => el('button', {
    class: 'chip', type: 'button', text: label,
    'aria-selected': value === selected ? 'true' : 'false',
    onclick: () => {
      if (value === selected) return;
      ctx.setStatsFilter(value === 'shared' ? 0 : value, value === 'shared');
    },
  });
  return el('div', { class: 'chips' }, [
    chip('全部', 0),
    chip('共同', 'shared'),
    ...users.map((user) => chip(user.username, user.id)),
  ]);
}
