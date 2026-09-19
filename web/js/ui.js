import { el, append } from './dom.js';
import { dateLabel, daysInMonth, monthLabel, monthOf, shiftMonth, shiftYear, todayISO, yearLabel } from './fmt.js';

const layers = document.getElementById('layers');
const toastEl = document.getElementById('toast');
const viewEl = document.getElementById('view');
let openLayers = 0;
let lockScroll = 0;
let toastTimer = 0;
const keyboardSheets = [];

// A soft keyboard shrinks the visual viewport by far more than a collapsing
// browser toolbar does, which is how the two are told apart.
const KEYBOARD_MIN = 120;
const WEEK_HEAD = ['日', '一', '二', '三', '四', '五', '六'];
const SHEET_MS = 450;

function lock() {
  if (openLayers === 0) {
    lockScroll = viewEl ? viewEl.scrollTop : 0;
    document.documentElement.classList.add('locked');
    document.body.classList.add('locked');
    viewEl?.classList.add('locked');
  }
  openLayers += 1;
}

function unlock() {
  openLayers = Math.max(0, openLayers - 1);
  if (openLayers === 0) {
    document.documentElement.classList.remove('locked');
    document.body.classList.remove('locked');
    if (viewEl) {
      viewEl.classList.remove('locked');
      viewEl.scrollTop = lockScroll;
    }
  }
}

// Scroll only inside the sheet body — never call scrollIntoView on the field,
// which on iOS pans the whole page and hides the sheet header. The visible
// bottom is the visual viewport, so a field under the IME can still be reached
// without moving the sheet.
function scrollFieldIntoView(container, field) {
  if (!container.contains(field)) return;
  const pad = 16;
  const viewport = window.visualViewport;
  const cRect = container.getBoundingClientRect();
  const fRect = field.getBoundingClientRect();
  const top = Math.max(cRect.top, viewport ? viewport.offsetTop : cRect.top);
  const bottom = Math.min(cRect.bottom, viewport ? viewport.offsetTop + viewport.height : cRect.bottom);
  if (fRect.top < top + pad) {
    container.scrollTop -= top + pad - fRect.top;
  } else if (fRect.bottom > bottom - pad) {
    container.scrollTop += fRect.bottom - (bottom - pad);
  }
}

function keyboardCovered() {
  const viewport = window.visualViewport;
  if (!viewport) return 0;
  return Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop);
}

// followKeyboard never moves the sheet. Height is locked to the resting size
// captured while the IME is down, so a shrinking visual viewport cannot clamp
// it. Extra bottom padding is absorbed inside that box. On dismiss, only the
// padding eases out — unlocking a pixel height in place is the jump.
function followKeyboard(sheet) {
  const viewport = window.visualViewport;
  const body = sheet.querySelector('.sheet-body');
  if (!viewport || !body) return () => {};
  keyboardSheets.push(sheet);
  let frame = 0;
  let settle = 0;
  let restHeight = 0;
  let kbOpen = false;

  const measureRest = () => {
    if (sheet.style.height) return;
    const height = sheet.getBoundingClientRect().height;
    if (height) restHeight = height;
  };

  const lockBox = () => {
    measureRest();
    if (!restHeight) return;
    sheet.style.height = `${restHeight}px`;
    sheet.style.maxHeight = `${restHeight}px`;
  };

  const apply = () => {
    if (keyboardSheets[keyboardSheets.length - 1] !== sheet) return;
    const field = document.activeElement;
    const focused = field instanceof HTMLElement
      && body.contains(field)
      && field.matches('input, textarea, select');
    // Blur happens before the keyboard finishes closing; use the viewport.
    // Stay open until the IME is actually gone, not merely below KEYBOARD_MIN.
    const covered = keyboardCovered();
    if (focused && covered > KEYBOARD_MIN) kbOpen = true;
    else if (covered < 24) kbOpen = false;
    const kb = kbOpen ? covered : 0;
    if (kb) {
      lockBox();
      body.style.paddingBottom = `${kb}px`;
      if (focused) scrollFieldIntoView(body, field);
      return;
    }
    body.style.paddingBottom = '';
    measureRest();
  };
  const fit = () => {
    cancelAnimationFrame(frame);
    clearTimeout(settle);
    // The candidate bar keeps resizing for a moment; wait until it settles
    // so the sheet does not jump twice.
    settle = setTimeout(() => {
      frame = requestAnimationFrame(apply);
    }, 80);
  };
  const ro = new ResizeObserver(() => measureRest());
  ro.observe(sheet);
  requestAnimationFrame(() => requestAnimationFrame(apply));
  viewport.addEventListener('resize', fit);
  viewport.addEventListener('scroll', fit);
  sheet.addEventListener('focusin', fit);
  sheet.addEventListener('focusout', fit);
  return () => {
    cancelAnimationFrame(frame);
    clearTimeout(settle);
    ro.disconnect();
    viewport.removeEventListener('resize', fit);
    viewport.removeEventListener('scroll', fit);
    sheet.removeEventListener('focusin', fit);
    sheet.removeEventListener('focusout', fit);
    const index = keyboardSheets.lastIndexOf(sheet);
    if (index >= 0) keyboardSheets.splice(index, 1);
  };
}

