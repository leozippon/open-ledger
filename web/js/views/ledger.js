import { api } from '../api.js';
import { card, el, empty, frag } from '../dom.js';
import { cardMark, cardShort, currencyLabel, dayLabel, formatCard, memberColor, memberInitial, money, moneyOf, signedMoney, softColor } from '../fmt.js';
import { openSheet, toast } from '../ui.js';
import { commitRecognized, recognizeRow } from './entry.js';

export function render(ctx) {
  return frag([
    ctx.state.me?.recognize ? recognizeRow((got) => commitRecognized(ctx, got)) : null,
    summaryCard(ctx),
    activityBoxes(ctx),
  ]);
}

export function openSearch(ctx) {
  const input = el('input', {
    class: 'row-input search-input', type: 'search', maxlength: 40,
    placeholder: '搜索备注',
  });
  const results = el('div', { class: 'search-results' }, [
    empty('🔍', '输入关键词，查找全部时间的收支'),
  ]);
  let timer = 0;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(() => run(input.value), 160);
  });
  openSheet({
    title: '搜索',
    body: [card(input, 'search-box'), results],
  });
  requestAnimationFrame(() => input.focus());

  async function run(raw) {
    const q = raw.trim();
    if (!q) {
      results.replaceChildren(empty('🔍', '输入关键词，查找全部时间的收支'));
      return;
    }
    try {
      const found = await api.searchNotes(q, ctx.state.statsMember || undefined, ctx.state.statsShared);
      results.replaceChildren(searchBody(ctx, found));
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function searchBody(ctx, found) {
  const list = found.transactions || [];
  return frag([
    el('div', { class: 'search-sum' }, [
      el('span', { text: `${list.length} 笔` }),
      el('span', { class: 'amount-out', text: `支出 ${money(found.expense || 0)}` }),
      found.income ? el('span', { class: 'amount-in', text: `收入 ${money(found.income)}` }) : null,
    ]),
    entryList(ctx, list, '没有找到相关记录'),
  ]);
}

function summaryCard(ctx) {
  const scoped = ctx.state.statsMember || ctx.state.statsShared;
  const summary = scoped ? (ctx.state.memberSummary ?? ctx.state.summary) : ctx.state.summary;
  const grid = el('div', { class: 'summary-grid' }, [
    summaryCell('支出', money(summary.expense), 'amount-out'),
    summaryCell('收入', money(summary.income), 'amount-in'),
  ]);
  if (scoped) {
    const net = summary.income - summary.expense;
    const tone = net > 0 ? 'amount-in' : net < 0 ? 'amount-out' : '';
    return el('div', { class: 'card summary' }, [
      el('div', { class: 'summary-label', text: '本月结余' }),
      el('div', { class: `summary-total ${tone}`, text: money(net) }),
      grid,
    ]);
  }
  return el('div', {
    class: 'card summary tappable',
    onclick: () => openBalances(ctx, summary),
  }, [
    el('div', { class: 'summary-label' }, [
      el('span', { text: '余额' }),
      el('span', { class: 'row-chevron', text: '›' }),
    ]),
    el('div', { class: 'summary-total', text: mainBalance(summary) }),
    extraBalance(summary),
    monthNet(summary),
    grid,
  ]);
}

function openBalances(ctx, summary) {
  const flows = summary.currencies?.length ? summary.currencies : [{
    currency: 'CNY', balance: summary.balance ?? 0, income: summary.income, expense: summary.expense,
  }];
  openSheet({
    title: '余额',
    body: flows.map((flow) => currencyCard(ctx, flow)),
  });
}

function currencyCard(ctx, flow) {
  const funds = cardFunds(ctx, flow.currency);
  return card([
    el('div', { class: 'fx-head' }, [
      el('span', { class: 'fx-code', text: currencyLabel(flow.currency) }),
      el('span', { class: 'fx-balance', text: moneyOf(flow.balance, flow.currency) }),
    ]),
    el('div', { class: 'fx-month' }, [
      el('span', { class: 'amount-out', text: `支出 ${moneyOf(flow.expense, flow.currency)}` }),
      el('span', { class: 'amount-in', text: `收入 ${moneyOf(flow.income, flow.currency)}` }),
    ]),
    funds.length ? el('div', { class: 'fx-cards' }, funds.map((item) => el('div', { class: 'fx-card' }, [
      el('span', {}, [el('span', { text: cardShort(item.card) }), cardMark(item.card.network)]),
      el('span', { text: moneyOf(item.balance, flow.currency) }),
    ]))) : null,
  ]);
}

function cardFunds(ctx, currency) {
  return (ctx.state.cards || []).flatMap((item) => {
    if (item.archived) return [];
    const fund = (item.funds || []).find((row) => row.currency === currency);
    return fund ? [{ card: item, balance: fund.balance }] : [];
  });
}

function mainBalance(summary) {
  const row = (summary.balances || []).find((item) => item.currency === 'CNY');
  return money(row?.amount ?? summary.balance ?? 0);
}

function monthNet(summary) {
  const net = summary.income - summary.expense;
  const tone = net > 0 ? 'amount-in' : net < 0 ? 'amount-out' : '';
  return el('div', { class: 'summary-month' }, [
    el('span', { text: '本月结余' }),
    el('strong', { class: tone, text: money(net) }),
  ]);
}

function extraBalance(summary) {
  if (!summary.balances?.length) return null;
  const extras = ['HKD', 'USD'].flatMap((code) => {
    const row = summary.balances.find((item) => item.currency === code);
    return row ? [`${code} ${moneyOf(row.amount)}`] : [];
  });
  if (!extras.length) return null;
  return el('div', { class: 'summary-fx', text: extras.join('　') });
}

function summaryCell(label, value, cls = '') {
  return el('div', { class: 'summary-cell' }, [
    el('span', { text: label }),
    el('strong', { class: cls, text: value }),
  ]);
}

function activityBoxes(ctx) {
  const list = ctx.state.activityMonths || [];
  if (!list.length) return card(empty('🌤️', '这个月还没有活动记录，点下方 + 记一笔'));
  return frag(list.map((item) => activityBox(ctx, item)));
}

function activityBox(ctx, item) {
  const net = item.income - item.expense;
  const tone = net > 0 ? 'amount-in' : net < 0 ? 'amount-out' : '';
  return el('div', { class: 'card activity-box' }, [
    el('div', {
      class: 'activity-box-top tappable',
      onclick: () => openActivitySheet(ctx, item),
    }, [
      el('div', { class: 'activity-box-head' }, [
        el('span', { class: 'activity-box-name', text: item.name }),
        el('span', { class: 'row-chevron', text: '›' }),
      ]),
      el('div', { class: 'activity-box-meta' }, [
        el('span', { class: tone, text: `结余 ${money(net)}` }),
        el('span', { class: 'amount-out', text: `支出 ${money(item.expense)}` }),
      ]),
      activityBudgets(item),
    ]),
    activityPreview(ctx, item),
  ]);
}

function activityPreview(ctx, item) {
  const list = item.recent || [];
  if (!list.length) return null;
  return el('div', { class: 'activity-preview' }, list.map((tx) => entryRow(ctx, tx)));
}

function activityBudgets(item) {
  const bars = [];
  if (item.budget > 0) bars.push(activityBudget('本月上限', item.budget, item.used));
  if (item.total_budget > 0) bars.push(activityBudget('活动上限', item.total_budget, item.total_used));
  if (!bars.length) return null;
  return el('div', { class: 'activity-budgets' }, bars);
}

function activityBudget(label, cap, spent) {
  const remaining = cap - spent;
  const over = remaining < 0;
  const used = cap > 0 ? Math.min(spent / cap, 1) * 100 : 0;
  return el('div', { class: 'budget' }, [
    el('div', { class: 'budget-head' }, [
      el('span', { text: `${label} ${money(cap)}` }),
      el('span', { text: over ? `已超支 ${money(-remaining)}` : `还可用 ${money(remaining)}` }),
    ]),
    el('div', { class: 'budget-track' },
      el('div', { class: over ? 'budget-fill over' : 'budget-fill', style: { width: `${used}%` } })),
  ]);
}

let activitySheet = null;
let activityItem = null;

async function openActivitySheet(ctx, item) {
  if (activitySheet) activitySheet.close();
  ctx.state.ledgerActivity = item.id;
  activityItem = item;
  const results = el('div', { class: 'search-results' }, [card(empty('🌤️', '正在加载'))]);
  activitySheet = openSheet({
    title: item.name,
    body: [activitySheetSummary(item), results],
    onClose: () => {
      activitySheet = null;
      activityItem = null;
      if (ctx.state.ledgerActivity === item.id) ctx.state.ledgerActivity = 0;
    },
  });
  try {
    results.replaceChildren(entryList(ctx, await activityTxs(ctx, item.id), '这个月这个活动还没有记录'));
  } catch (error) {
    toast(error.message, true);
    activitySheet.close();
  }
}

export async function refreshActivitySheet(ctx) {
  if (!activitySheet || !activityItem) return;
  const item = (ctx.state.activityMonths || []).find((row) => row.id === activityItem.id);
  if (!item) {
    activitySheet.close();
    return;
  }
  activityItem = item;
  try {
    activitySheet.setBody([
      activitySheetSummary(item),
      el('div', { class: 'search-results' }, [entryList(ctx, await activityTxs(ctx, item.id), '这个月这个活动还没有记录')]),
    ]);
  } catch (error) {
    toast(error.message, true);
  }
}

function activitySheetSummary(item) {
  const net = item.income - item.expense;
  const tone = net > 0 ? 'amount-in' : net < 0 ? 'amount-out' : '';
  const bars = activityBudgets(item);
  return card([
    el('div', { class: 'fx-head' }, [
      el('span', { class: 'fx-code', text: '本月结余' }),
      el('span', { class: `fx-balance ${tone}`, text: money(net) }),
    ]),
    el('div', { class: 'fx-month' }, [
      el('span', { class: 'amount-out', text: `支出 ${money(item.expense)}` }),
      el('span', { class: 'amount-in', text: `收入 ${money(item.income)}` }),
    ]),
    bars ? el('div', { class: 'activity-sheet-budget' }, bars) : null,
  ]);
}

function activityTxs(ctx, id) {
  return api.transactions({
    month: ctx.state.month,
    user_id: ctx.state.statsMember || undefined,
    shared: ctx.state.statsShared ? 1 : undefined,
    activity_id: id,
  });
}

function entryList(ctx, list, vacant) {
  if (!list.length) return card(empty('🌤️', vacant));

  const days = [];
  for (const tx of list) {
    if (!days.length || days[days.length - 1].date !== tx.date) days.push({ date: tx.date, items: [] });
    days[days.length - 1].items.push(tx);
  }
  return frag(days.map((day) => frag([
    dayHeader(day),
    card(day.items.map((tx) => entryRow(ctx, tx))),
  ])));
}

function dayHeader(day) {
  let expense = 0;
  let income = 0;
  for (const tx of day.items) {
    if (tx.kind === 'expense') expense += tx.amount;
    if (tx.kind === 'income') income += tx.amount;
  }
  const sums = [];
  if (expense > 0) sums.push(el('span', { class: 'amount-out', text: `支出 ${money(expense)}` }));
  if (income > 0) sums.push(el('span', { class: 'amount-in', text: `收入 ${money(income)}` }));
  return el('div', { class: 'day-head' }, [
    el('span', { text: dayLabel(day.date) }),
    sums.length ? el('span', { class: 'day-head-sum' }, sums) : null,
  ]);
}

function entryRow(ctx, tx) {
  const transfer = tx.kind === 'transfer';
  const exchange = tx.kind === 'exchange';
  const title = transfer
    ? `${formatCard(tx.card_bank, tx.card_name, tx.card_last4)} → ${formatCard(tx.to_card_bank, tx.to_card_name, tx.to_card_last4)}`
    : exchange
      ? `${formatCard(tx.card_bank, tx.card_name, tx.card_last4)} 兑换`
      : (tx.note || tx.category_name);
  const value = exchange
    ? `${moneyOf(tx.amount, tx.currency)} → ${moneyOf(tx.to_amount, tx.to_currency)}`
    : signedMoney(tx.amount, tx.kind, tx.currency);
  return el('div', { class: 'row tappable', onclick: () => ctx.openEntry(tx) }, [
    el('div', {
      class: 'icon-badge',
      text: transfer ? '💳' : exchange ? '💱' : (tx.category_icon || '📦'),
      style: transfer || exchange ? undefined : { background: softColor(tx.category_color) },
    }),
    el('div', { class: 'row-main' }, [
      el('span', { class: 'row-title' }, [
        el('span', { text: title }),
        !ctx.state.statsShared && tx.shared ? el('span', { class: 'pill', text: '共同' }) : null,
      ]),
    ]),
    el('div', {
      class: transfer || exchange ? 'row-value muted' : (tx.kind === 'income' ? 'row-value amount-in' : 'row-value amount-out'),
      text: value,
    }),
    ctx.state.users.length > 1 && !ctx.state.statsMember ? memberAvatar(tx) : null,
  ]);
}

function memberAvatar(tx) {
  if (tx.shared) {
    const color = '#8e8e93';
    return el('i', {
      class: 'who', title: '共同', text: '臭',
      style: { background: softColor(color, 0.18), color },
    });
  }
  const color = memberColor(tx.user_id);
  return el('i', {
    class: 'who', title: tx.username, text: memberInitial(tx.username),
    style: { background: softColor(color, 0.18), color },
  });
}
