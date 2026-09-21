import { api } from '../api.js';
import { card, el, empty, segmented } from '../dom.js';
import { CURRENCIES, cardMark, cardShort, centsToInput, currencyLabel, currencySymbol, dateLabel, parseAmount, softColor, todayISO } from '../fmt.js';
import { applyKey, keypad } from '../keypad.js';
import { confirmDialog, fieldRow, openDatePicker, openSheet, toast } from '../ui.js';

const KINDS = [
  { value: 'expense', label: '支出' },
  { value: 'income', label: '收入' },
];
const TRANSFER = { value: 'transfer', label: '转账' };
const EXCHANGE = { value: 'exchange', label: '兑换' };

// openEntry shows the record sheet, either empty or editing an existing entry.
export function openEntry(ctx, existing = null) {
  const draft = {
    kind: existing?.kind ?? 'expense',
    amount: existing ? centsToInput(existing.amount) : '',
    toAmount: existing?.to_amount ? centsToInput(existing.to_amount) : '',
    categoryId: existing?.category_id || null,
    cardId: existing?.card_id || 0,
    toCardId: existing?.to_card_id || 0,
    date: existing?.date ?? todayISO(),
    note: existing?.note ?? '',
    shared: existing ? !!existing.shared : !!ctx.state.statsShared,
    currency: existing?.currency || 'CNY',
    toCurrency: existing?.to_currency || 'HKD',
    fxSide: 'sell',
    activityId: existing?.activity_id || defaultActivityId(ctx, existing),
  };
  if (draft.kind === 'exchange' && draft.currency === draft.toCurrency) draft.toCurrency = otherCurrency(draft.currency);
  if (!existing && draft.kind !== 'transfer' && draft.kind !== 'exchange') draft.categoryId = firstCategory(ctx, draft.kind)?.id ?? null;

  const kindBox = el('div', { class: 'kind-switch' });
  const amountBox = el('div', { class: 'entry-amount' });
  const pickerBox = el('div');
  const metaBox = el('div');
  let saving = false;

  const dateInput = el('button', {
    class: 'row-input row-pick', type: 'button', text: dateLabel(draft.date),
    onclick: () => openDatePicker(draft.date, (next) => {
      draft.date = next;
      dateInput.textContent = dateLabel(next);
    }),
  });
  const noteInput = el('input', {
    class: 'row-input', type: 'text', maxlength: 100, placeholder: '添加备注', value: draft.note,
    oninput: (event) => { draft.note = event.target.value; },
  });

  const sheet = openSheet({
    title: kindBox,
    right: existing ? { text: '删除', danger: true, onClick: remove } : null,
    tight: true,
    body: [
      amountBox,
      pickerBox,
      metaBox,
    ],
    footer: keypad(press, save),
  });

  paintKind();
  paintAmount();
  paintPicker();
  paintMeta();
  holdHeight();

  function holdHeight() {
    const layer = sheet.bodyEl.closest('.sheet');
    requestAnimationFrame(() => {
      const next = Math.ceil(layer.getBoundingClientRect().height);
      const prev = parseFloat(layer.style.minHeight) || 0;
      if (next > prev) layer.style.minHeight = `${next}px`;
    });
  }

  function paintKind() {
    const cards = usableCards(ctx, 0);
    const options = [...KINDS];
    if (cards.length >= 2 || draft.kind === 'transfer') options.push(TRANSFER);
    if (cards.length >= 1 || draft.kind === 'exchange') options.push(EXCHANGE);
    kindBox.replaceChildren(segmented(options, draft.kind, (kind) => {
      draft.kind = kind;
      if (kind === 'transfer') {
        draft.shared = false;
        draft.categoryId = null;
        if (!cards.some((item) => item.id === draft.cardId)) draft.cardId = cards[0]?.id || 0;
        if (!cards.some((item) => item.id === draft.toCardId) || draft.toCardId === draft.cardId) {
          draft.toCardId = cards.find((item) => item.id !== draft.cardId)?.id || 0;
        }
      } else if (kind === 'exchange') {
        draft.shared = false;
        draft.categoryId = null;
        draft.toCardId = 0;
        draft.fxSide = 'sell';
        if (!cards.some((item) => item.id === draft.cardId)) draft.cardId = cards[0]?.id || 0;
        if (draft.currency === draft.toCurrency) draft.toCurrency = otherCurrency(draft.currency);
      } else if (!categoryOf(ctx, draft.categoryId, kind)) {
        draft.categoryId = firstCategory(ctx, kind)?.id ?? null;
        draft.toCardId = 0;
      }
      paintKind();
      paintAmount();
      paintPicker();
      paintMeta();
      holdHeight();
    }));
  }

  function paintAmount() {
    const currency = draft.kind === 'exchange' && draft.fxSide === 'buy' ? draft.toCurrency : draft.currency;
    const value = draft.kind === 'exchange' && draft.fxSide === 'buy' ? draft.toAmount : draft.amount;
    const children = [];
    if (draft.kind === 'exchange') {
      children.push(el('div', { class: 'entry-fx' }, [
        el('button', {
          class: 'chip', type: 'button',
          'aria-selected': draft.fxSide === 'sell' ? 'true' : 'false',
          text: `卖出 ${currencyLabel(draft.currency)}`,
          onclick: () => { draft.fxSide = 'sell'; paintAmount(); },
        }),
        el('button', {
          class: 'chip', type: 'button',
          'aria-selected': draft.fxSide === 'buy' ? 'true' : 'false',
          text: `买入 ${currencyLabel(draft.toCurrency)}`,
          onclick: () => { draft.fxSide = 'buy'; paintAmount(); },
        }),
      ]));
    }
    children.push(el('span', { class: 'sym', text: currencySymbol(currency) }));
    children.push(el('span', { class: value === '' ? 'val placeholder' : 'val', text: value === '' ? '0.00' : value }));
    amountBox.replaceChildren(...children);
  }

  function paintPicker() {
    if (draft.kind === 'transfer') {
      const cards = usableCards(ctx, draft.cardId || draft.toCardId);
      if (cards.length < 2) {
        pickerBox.replaceChildren(card(empty('💳', '至少两张银行卡才能转账，先去「我的」里添加')));
      } else {
        pickerBox.replaceChildren();
      }
      return;
    }
    if (draft.kind === 'exchange') {
      const cards = usableCards(ctx, draft.cardId);
      if (!cards.length) {
        pickerBox.replaceChildren(card(empty('💳', '先去「我的」里添加银行卡')));
      } else {
        pickerBox.replaceChildren();
      }
      return;
    }
    const list = ctx.state.categories.filter(
      (item) => item.kind === draft.kind && (!item.archived || item.id === draft.categoryId));
    if (!list.length) {
      pickerBox.replaceChildren(card(empty('🏷️', '还没有分类，先去「我的」里添加')));
      return;
    }
    pickerBox.replaceChildren(card([
      el('div', { class: 'card-title' }, el('span', { text: '分类' })),
      el('div', { class: 'cat-grid' }, list.map((item) => el('button', {
        class: 'cat-cell', type: 'button',
        'aria-selected': item.id === draft.categoryId ? 'true' : 'false',
        onclick: () => { draft.categoryId = item.id; paintPicker(); },
      }, [
        el('span', { class: 'emoji', text: item.icon || '📦', style: { background: softColor(item.color) } }),
        el('span', { text: item.name }),
      ]))),
    ]));
  }

  function paintMeta() {
    const rows = [fieldRow('日期', dateInput), fieldRow('备注', noteInput)];
    if (draft.kind === 'transfer') {
      const cards = usableCards(ctx, draft.cardId || draft.toCardId);
      if (cards.length >= 2) {
        rows.push(
          fieldRow('转出', cardSelect(cards, draft.cardId, false, (id) => {
            draft.cardId = id;
            if (draft.toCardId === id) draft.toCardId = cards.find((item) => item.id !== id)?.id || 0;
            paintMeta();
          })),
          fieldRow('转入', cardSelect(cards, draft.toCardId, false, (id) => {
            draft.toCardId = id;
            if (draft.cardId === id) draft.cardId = cards.find((item) => item.id !== id)?.id || 0;
            paintMeta();
          })),
          fieldRow('货币', currencySelect(draft.currency, (code) => {
            draft.currency = code;
            paintAmount();
            paintMeta();
          })),
        );
      }
    } else if (draft.kind === 'exchange') {
      const cards = usableCards(ctx, draft.cardId);
      if (cards.length) {
        rows.push(
          fieldRow('银行卡', cardSelect(cards, draft.cardId, false, (id) => {
            draft.cardId = id;
            paintMeta();
          })),
          fieldRow('卖出', currencySelect(draft.currency, (code) => {
            draft.currency = code;
            if (draft.toCurrency === code) draft.toCurrency = otherCurrency(code);
            paintAmount();
            paintMeta();
          })),
          fieldRow('买入', currencySelect(draft.toCurrency, (code) => {
            draft.toCurrency = code;
            if (draft.currency === code) draft.currency = otherCurrency(code);
            paintAmount();
            paintMeta();
          })),
        );
      }
    } else {
      const cards = usableCards(ctx, draft.cardId);
      if (cards.length) {
        rows.push(fieldRow('银行卡', cardSelect(cards, draft.cardId, true, (id) => {
          draft.cardId = id;
          paintMeta();
        })));
      }
      const activities = ctx.state.activities || [];
      if (activities.length) {
        rows.push(fieldRow('活动', activitySelect(activities, draft.activityId, (id) => {
          draft.activityId = id;
          paintMeta();
        })));
      }
      rows.push(fieldRow('货币', currencySelect(draft.currency, (code) => {
        draft.currency = code;
        paintAmount();
        paintMeta();
      })));
    }
    if (draft.kind !== 'transfer' && draft.kind !== 'exchange' && ctx.state.users.length > 1) {
      rows.push(fieldRow('分担', segmented([
        { value: 'self', label: '个人' },
        { value: 'shared', label: '共同' },
      ], draft.shared ? 'shared' : 'self', (value) => {
        draft.shared = value === 'shared';
        paintMeta();
      })));
    }
    metaBox.replaceChildren(card(rows, 'entry-meta'));
  }

  function amountField() {
    return draft.kind === 'exchange' && draft.fxSide === 'buy' ? 'toAmount' : 'amount';
  }

  function press(key) {
    const field = amountField();
    draft[field] = applyKey(draft[field], key);
    paintAmount();
  }

  async function save() {
    if (saving) return;
    let amount;
    try {
      amount = parseAmount(draft.amount);
    } catch (error) {
      toast(error.message, true);
      return;
    }
    if (!draft.date) return toast('请选择日期', true);
    let toAmount = 0;
    if (draft.kind === 'exchange') {
      try {
        toAmount = parseAmount(draft.toAmount);
      } catch (error) {
        toast(`买入 ${error.message}`, true);
        return;
      }
    }
    if (draft.kind === 'transfer') {
      if (!draft.cardId || !draft.toCardId) return toast('请选择转出和转入的卡', true);
      if (draft.cardId === draft.toCardId) return toast('转出和转入不能是同一张卡', true);
    } else if (draft.kind === 'exchange') {
      if (!draft.cardId) return toast('请选择银行卡', true);
      if (draft.currency === draft.toCurrency) return toast('买入和卖出不能是同一种货币', true);
    } else if (!draft.categoryId) {
      return toast('请选择分类', true);
    }

    const body = {
      kind: draft.kind,
      amount,
      date: draft.date,
      note: draft.note,
      category_id: draft.kind === 'transfer' || draft.kind === 'exchange' ? 0 : draft.categoryId,
      card_id: draft.cardId || 0,
      to_card_id: draft.kind === 'transfer' ? draft.toCardId : 0,
      shared: draft.kind !== 'transfer' && draft.kind !== 'exchange' && !!draft.shared,
      currency: draft.currency,
      to_amount: draft.kind === 'exchange' ? toAmount : 0,
      to_currency: draft.kind === 'exchange' ? draft.toCurrency : '',
      activity_id: draft.kind === 'transfer' || draft.kind === 'exchange' ? 0 : draft.activityId,
    };
    saving = true;
    try {
      const saved = existing
        ? await api.updateTransaction(existing.id, body)
        : await api.createTransaction(body);
      sheet.close();
      toast(existing ? '已更新' : '已记下');
      await ctx.afterSave(saved);
    } catch (error) {
      toast(error.message, true);
    } finally {
      saving = false;
    }
  }

  async function remove() {
    if (!await confirmDialog({ title: '删除这笔账目？', message: '删除后无法恢复。' })) return;
    try {
      await api.deleteTransaction(existing.id);
      sheet.close();
      toast('已删除');
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

export function recognizeRow(onDone) {
  return el('div', { class: 'recognize-row' }, [
    recognizeOpen('sms', '短信', '粘贴入账', () => fromSms(onDone)),
    recognizeOpen('pic', '识图', '拍照入账', () => fromImage(onDone)),
  ]);
}

function recognizeOpen(kind, title, hint, onClick) {
  return el('button', { class: `recognize-open recognize-${kind}`, type: 'button', onclick: onClick }, [
    el('span', { class: 'recognize-mark', 'aria-hidden': true }, recognizeIcon(kind)),
    el('span', { class: 'recognize-copy' }, [
      el('span', { class: 'recognize-title', text: title }),
      el('span', { class: 'recognize-hint', text: hint }),
    ]),
  ]);
}

function recognizeIcon(kind) {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('class', 'recognize-icon');
  svg.setAttribute('viewBox', '0 0 24 24');
  const paths = kind === 'sms'
    ? ['M4 7.4A3.4 3.4 0 0 1 7.4 4h9.2A3.4 3.4 0 0 1 20 7.4v6.2A3.4 3.4 0 0 1 16.6 17H8.4L4.6 19.7c-.46.32-1.1-.08-.96-.62L4 16.8V7.4z']
    : [
      'M4.6 9.2A2.2 2.2 0 0 1 6.8 7h1.7l1.15-1.55A1.2 1.2 0 0 1 10.6 5h2.8c.37 0 .72.17.95.45L15.5 7h1.7a2.2 2.2 0 0 1 2.2 2.2v7.1a2.2 2.2 0 0 1-2.2 2.2H6.8a2.2 2.2 0 0 1-2.2-2.2z',
      'M12 15.4a2.6 2.6 0 1 0 0-5.2 2.6 2.6 0 0 0 0 5.2z',
    ];
  for (const d of paths) {
    const path = document.createElementNS(ns, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}

export async function commitRecognized(ctx, got) {
  const entries = Array.isArray(got?.entries) && got.entries.length
    ? got.entries
    : (got?.kind ? [got] : []);
  if (!entries.length) throw new Error('没有识别到账目');
  let saved = null;
  let n = 0;
  try {
    for (const item of entries) {
      saved = await api.createTransaction({
        kind: item.kind,
        amount: item.amount,
        date: item.date,
        note: item.note ?? '',
        category_id: item.category_id,
        activity_id: item.activity_id || 0,
        card_id: item.card_id || 0,
        shared: !!item.shared,
      });
      n += 1;
    }
    toast(n === 1 ? '已记下' : `已记下 ${n} 笔`);
  } catch (error) {
    if (!n) throw error;
    toast(`记下了 ${n} 笔，后面失败：${error.message}`, true);
  }
  if (saved) await ctx.afterSave(saved);
}

let busy = false;

async function submitRecognize(body, onDone) {
  if (busy) return false;
  busy = true;
  setRecognizeBusy(true);
  toast('正在识别…', false, true);
  try {
    await onDone(await api.recognize(body));
    return true;
  } catch (error) {
    toast(error.message, true);
    return false;
  } finally {
    busy = false;
    setRecognizeBusy(false);
  }
}

function setRecognizeBusy(on) {
  for (const row of document.querySelectorAll('.recognize-row')) {
    row.classList.toggle('busy', on);
    for (const button of row.querySelectorAll('button')) button.disabled = on;
  }
}

async function fromSms(onDone) {
  let text = '';
  if (!navigator.clipboard?.readText) {
    openSmsSheet(onDone, '');
    return;
  }
  try {
    text = (await navigator.clipboard.readText()).trim();
  } catch {
    // iOS shows a paste chip; tapping elsewhere rejects and must not
    // flash an empty compose sheet.
    return;
  }
  if (!text) {
    toast('请先复制短信');
    return;
  }
  openSmsSheet(onDone, text);
}

function openSmsSheet(onDone, text) {
  const box = el('textarea', {
    class: 'recognize-text', rows: 8, maxlength: 2000,
    placeholder: '粘贴短信',
    value: text,
  });
  const sheet = openSheet({
    title: '短信',
    right: { text: '记下', onClick: run },
    body: [card(box, 'recognize-compose')],
  });
  requestAnimationFrame(() => box.focus());

  async function run() {
    const next = box.value.trim();
    if (!next) {
      toast('请粘贴短信', true);
      return;
    }
    if (await submitRecognize({ text: next }, onDone)) sheet.close();
  }
}

function fromImage(onDone) {
  const input = el('input', { class: 'recognize-file', type: 'file', accept: 'image/*' });
  input.addEventListener('change', () => {
    const picked = input.files?.[0];
    input.remove();
    if (!picked) return;
    if (picked.size > 2 * 1024 * 1024) {
      toast('图片不能超过 2 MB', true);
      return;
    }
    const reader = new FileReader();
    reader.onerror = () => toast('图片读取失败', true);
    reader.onload = () => {
      const image = String(reader.result ?? '');
      if (!image) {
        toast('图片读取失败', true);
        return;
      }
      submitRecognize({ image }, onDone);
    };
    reader.readAsDataURL(picked);
  });
  document.body.append(input);
  input.click();
}

function defaultActivityId(ctx, existing) {
  if (existing && (existing.kind === 'transfer' || existing.kind === 'exchange')) return 0;
  if (ctx.state.ledgerActivity) return ctx.state.ledgerActivity;
  if (ctx.state.statsActivity) return ctx.state.statsActivity;
  return (ctx.state.activities || []).find((item) => item.is_default)?.id || 0;
}

function activitySelect(activities, selected, onPick) {
  return el('div', { class: 'entry-cards' }, activities.map((item) => el('button', {
    class: 'chip', type: 'button',
    'aria-selected': item.id === selected ? 'true' : 'false',
    text: item.name,
    onclick: () => onPick(item.id),
  })));
}

function firstCategory(ctx, kind) {
  return ctx.state.categories.find((item) => item.kind === kind && !item.archived) ?? null;
}

function usableCards(ctx, keepId) {
  return (ctx.state.cards || []).filter((item) => !item.archived || item.id === keepId);
}

function currencySelect(selected, onPick) {
  return el('div', { class: 'entry-cards' }, CURRENCIES.map((item) => el('button', {
    class: 'chip', type: 'button',
    'aria-selected': item.code === selected ? 'true' : 'false',
    text: item.label,
    onclick: () => onPick(item.code),
  })));
}

function otherCurrency(code) {
  return code === 'CNY' ? 'HKD' : 'CNY';
}

function cardSelect(cards, selected, allowNone, onPick) {
  const options = allowNone ? [{ id: 0, bank: '不选', last4: '' }, ...cards] : cards;
  return el('div', { class: 'entry-cards' }, options.map((item) => el('button', {
    class: 'chip', type: 'button',
    'aria-selected': item.id === selected ? 'true' : 'false',
    onclick: () => onPick(item.id),
  }, item.id === 0 ? '不选' : [el('span', { text: cardShort(item) }), cardMark(item.network)])));
}

function categoryOf(ctx, id, kind) {
  return ctx.state.categories.find((item) => item.id === id && item.kind === kind) ?? null;
}