export function toast(message, bad = false) {
  toastEl.textContent = message;
  toastEl.classList.toggle('bad', bad);
  toastEl.hidden = false;
  requestAnimationFrame(() => toastEl.classList.add('on'));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toastEl.classList.remove('on');
    setTimeout(() => { toastEl.hidden = true; }, SHEET_MS);
  }, 2200);
}

// openSheet slides a panel up from the bottom and returns a handle so callers
// can refresh its body or close it after saving. title may be an element when a
// control belongs in the header, as the record sheet does with its type switch.
export function openSheet({ title, left, right, body, footer, tight = false, onClose }) {
  const scrim = el('div', { class: 'scrim', onclick: () => close() });
  const bodyEl = el('div', { class: tight ? 'sheet-body tight' : 'sheet-body' }, body);
  const sheet = el('div', { class: 'sheet', role: 'dialog', 'aria-modal': 'true' }, [
    el('div', { class: 'sheet-head' }, [
      el('button', { type: 'button', text: left?.text ?? '取消', onclick: left?.onClick ?? (() => close()) }),
      typeof title === 'string' ? el('h2', { text: title }) : title,
      right
        ? el('button', { class: right.danger ? 'right del' : 'right', type: 'button', text: right.text, onclick: right.onClick })
        : el('span'),
    ]),
    bodyEl,
    footer,
  ]);

  sheet.addEventListener('focusin', (event) => {
    const field = event.target;
    if (!(field instanceof HTMLElement) || !field.matches('input, textarea, select')) return;
    scrollFieldIntoView(bodyEl, field);
  });

  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    document.removeEventListener('keydown', onKey);
    stopFollowing();
    onClose?.();
    scrim.classList.remove('on');
    sheet.classList.remove('on');
    // Tear down after the slide, not before: clearing a locked height or
    // unlocking the page while a near-full sheet is still on screen flashes
    // the backdrop (category management is tall enough to show it).
    setTimeout(() => {
      scrim.remove();
      sheet.remove();
      unlock();
    }, SHEET_MS);
  }

  function onKey(event) {
    if (event.key === 'Escape') close();
  }

  layers.append(scrim, sheet);
  lock();
  const stopFollowing = followKeyboard(sheet);
  document.addEventListener('keydown', onKey);
  requestAnimationFrame(() => { scrim.classList.add('on'); sheet.classList.add('on'); });
  return {
    close,
    bodyEl,
    setBody: (next) => {
      bodyEl.replaceChildren();
      append(bodyEl, next);
    },
  };
}

