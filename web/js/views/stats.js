import { donut, lines } from '../charts.js';
import { card, el, empty, frag, segmented } from '../dom.js';
import { cardShort, daysInMonth, money, periodLabel } from '../fmt.js';

const KINDS = [
  { value: 'expense', label: '支出' },
  { value: 'income', label: '收入' },
];

export function render(ctx) {
  const { statsKind, statsMember, statsShared, statsGrain, month, statsSummary, statsTrend } = ctx.state;
  const yearly = statsGrain === 'year';
  const allTime = statsGrain === 'all';
  const summary = statsSummary;
  const trend = yearly || allTime ? statsTrend ?? [] : monthDays(month, summary?.days);
  const expense = statsKind === 'expense';
  const slices = (expense ? summary?.expense_by_category : summary?.income_by_category) ?? [];
  const total = expense ? summary?.expense ?? 0 : summary?.income ?? 0;
  const who = statsShared ? null : ctx.state.users.find((user) => user.id === statsMember)?.username;
  const kindLabel = statsShared ? (expense ? '共同支出' : '共同收入') : (expense ? '支出' : '收入');
  const when = allTime ? '全部' : yearly ? '本年' : '本月';
  const emptyWhen = allTime ? '到现在' : yearly ? '这一年' : '这个月';

  return frag([
    el('div', { class: 'stats-switch' }, segmented(KINDS, statsKind, (kind) => {
      ctx.state.statsKind = kind;
      ctx.render();
    })),
    breakdownCard(slices, total, kindLabel, who, expense, emptyWhen, when),
    cardBreakdown(expense ? summary?.expense_by_card : summary?.income_by_card, total, expense),
    trendCard(trend, yearly || allTime, expense),
  ]);
}

// The selected chip already names the book, so the chart keeps a short caption.
function breakdownCard(slices, total, kindLabel, who, expense, emptyWhen, when) {
  if (!slices.length) return card(empty('📊', `${emptyWhen}${who ?? ''}还没有${kindLabel}`));
  return card([
    el('div', { class: 'chart-wrap' }, donut(slices, { caption: `${when}${kindLabel}`, total })),
    el('div', { class: 'legend' }, slices.map((slice) => el('div', { class: 'legend-row' }, [
      el('i', { class: 'dot', style: { background: slice.color } }),
      el('span', { class: 'legend-name', text: `${slice.icon} ${slice.name}`.trim() }),
      el('span', { class: 'legend-pct', text: `${((slice.amount / total) * 100).toFixed(1)}%` }),
      el('span', { class: `legend-amt ${expense ? 'amount-out' : 'amount-in'}`, text: money(slice.amount) }),
    ]))),
  ]);
}

function cardBreakdown(slices, total, expense) {
  const banks = (slices ?? []).filter((item) => item.card_id);
  if (!banks.length) return null;
  return card([
    el('div', { class: 'card-title' }, el('span', { text: expense ? '银行消费' : '银行卡收入' })),
    el('div', { class: 'legend' }, banks.map((item) => el('div', { class: 'legend-row' }, [
      el('span', { class: 'legend-name', text: cardShort(item) }),
      total ? el('span', { class: 'legend-pct', text: `${((item.amount / total) * 100).toFixed(1)}%` }) : null,
      el('span', { class: `legend-amt ${expense ? 'amount-out' : 'amount-in'}`, text: money(item.amount) }),
    ]))),
  ]);
}

function monthDays(month, days) {
  const byDate = new Map((days ?? []).map((row) => [row.date, row]));
  return Array.from({ length: daysInMonth(month) }, (_, index) => {
    const date = `${month}-${String(index + 1).padStart(2, '0')}`;
    const row = byDate.get(date);
    return { month: date, income: row?.income ?? 0, expense: row?.expense ?? 0 };
  });
}

function peakIndex(trend, key) {
  let best = -1;
  let peak = 0;
  trend.forEach((point, index) => {
    if (point[key] >= peak && point[key] > 0) {
      peak = point[key];
      best = index;
    }
  });
  return best;
}

function trendCard(trend, yearly, expense) {
  const key = expense ? 'expense' : 'income';
  const title = '走势';
  const active = trend.some((point) => point[key] > 0);
  if (!active) {
    return card([
      el('div', { class: 'card-title' }, el('span', { text: title })),
      empty('📈', '还没有足够的数据'),
    ]);
  }

  const chartWrap = el('div', { class: 'trend' });
  const detail = el('div', { class: 'trend-detail' });
  let selected = peakIndex(trend, key);

  function paintDetail(point) {
    detail.replaceChildren(
      el('span', { class: 'trend-when', text: periodLabel(point.month) }),
      el('strong', { class: expense ? 'amount-out' : 'amount-in', text: money(point[key]) }),
    );
  }

  function paintChart() {
    chartWrap.replaceChildren(lines(trend, {
      selected,
      focus: key,
      onSelect: (point, index) => {
        selected = index;
        paintDetail(point);
        paintChart();
      },
    }));
  }
  paintDetail(trend[selected]);
  paintChart();

  return card([
    el('div', { class: 'card-title' }, el('span', { text: title })),
    detail,
    chartWrap,
  ]);
}
