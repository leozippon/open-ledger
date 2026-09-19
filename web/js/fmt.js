import { el } from './dom.js';

const group = new Intl.NumberFormat('zh-CN');
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

export const CURRENCIES = [
  { code: 'CNY', label: 'RMB', symbol: '¥' },
  { code: 'HKD', label: 'HKD', symbol: 'HK$' },
  { code: 'USD', label: 'USD', symbol: '$' },
  { code: 'CAD', label: 'CAD', symbol: 'CA$' },
  { code: 'TWD', label: 'TWD', symbol: 'NT$' },
  { code: 'EUR', label: 'EUR', symbol: '€' },
  { code: 'GBP', label: 'GBP', symbol: '£' },
  { code: 'JPY', label: 'JPY', symbol: 'JP¥' },
  { code: 'AUD', label: 'AUD', symbol: 'A$' },
  { code: 'SGD', label: 'SGD', symbol: 'S$' },
];

function currencyOf(code) {
  return CURRENCIES.find((item) => item.code === code);
}

export function currencyLabel(code) {
  return currencyOf(code)?.label || code || 'RMB';
}

export function currencySymbol(code) {
  return currencyOf(code)?.symbol || code || '¥';
}

// money renders integer cents as 1,234.56. Ledger RMB omits ¥; card balances keep every symbol.
export function money(cents) {
  return moneyOf(cents, 'CNY');
}

export function moneyOf(cents, currency = 'CNY') {
  const symbol = !currency || currency === 'CNY' ? '' : currencySymbol(currency);
  return formatMoney(cents, symbol);
}

export function signedMoney(cents, kind, currency = 'CNY') {
  if (kind === 'transfer' || kind === 'exchange') return moneyOf(cents, currency);
  return `${kind === 'income' ? '+' : '-'}${moneyOf(cents, currency)}`;
}

export function fundLine(item) {
  const funds = item?.funds?.length ? item.funds : [{ currency: 'CNY', balance: item?.balance ?? 0 }];
  return funds.map((fund) => formatMoney(fund.balance, currencySymbol(fund.currency))).join('　');
}

function formatMoney(cents, symbol) {
  const sign = cents < 0 ? '-' : '';
  const value = Math.abs(cents);
  return `${sign}${symbol}${group.format(Math.floor(value / 100))}.${String(value % 100).padStart(2, '0')}`;
}

export const NETWORKS = [
  { code: 'unionpay', label: '银联' },
  { code: 'visa', label: 'Visa' },
  { code: 'mastercard', label: 'Mastercard' },
  { code: 'amex', label: '运通' },
];

function networkLabel(code) {
  return NETWORKS.find((item) => item.code === code)?.label || '';
}

export function cardMark(network) {
  if (!networkLabel(network)) return null;
  return el('span', {
    class: `card-mark card-mark-${network}`,
    role: 'img',
    'aria-label': networkLabel(network),
  }, el('img', { src: `/marks/${network}.svg`, alt: '' }));
}

function cardKindLabel(kind) {
  return kind === 'credit' ? '信用卡' : '储蓄卡';
}

export function formatCard(bank, name, last4) {
  const head = [bank, name].filter(Boolean).join(' ');
  if (!head) return '';
  return last4 ? `${head} · ${last4}` : head;
}

export function cardLabel(card) {
  if (!card?.bank) return '';
  return `${formatCard(card.bank, card.name, '')} ${cardKindLabel(card.kind)} · ${card.last4}`;
}

export function cardShort(card) {
  return formatCard(card?.bank, card?.name, card?.last4);
}

// parseAmount mirrors the server's parser so the keypad fails before saving.
export function parseAmount(text) {
  const raw = String(text).trim();
  if (raw === '') throw new Error('请输入金额');
  if (!/^\d+(\.\d{0,2})?$/.test(raw)) {
    if (/^\d+\.\d{3,}$/.test(raw)) throw new Error('金额最多保留两位小数');
    throw new Error('金额格式不正确');
  }
  const [whole, fraction = ''] = raw.split('.');
  const cents = Number(whole) * 100 + Number(fraction.padEnd(2, '0') || 0);
  if (cents <= 0) throw new Error('金额必须大于 0');
  if (cents > 10000000000) throw new Error('金额过大');
  return cents;
}