export function confirmDialog({ title, message, confirmText = '删除', danger = true }) {
  return new Promise((resolve) => {
    const finish = (answer) => {
      document.removeEventListener('keydown', onKey);
      scrim.classList.remove('on');
      dialog.classList.remove('on');
      setTimeout(() => { scrim.remove(); dialog.remove(); }, SHEET_MS);
      unlock();
      resolve(answer);
    };
    const onKey = (event) => { if (event.key === 'Escape') finish(false); };
    const scrim = el('div', { class: 'scrim', onclick: () => finish(false) });
    const dialog = el('div', { class: 'dialog', role: 'dialog', 'aria-modal': 'true' }, [
      el('h3', { text: title }),
      message ? el('p', { text: message }) : null,
      el('div', { class: 'dialog-actions' }, [
        el('button', { class: 'btn btn-ghost', type: 'button', text: '取消', onclick: () => finish(false) }),
        el('button', {
          class: danger ? 'btn btn-danger' : 'btn btn-primary',
          type: 'button', text: confirmText, onclick: () => finish(true),
        }),
      ]),
    ]);
    layers.append(scrim, dialog);
    lock();
    document.addEventListener('keydown', onKey);
    requestAnimationFrame(() => { scrim.classList.add('on'); dialog.classList.add('on'); });
  });
}

function monthNav(month, onChange, { pickable = false, open } = {}) {
  const label = pickable
    ? el('button', {
      class: 'month-pick', type: 'button', text: monthLabel(month),
      onclick: () => (open || (() => openMonthPicker(month, onChange)))(),
    })
    : el('span', { class: 'month-pick', text: monthLabel(month) });
  return el('div', { class: 'month-switch', 'data-month': month }, [
    el('button', { type: 'button', 'aria-label': '上个月', text: '‹', onclick: () => onChange(shiftMonth(month, -1)) }),
    label,
    el('button', { type: 'button', 'aria-label': '下个月', text: '›', onclick: () => onChange(shiftMonth(month, 1)) }),
  ]);
}

export function monthSwitch(month, onChange, open) {
  return monthNav(month, onChange, { pickable: true, open });
}

export function yearSwitch(year, onChange, open) {
  return el('div', { class: 'month-switch', 'data-year': year }, [
    el('button', { type: 'button', 'aria-label': '上一年', text: '‹', onclick: () => onChange(shiftYear(year, -1)) }),
    el('button', {
      class: 'month-pick', type: 'button', text: yearLabel(year),
      onclick: () => (open || (() => openYearPicker(year, onChange)))(),
    }),
    el('button', { type: 'button', 'aria-label': '下一年', text: '›', onclick: () => onChange(shiftYear(year, 1)) }),
  ]);
}

export function openStatsPeriodPicker({ grain, month, year, onMonth, onYear }) {
  const mode = grain === 'year' ? 'year' : 'month';
  let cursor = Number(mode === 'year' ? year : month.split('-')[0]);
  const selectedMonth = month;
  const selectedYear = year;
  const sheet = openSheet({ title: '选择时间', tight: true, body: [] });

  function paint() {
    if (cursor < 1000) cursor = 1000;
    if (cursor > 9999) cursor = 9999;
    const yearStart = mode === 'year' ? cursor - 11 : cursor;
    sheet.setBody([
      el('div', { class: 'month-switch' }, [
        el('button', {
          type: 'button', 'aria-label': mode === 'year' ? '更早' : '上一年', text: '‹',
          onclick: () => { cursor -= mode === 'year' ? 12 : 1; paint(); },
        }),
        el('span', {
          class: 'month-pick',
          text: mode === 'year' ? `${yearStart}–${yearStart + 11}年` : `${cursor}年`,
        }),
        el('button', {
          type: 'button', 'aria-label': mode === 'year' ? '更晚' : '下一年', text: '›',
          onclick: () => { cursor += mode === 'year' ? 12 : 1; paint(); },
        }),
      ]),
      el('div', { class: 'cal-months' }, mode === 'year'
        ? Array.from({ length: 12 }, (_, index) => {
          const value = String(yearStart + index);
          return el('button', {
            class: 'cal-cell', type: 'button', text: yearLabel(value),
            'aria-selected': value === selectedYear ? 'true' : 'false',
            onclick: () => { onYear(value); sheet.close(); },
          });
        })
        : Array.from({ length: 12 }, (_, index) => {
          const value = `${cursor}-${String(index + 1).padStart(2, '0')}`;
          return el('button', {
            class: 'cal-cell', type: 'button', text: `${index + 1}月`,
            'aria-selected': value === selectedMonth ? 'true' : 'false',
            onclick: () => { onMonth(value); sheet.close(); },
          });
        })),
    ]);
  }

  paint();
}