// centsToInput turns stored cents back into keypad text.
export function centsToInput(cents) {
  return (cents % 100 === 0) ? String(cents / 100) : (cents / 100).toFixed(2);
}

function pad(n) { return String(n).padStart(2, '0'); }

export function todayISO() {
  const now = new Date();
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

export function currentMonth() { return todayISO().slice(0, 7); }

export function currentYear() { return todayISO().slice(0, 4); }

export function shiftYear(year, delta) {
  return String(Number(year) + delta);
}

export function yearLabel(year) {
  return `${year}年`;
}

export function monthOf(date) { return date.slice(0, 7); }

export function shiftMonth(month, delta) {
  const [year, index] = month.split('-').map(Number);
  const moved = new Date(year, index - 1 + delta, 1);
  return `${moved.getFullYear()}-${pad(moved.getMonth() + 1)}`;
}

export function monthLabel(month) {
  const [year, index] = month.split('-');
  return `${year}年${Number(index)}月`;
}

export function dateLabel(date) {
  const [year, month, day] = date.split('-').map(Number);
  return `${year}年${month}月${day}日`;
}

export function daysInMonth(month) {
  const [year, index] = month.split('-').map(Number);
  return new Date(year, index, 0).getDate();
}

export function shortMonthLabel(month) {
  return `${Number(month.split('-')[1])}月`;
}

export function shortPeriodLabel(period) {
  if (period.length === 10) return `${Number(period.slice(8))}日`;
  if (period.length === 4) return `${period.slice(2)}年`;
  return shortMonthLabel(period);
}

export function periodLabel(period) {
  if (period.length === 10) {
    const [, month, day] = period.split('-').map(Number);
    return `${month}月${day}日`;
  }
  if (period.length === 4) return yearLabel(period);
  return monthLabel(period);
}

// dayLabel reads like 9月17日 周四, with today and yesterday spelled out.
export function dayLabel(date) {
  const [year, month, day] = date.split('-').map(Number);
  const at = new Date(year, month - 1, day);
  const today = todayISO();
  if (date === today) return `今天 ${month}月${day}日`;
  if (date === shiftDay(today, -1)) return `昨天 ${month}月${day}日`;
  return `${month}月${day}日 ${WEEKDAYS[at.getDay()]}`;
}

const MEMBER_COLORS = ['#4a9ef7', '#f2a33c', '#34c07a', '#a06ef0', '#f77ba8', '#4fc4b0'];

// memberColor keeps each member's avatar tint stable across renders.
export function memberColor(id) {
  return MEMBER_COLORS[Math.abs(Number(id) || 0) % MEMBER_COLORS.length];
}

// memberInitial is the single character shown inside a member avatar: the
// second code point of the username, which in a Chinese name is the given name
// rather than the surname the whole family shares. A one-character name shows
// that character.
export function memberInitial(username) {
  const chars = [...String(username || '')];
  return (chars[1] ?? chars[0] ?? '?').toUpperCase();
}

// softColor tints a category colour for icon backgrounds.
export function softColor(hex, alpha = 0.16) {
  if (!/^#[0-9a-fA-F]{6}$/.test(hex)) return 'var(--sunken)';
  const value = parseInt(hex.slice(1), 16);
  return `rgba(${(value >> 16) & 255}, ${(value >> 8) & 255}, ${value & 255}, ${alpha})`;
}

function shiftDay(date, delta) {
  const [year, month, day] = date.split('-').map(Number);
  const moved = new Date(year, month - 1, day + delta);
  return `${moved.getFullYear()}-${pad(moved.getMonth() + 1)}-${pad(moved.getDate())}`;
}