function openYearPicker(year, onPick) {
  let start = Number(year) - 11;
  const selected = year;
  const sheet = openSheet({ title: '选择年份', tight: true, body: [] });

  function paint() {
    if (start < 1000) start = 1000;
    if (start > 9988) start = 9988;
    sheet.setBody([
      el('div', { class: 'month-switch' }, [
        el('button', { type: 'button', 'aria-label': '更早', text: '‹', onclick: () => { start -= 12; paint(); } }),
        el('span', { class: 'month-pick', text: `${start}–${start + 11}年` }),
        el('button', { type: 'button', 'aria-label': '更晚', text: '›', onclick: () => { start += 12; paint(); } }),
      ]),
      el('div', { class: 'cal-months' }, Array.from({ length: 12 }, (_, index) => {
        const value = String(start + index);
        return el('button', {
          class: 'cal-cell', type: 'button', text: yearLabel(value),
          'aria-selected': value === selected ? 'true' : 'false',
          onclick: () => { onPick(value); sheet.close(); },
        });
      })),
    ]);
  }

  paint();
}

function openMonthPicker(month, onPick) {
  let year = Number(month.split('-')[0]);
  const selected = month;
  const sheet = openSheet({ title: '选择月份', tight: true, body: [] });

  function paint() {
    sheet.setBody([
      el('div', { class: 'month-switch' }, [
        el('button', { type: 'button', 'aria-label': '上一年', text: '‹', onclick: () => { year -= 1; paint(); } }),
        el('span', { class: 'month-pick', text: `${year}年` }),
        el('button', { type: 'button', 'aria-label': '下一年', text: '›', onclick: () => { year += 1; paint(); } }),
      ]),
      el('div', { class: 'cal-months' }, Array.from({ length: 12 }, (_, index) => {
        const value = `${year}-${String(index + 1).padStart(2, '0')}`;
        return el('button', {
          class: 'cal-cell', type: 'button', text: `${index + 1}月`,
          'aria-selected': value === selected ? 'true' : 'false',
          onclick: () => { onPick(value); sheet.close(); },
        });
      })),
    ]);
  }

  paint();
}

export function openDatePicker(date, onPick) {
  let view = monthOf(date);
  const today = todayISO();
  const sheet = openSheet({ title: '选择日期', tight: true, body: [] });

  function paint() {
    const [year, index] = view.split('-').map(Number);
    const first = new Date(year, index - 1, 1).getDay();
    const count = daysInMonth(view);
    const cells = [
      ...WEEK_HEAD.map((label) => el('span', { class: 'cal-dow', text: label })),
      ...Array.from({ length: first }, () => el('span', { class: 'cal-cell muted' })),
      ...Array.from({ length: count }, (_, offset) => {
        const value = `${view}-${String(offset + 1).padStart(2, '0')}`;
        return el('button', {
          class: 'cal-cell', type: 'button', text: String(offset + 1),
          'aria-selected': value === date ? 'true' : 'false',
          'data-today': value === today ? '' : null,
          onclick: () => { onPick(value); sheet.close(); },
        });
      }),
    ];
    sheet.setBody([
      monthNav(view, (next) => { view = next; paint(); }),
      el('div', { class: 'cal-days' }, cells),
      el('button', {
        class: 'cal-today', type: 'button', text: `今天 ${dateLabel(today)}`,
        onclick: () => { onPick(today); sheet.close(); },
      }),
    ]);
  }

  paint();
}

// fieldRow lays out a label with a control aligned to the right edge.
export function fieldRow(label, control) {
  return append(el('div', { class: 'row' }, el('span', { class: 'row-label', text: label })), control);
}
