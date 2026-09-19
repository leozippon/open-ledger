import { api } from '../api.js';
import { card, el, frag, segmented } from '../dom.js';
import { CURRENCIES, NETWORKS, cardLabel, cardMark, centsToInput, currencyLabel, currencySymbol, fundLine, memberColor, memberInitial, money, parseAmount, softColor } from '../fmt.js';
import { openAmountPad } from '../keypad.js';
import { ACCENTS, accentPreference, setAccentPreference, setThemePreference, themePreference } from '../theme.js';
import { confirmDialog, fieldRow, openSheet, toast } from '../ui.js';

const EMOJIS = [
  '💵', '💬', '🅰️', '💳', '🏦', '📱', '🍜', '🍱', '☕', '🍎',
  '🚇', '🚗', '⛽', '🛍️', '👕', '🏠', '💡', '🎮', '🎬', '💊',
  '📚', '✈️', '🐾', '🎁', '💰', '🎉', '📈', '🧑‍💻', '🪙', '📦',
];

const COLORS = [
  '#ff8a5c', '#f2a33c', '#f2b13c', '#34c07a', '#4fc4b0', '#5ac8e8',
  '#4a9ef7', '#7c86f5', '#a06ef0', '#f77ba8', '#ef6f6f', '#9aa0aa',
];

const STATES = [
  { value: 'active', label: '使用中' },
  { value: 'archived', label: '已归档' },
];

export function render(ctx) {
  return frag([
    el('div', { class: 'profile-head' }, el('h2', { text: '我的' })),
    profileCard(ctx),
    ctx.state.me?.is_admin ? membersCard(ctx) : null,
    settingsCard(ctx),
    appearanceCard(),
    dataCard(ctx),
  ]);
}

/* ── 当前成员 ─────────────────────────────────────────────────────────────── */

function profileCard(ctx) {
  const me = ctx.state.me;
  return card([
    el('div', { class: 'row' }, [
      avatar(me?.username, me?.id),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title' }, [
          me?.username ?? '—',
          me?.is_admin ? el('span', { class: 'pill', text: '管理员' }) : null,
        ]),
        el('span', { class: 'row-sub', text: `共 ${ctx.state.users.length} 位家庭成员` }),
      ]),
    ]),
    el('div', { class: 'row tappable', onclick: () => changeUsername(ctx) }, [
      el('div', { class: 'icon-badge', text: '✏️' }),
      el('div', { class: 'row-main' }, el('span', { class: 'row-title', text: '修改用户名' })),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
    el('div', { class: 'row tappable', onclick: () => changePassword() }, [
      el('div', { class: 'icon-badge', text: '🔑' }),
      el('div', { class: 'row-main' }, el('span', { class: 'row-title', text: '修改密码' })),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
  ]);
}

function usernameInput(value) {
  return el('input', {
    class: 'row-input', type: 'text', maxlength: 20, value,
    placeholder: '2 到 20 个字符',
    autocapitalize: 'none', spellcheck: 'false',
  });
}

function changeUsername(ctx) {
  const nameField = usernameInput(ctx.state.me?.username ?? '');
  const sheet = openSheet({
    title: '修改用户名',
    right: { text: '保存', onClick: save },
    body: [
      card([fieldRow('用户名', nameField)]),
      el('p', { class: 'note-text', text: '改名后用新用户名登录。已经记下的账还是你的，不用重新登录。' }),
    ],
  });

  async function save() {
    try {
      await api.renameUser(ctx.state.me.id, nameField.value);
      sheet.close();
      toast('已改名');
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function changePassword() {
  const oldInput = passwordInput('必填');
  const newInput = passwordInput('至少 8 位');
  const againInput = passwordInput('再次输入');
  const sheet = openSheet({
    title: '修改密码',
    right: { text: '保存', onClick: save },
    body: [
      card([fieldRow('当前密码', oldInput)]),
      card([fieldRow('新密码', newInput), fieldRow('确认', againInput)]),
      el('p', { class: 'note-text', text: '密码修改后需要重新登录，其他设备上的登录也会失效。' }),
    ],
  });

  async function save() {
    if (newInput.value !== againInput.value) {
      toast('两次输入的新密码不一致', true);
      return;
    }
    try {
      await api.changePassword(oldInput.value, newInput.value);
      sheet.close();
      toast('密码已修改，请重新登录');
      window.dispatchEvent(new Event('ledger:unauthorized'));
    } catch (error) {
      toast(error.message, true);
    }
  }
}

/* ── 家庭成员 ─────────────────────────────────────────────────────────────── */

function membersCard(ctx) {
  return card([
    el('div', { class: 'card-title' }, el('span', { text: '家庭成员' })),
    ...ctx.state.users.map((user) => el('div', {
      class: 'row tappable', onclick: () => editMember(ctx, user),
    }, [
      avatar(user.username, user.id),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title' }, [
          user.username,
          user.is_admin ? el('span', { class: 'pill', text: '管理员' }) : null,
        ]),
        el('span', { class: 'row-sub', text: `${user.entries} 笔记录` }),
      ]),
      el('div', { class: 'row-chevron', text: '›' }),
    ])),
    el('div', { class: 'row tappable', onclick: () => addMember(ctx) }, [
      el('div', { class: 'icon-badge', text: '＋' }),
      el('div', { class: 'row-main' }, el('span', { class: 'row-title muted', text: '添加成员' })),
    ]),
  ]);
}

function addMember(ctx) {
  const nameField = usernameInput('');
  const passField = passwordInput('至少 8 位');
  let isAdmin = false;
  const sheet = openSheet({ title: '添加成员', right: { text: '保存', onClick: save }, body: [] });
  paint();

  function paint() {
    sheet.setBody([
      card([fieldRow('用户名', nameField), fieldRow('初始密码', passField)]),
      card([el('div', { class: 'field' }, [
        el('span', { class: 'field-label', text: '权限' }),
        segmented(
          [{ value: 'member', label: '成员' }, { value: 'admin', label: '管理员' }],
          isAdmin ? 'admin' : 'member',
          (value) => { isAdmin = value === 'admin'; paint(); },
        ),
      ])]),
      el('p', { class: 'note-text', text: '成员可以记账、编辑全家的账目；管理员还能管理成员。请把初始密码交给对方，让 TA 登录后自行修改。' }),
    ]);
  }

  async function save() {
    try {
      await api.createUser({ username: nameField.value, password: passField.value, is_admin: isAdmin });
      sheet.close();
      toast('已添加');
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function editMember(ctx, user) {
  const isMe = ctx.state.me?.id === user.id;
  const nameField = usernameInput(user.username);
  const passField = passwordInput('至少 8 位');
  const sheet = openSheet({
    title: `成员：${user.username}`,
    right: { text: '保存', onClick: rename },
    body: [
      card([fieldRow('用户名', nameField)]),
      card([fieldRow('重置密码', passField)]),
      el('button', {
        class: 'btn btn-primary btn-block', type: 'button', text: '重置密码',
        style: { marginTop: '14px' }, onclick: reset,
      }),
      el('p', { class: 'note-text', text: '重置后该成员在所有设备上都需要用新密码重新登录。' }),
      isMe ? null : el('button', {
        class: 'btn btn-danger btn-block', type: 'button', text: '删除成员',
        style: { marginTop: '14px' }, onclick: remove,
      }),
    ],
  });

  async function rename() {
    try {
      await api.renameUser(user.id, nameField.value);
      sheet.close();
      toast('已改名');
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function reset() {
    try {
      await api.resetPassword(user.id, passField.value);
      sheet.close();
      toast('密码已重置');
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function remove() {
    if (!await confirmDialog({
      title: `删除成员「${user.username}」？`,
      message: '只有没有记过账的成员可以删除。',
    })) return;
    try {
      await api.deleteUser(user.id);
      sheet.close();
      toast('已删除');
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function avatar(username, id) {
  const color = memberColor(id);
  return el('div', {
    class: 'icon-badge', text: memberInitial(username),
    style: { background: softColor(color, 0.18), color, fontWeight: '600' },
  });
}

function passwordInput(placeholder) {
  return el('input', { class: 'row-input', type: 'password', placeholder, autocomplete: 'new-password' });
}

/* ── 分类与活动 ───────────────────────────────────────────────────────────── */

function settingsCard(ctx) {
  const activities = ctx.state.activities || [];
  return card([
    el('div', { class: 'row tappable', onclick: () => manageCategories(ctx) }, [
      el('div', { class: 'icon-badge', text: '🏷️' }),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title', text: '分类管理' }),
        el('span', { class: 'row-sub', text: `${ctx.state.categories.filter((c) => !c.archived).length} 个使用中` }),
      ]),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
    el('div', { class: 'row tappable', onclick: () => manageCards(ctx) }, [
      el('div', { class: 'icon-badge', text: '💳' }),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title', text: '银行卡' }),
        el('span', { class: 'row-sub', text: cardSummary(ctx) }),
      ]),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
    el('div', { class: 'row tappable', onclick: () => manageActivities(ctx) }, [
      el('div', { class: 'icon-badge', text: '🚩' }),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title', text: '活动' }),
        el('span', { class: 'row-sub', text: `${activities.length} 个` }),
      ]),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
  ]);
}

function appearanceCard() {
  const accent = accentPreference();
  return card([
    el('div', { class: 'field' }, [
      el('span', { class: 'field-label', text: '外观' }),
      segmented(
        [
          { value: 'auto', label: '自动' },
          { value: 'light', label: '浅色' },
          { value: 'dark', label: '深色' },
        ],
        themePreference(),
        (mode) => setThemePreference(mode),
      ),
    ]),
    el('div', { class: 'field accent-field' }, [
      el('span', { class: 'field-label', text: '主题色' }),
      el('div', { class: 'swatches accent-swatches' }, ACCENTS.map((item) => el('button', {
        class: 'swatch', type: 'button',
        style: { background: item.swatch },
        title: item.label,
        'aria-label': item.label,
        'aria-selected': item.id === accent ? 'true' : 'false',
        onclick: (event) => {
          if (event.currentTarget.getAttribute('aria-selected') === 'true') return;
          setAccentPreference(item.id);
          for (const button of event.currentTarget.parentElement.children) {
            button.setAttribute('aria-selected', button === event.currentTarget ? 'true' : 'false');
          }
        },
      }))),
    ]),
  ]);
}

function cardSummary(ctx) {
  const n = (ctx.state.cards || []).length;
  return n ? `${n} 张` : '未添加';
}

function manageCards(ctx) {
  let moving = false;
  const sheet = openSheet({ title: '银行卡', body: listBody() });

  function listBody() {
    const list = ctx.state.cards || [];
    const listCard = card([
      ...list.map((item) => cardRow(item, () => editCard(ctx, item, refresh))),
      el('div', { class: 'row tappable', onclick: () => editCard(ctx, null, refresh) }, [
        el('div', { class: 'icon-badge', text: '＋' }),
        el('div', { class: 'row-main' }, el('span', { class: 'row-title muted', text: '添加银行卡' })),
      ]),
    ]);
    bindRowDrag(listCard, commit);
    return [listCard];
  }

  function cardRow(item, onClick) {
    return el('div', {
      class: 'row tappable',
      'data-id': item.id,
      onclick: onClick,
    }, [
      el('div', { class: 'icon-badge', text: item.kind === 'credit' ? '💳' : '🏦' }),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title' }, [el('span', { text: cardLabel(item) }), cardMark(item.network)]),
        el('span', { class: 'row-sub', text: fundLine(item) }),
      ]),
      el('i', { class: 'order-mark', role: 'button', 'aria-label': '拖动排序' }, [el('span'), el('span'), el('span')]),
    ]);
  }

  async function commit(ids) {
    if (moving) return;
    moving = true;
    try {
      await api.reorderCards({ ids });
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
      sheet.setBody(listBody());
    } finally {
      moving = false;
    }
  }

  async function refresh() {
    await ctx.reload();
    sheet.setBody(listBody());
  }
}

function editCard(ctx, existing, onDone) {
  const draft = {
    kind: existing?.kind ?? 'debit',
    bank: existing?.bank ?? '',
    name: existing?.name ?? '',
    last4: existing?.last4 ?? '',
    network: existing?.network ?? '',
    funds: (existing?.funds?.length ? existing.funds : [{ currency: 'CNY', balance: existing?.balance ?? 0 }]).map((fund) => ({
      currency: fund.currency,
      balance: existing ? centsToInput(fund.balance) : '',
    })),
  };
  const bankInput = el('input', {
    class: 'row-input', type: 'text', maxlength: 16, placeholder: '银行名称', value: draft.bank,
    oninput: (event) => { draft.bank = event.target.value; },
  });
  const nameInput = el('input', {
    class: 'row-input', type: 'text', maxlength: 24, placeholder: '选填', value: draft.name,
    oninput: (event) => { draft.name = event.target.value; },
  });
  const last4Input = amountPick({
    getValue: () => draft.last4,
    setValue: (next) => { draft.last4 = next; },
    placeholder: '四位尾号',
    title: '尾号',
    maxLength: 4,
  });
  const fundStash = new Map(draft.funds.map((fund) => [fund.currency, fund.balance]));
  const fundInputs = new Map();
  const fundSlots = CURRENCIES.map((item) => {
    const input = amountPick({
      getValue: () => fundStash.get(item.code) ?? '',
      setValue: (next) => {
        fundStash.set(item.code, next);
        const fund = draft.funds.find((row) => row.currency === item.code);
        if (fund) fund.balance = next;
      },
      placeholder: '当前余额',
      title: item.label,
      symbol: currencySymbol(item.code),
    });
    fundInputs.set(item.code, input);
    return el('div', { class: 'fund-slot', 'data-currency': item.code, 'aria-hidden': 'true' },
      el('div', { class: 'fund-slot-inner' }, fieldRow(item.label, input)));
  });
  const fundChips = CURRENCIES.map((item) => el('button', {
    class: 'chip', type: 'button', text: item.label,
    onclick: () => toggleCurrency(item.code),
  }));
  const currencyBlock = el('div', { class: 'currency-add' }, [
    el('div', { class: 'field-label', text: '币种' }),
    el('div', { class: 'currency-add-chips' }, fundChips),
  ]);
  const form = card([]);
  const sheet = openSheet({
    title: existing ? '编辑银行卡' : '添加银行卡',
    right: { text: '保存', onClick: save },
    body: [
      form,
      existing ? el('button', {
        class: 'btn btn-danger btn-block', type: 'button', text: '删除',
        style: { marginTop: '14px' }, onclick: remove,
      }) : null,
    ],
  });
  paintForm();

  function selectedCodes() {
    return new Set(draft.funds.map((fund) => fund.currency));
  }

  function syncFunds(animate) {
    const selected = selectedCodes();
    fundChips.forEach((chip, index) => {
      chip.setAttribute('aria-selected', selected.has(CURRENCIES[index].code) ? 'true' : 'false');
    });
    fundSlots.forEach((slot) => {
      const on = selected.has(slot.dataset.currency);
      const input = fundInputs.get(slot.dataset.currency);
      slot.toggleAttribute('aria-hidden', !on);
      if (input) input.tabIndex = on ? 0 : -1;
      if (!animate) slot.style.transition = 'none';
      slot.classList.toggle('on', on);
      if (!animate) {
        void slot.offsetHeight;
        slot.style.transition = '';
      }
    });
  }

  function paintForm() {
    form.replaceChildren(
      fieldRow('类型', segmented(
        [{ value: 'debit', label: '储蓄卡' }, { value: 'credit', label: '信用卡' }],
        draft.kind,
        (kind) => { draft.kind = kind; },
      )),
      fieldRow('银行', bankInput),
      fieldRow('卡名', nameInput),
      fieldRow('尾号', last4Input),
      fieldRow('卡组织', el('div', { class: 'entry-cards network-picks' }, NETWORKS.map((item) => el('button', {
        class: 'chip chip-mark', type: 'button',
        'aria-selected': draft.network === item.code ? 'true' : 'false',
        onclick: () => {
          draft.network = draft.network === item.code ? '' : item.code;
          paintForm();
        },
      }, [cardMark(item.code)])))),
      ...fundSlots,
      currencyBlock,
    );
    syncFunds(false);
  }

  function toggleCurrency(code) {
    const index = draft.funds.findIndex((fund) => fund.currency === code);
    if (index >= 0) {
      if (draft.funds.length === 1) {
        toast('至少保留一种币种', true);
        return;
      }
      draft.funds.splice(index, 1);
    } else {
      draft.funds.push({ currency: code, balance: fundStash.get(code) ?? '' });
    }
    syncFunds(true);
  }

  async function save() {
    const bank = draft.bank.trim();
    const last4 = draft.last4.trim();
    if (!bank) return toast('请填写银行', true);
    if (!/^\d{4}$/.test(last4)) return toast('请填写四位尾号', true);
    const funds = [];
    for (const fund of draft.funds) {
      let cents = 0;
      const raw = String(fund.balance ?? '').trim();
      if (raw !== '' && !/^0*(\.0{0,2})?$/.test(raw)) {
        try {
          cents = parseAmount(raw);
        } catch (error) {
          toast(`${currencyLabel(fund.currency)} ${error.message}`, true);
          return;
        }
      }
      funds.push({ currency: fund.currency, balance: cents });
    }
    const body = { kind: draft.kind, bank, name: draft.name.trim(), last4, network: draft.network, archived: false, funds, sort_order: existing?.sort_order ?? 0 };
    try {
      if (existing) await api.updateCard(existing.id, body);
      else await api.createCard(body);
      sheet.close();
      toast('已保存');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function remove() {
    if (!await confirmDialog({ title: '删除这张卡？', message: '有账目的卡不能删除。' })) return;
    try {
      await api.deleteCard(existing.id);
      sheet.close();
      toast('已删除');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function manageCategories(ctx) {
  let kind = 'expense';
  let moving = false;
  const sheet = openSheet({ title: '分类管理', body: listBody() });

  function listBody() {
    const list = ctx.state.categories.filter((item) => item.kind === kind);
    const active = list.filter((item) => !item.archived);
    const archived = list.filter((item) => item.archived);
    const listCard = card([
      ...active.map((item) => categoryRow(item, true, () => editCategory(ctx, item, kind, refresh))),
      ...archived.map((item) => categoryRow(item, false, () => editCategory(ctx, item, kind, refresh))),
      el('div', { class: 'row tappable', onclick: () => editCategory(ctx, null, kind, refresh) }, [
        el('div', { class: 'icon-badge', text: '＋' }),
        el('div', { class: 'row-main' }, el('span', { class: 'row-title muted', text: '添加分类' })),
      ]),
    ]);
    bindRowDrag(listCard, commit);
    return [
      el('div', { class: 'stats-switch' }, segmented(
        [{ value: 'expense', label: '支出' }, { value: 'income', label: '收入' }],
        kind,
        (next) => { kind = next; paint(); },
      )),
      listCard,
    ];
  }

  function paint() {
    sheet.setBody(listBody());
  }

  async function commit(ids) {
    if (moving) return;
    moving = true;
    try {
      await api.reorderCategories({ kind, ids });
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
      paint();
    } finally {
      moving = false;
    }
  }

  async function refresh() {
    await ctx.reload();
    paint();
  }
}

function categoryRow(item, draggable, onEdit) {
  return el('div', {
    class: 'row tappable',
    'data-id': draggable ? item.id : null,
    onclick: onEdit,
  }, [
    el('div', { class: 'icon-badge', text: item.icon || '📦', style: { background: softColor(item.color) } }),
    el('div', { class: 'row-main' }, el('span', { class: 'row-title' }, [
      item.name,
      item.archived ? el('span', { class: 'pill', text: '已归档' }) : null,
    ])),
    draggable
      ? el('i', { class: 'order-mark', role: 'button', 'aria-label': '拖动排序' }, [el('span'), el('span'), el('span')])
      : el('i', { class: 'dot', style: { background: item.color } }),
  ]);
}

// Drag from the grip only. Neighbors slide with translateY and the DOM
// stays put until drop, so a swap cannot flicker the displaced rows.
function bindRowDrag(card, onCommit) {
  card.addEventListener('pointerdown', (event) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return;
    const handle = event.target.closest('.order-mark');
    if (!handle || !card.contains(handle)) return;
    const row = handle.closest('[data-id]');
    if (!row || !card.contains(row)) return;
    event.preventDefault();
    event.stopPropagation();
    beginRowDrag(card, row, event, onCommit);
  });
}

function beginRowDrag(card, row, event, onCommit) {
  const scroller = card.closest('.sheet-body');
  const layer = row.closest('.sheet');
  const pointerId = event.pointerId;
  const rows = [...card.querySelectorAll('[data-id]')];
  const from = rows.indexOf(row);
  const originIds = rows.map((node) => Number(node.dataset.id));
  const startY = event.clientY;
  const home = row.getBoundingClientRect();
  const cardTop = () => card.getBoundingClientRect().top;
  const slots = rows.map((node) => {
    const box = node.getBoundingClientRect();
    return { rel: box.top - cardTop(), height: box.height };
  });
  const slack = 8;
  let over = from;
  let moved = false;
  const hole = el('div', { class: 'order-hole', 'aria-hidden': 'true' });
  hole.style.height = `${home.height}px`;
  row.after(hole);
  row.classList.add('dragging');
  Object.assign(row.style, {
    position: 'fixed',
    width: `${home.width}px`,
    margin: '0',
    zIndex: '5',
    transition: 'none',
  });
  const pin = (clientY) => {
    const box = layer.getBoundingClientRect();
    row.style.top = `${clientY - box.top}px`;
    row.style.left = `${home.left - box.left}px`;
  };
  pin(home.top);
  handleCapture(row, pointerId);
  try { navigator.vibrate?.(12); } catch { /* optional */ }

  const top = (index) => cardTop() + slots[index].rel;
  const bottom = (index) => top(index) + slots[index].height;

  // Same idea as dnd-kit's verticalListSortingStrategy: keep DOM order,
  // slide neighbors with translateY, and only commit the new order on drop.
  function pickOver(y) {
    if (over === from) {
      if (from + 1 < rows.length && y > bottom(from)) return from + 1;
      if (from > 0 && y < top(from)) return from - 1;
      return from;
    }
    if (over > from) {
      if (over + 1 < rows.length && y > bottom(over)) return over + 1;
      if (y < top(over) - slack) return over - 1;
      return over;
    }
    if (over > 0 && y < top(over)) return over - 1;
    if (y > bottom(over) + slack) return over + 1;
    return over;
  }

  function paintShift(next) {
    if (next === over) return;
    over = next;
    for (let i = 0; i < rows.length; i++) {
      if (i === from) continue;
      let offset = 0;
      if (from < over && i > from && i <= over) offset = -home.height;
      else if (over < from && i >= over && i < from) offset = home.height;
      rows[i].style.transition = 'transform var(--dur-sheet) var(--ease)';
      rows[i].style.transform = offset ? `translateY(${offset}px)` : '';
    }
  }

  const move = (next) => {
    if (next.pointerId !== pointerId) return;
    next.preventDefault();
    const y = next.clientY;
    if (Math.abs(y - startY) > 4) moved = true;
    pin(home.top + y - startY);
    paintShift(pickOver(y));
    if (scroller) {
      const box = scroller.getBoundingClientRect();
      if (y < box.top + 48) scroller.scrollTop -= 12;
      if (y > box.bottom - 48) scroller.scrollTop += 12;
    }
  };

  const stop = (next) => {
    if (next && next.pointerId !== pointerId) return;
    window.removeEventListener('pointermove', move, true);
    window.removeEventListener('pointerup', stop, true);
    window.removeEventListener('pointercancel', stop, true);
    hole.remove();
    for (const node of rows) {
      if (node === row) continue;
      node.style.transition = '';
      node.style.transform = '';
    }
    row.classList.remove('dragging');
    row.removeAttribute('style');
    if (over !== from) {
      if (over < from) rows[over].before(row);
      else rows[over].after(row);
    }
    row.addEventListener('click', (click) => click.stopPropagation(), { capture: true, once: true });
    const nextIds = [...card.querySelectorAll('[data-id]')].map((node) => Number(node.dataset.id));
    if (moved && nextIds.join() !== originIds.join()) onCommit(nextIds);
  };

  window.addEventListener('pointermove', move, { capture: true, passive: false });
  window.addEventListener('pointerup', stop, true);
  window.addEventListener('pointercancel', stop, true);
}

function handleCapture(row, pointerId) {
  try {
    row.setPointerCapture(pointerId);
  } catch {
    // Some browsers reject capture if the pointer is already gone.
  }
}

function editCategory(ctx, category, kind, onDone) {
  const draft = {
    name: category?.name ?? '',
    kind: category?.kind ?? kind,
    icon: category?.icon ?? '📦',
    color: category?.color ?? COLORS[0],
    sort_order: category?.sort_order ?? 0,
    archived: category?.archived ?? false,
  };
  const sheet = openSheet({
    title: category ? '编辑分类' : '添加分类',
    right: { text: '保存', onClick: save },
    body: form(),
  });

  function form() {
    const repaint = () => sheet.setBody(form());
    return [
      card([fieldRow('名称', nameInput(draft, '例如：早餐'))]),
      iconCard(draft, repaint),
      card([
        el('div', { class: 'field' }, [
          el('span', { class: 'field-label', text: '颜色' }),
          el('div', { class: 'swatches' }, COLORS.map((color) => el('button', {
            class: 'swatch', type: 'button', 'aria-label': color,
            'aria-selected': color === draft.color ? 'true' : 'false',
            style: { background: color },
            onclick: () => { draft.color = color; repaint(); },
          }))),
        ]),
      ]),
      category ? stateCard(draft, repaint) : null,
      category ? el('button', {
        class: 'btn btn-danger btn-block', type: 'button', text: '删除分类',
        style: { marginTop: '14px' }, onclick: remove,
      }) : null,
    ];
  }

  async function save() {
    try {
      const body = { ...draft, name: draft.name.trim() };
      if (category) await api.updateCategory(category.id, body);
      else await api.createCategory(body);
      sheet.close();
      toast('已保存');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function remove() {
    if (!await confirmDialog({ title: `删除分类「${category.name}」？`, message: '只有没有账目的分类可以删除。' })) return;
    try {
      await api.deleteCategory(category.id);
      sheet.close();
      toast('已删除');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

function manageActivities(ctx) {
  let moving = false;
  const sheet = openSheet({ title: '活动', body: listBody() });

  function listBody() {
    const list = ctx.state.activities || [];
    const listCard = card([
      ...list.map((item) => activityRow(item, () => editActivity(ctx, item, refresh))),
      el('div', { class: 'row tappable', onclick: () => editActivity(ctx, null, refresh) }, [
        el('div', { class: 'icon-badge', text: '＋' }),
        el('div', { class: 'row-main' }, el('span', { class: 'row-title muted', text: '添加活动' })),
      ]),
    ]);
    bindRowDrag(listCard, commit);
    return [listCard];
  }

  async function commit(ids) {
    if (moving) return;
    moving = true;
    try {
      await api.reorderActivities({ ids });
      await ctx.reload();
    } catch (error) {
      toast(error.message, true);
      sheet.setBody(listBody());
    } finally {
      moving = false;
    }
  }

  async function refresh() {
    await ctx.reload();
    sheet.setBody(listBody());
  }
}

function activityRow(item, onClick) {
  return el('div', {
    class: 'row tappable',
    'data-id': item.id,
    onclick: onClick,
  }, [
    el('div', { class: 'row-main' }, [
      el('span', { class: 'row-title', text: item.name }),
      item.is_default ? el('span', { class: 'row-sub', text: '默认' }) : null,
    ]),
    el('div', { class: 'row-sub', text: activityCapLine(item) }),
    el('i', { class: 'order-mark', role: 'button', 'aria-label': '拖动排序' }, [el('span'), el('span'), el('span')]),
  ]);
}

function activityCapLine(item) {
  const parts = [];
  if (item.budget > 0) parts.push(`月 ${money(item.budget)}`);
  if (item.total_budget > 0) parts.push(`活动 ${money(item.total_budget)}`);
  return parts.length ? parts.join(' · ') : '不限';
}

function editActivity(ctx, existing, onDone) {
  const draft = {
    name: existing?.name ?? '',
    budget: existing?.budget > 0 ? centsToInput(existing.budget) : '',
    totalBudget: existing?.total_budget > 0 ? centsToInput(existing.total_budget) : '',
  };
  const nameInput = el('input', {
    class: 'row-input', type: 'text', maxlength: 20, placeholder: '活动名称', value: draft.name,
    oninput: (event) => { draft.name = event.target.value; },
  });
  const budgetInput = amountPick({
    getValue: () => draft.budget,
    setValue: (next) => { draft.budget = next; },
    placeholder: '0 表示不设上限',
    title: '每月上限',
    symbol: '¥',
  });
  const totalInput = amountPick({
    getValue: () => draft.totalBudget,
    setValue: (next) => { draft.totalBudget = next; },
    placeholder: '0 表示不设上限',
    title: '活动上限',
    symbol: '¥',
  });
  const form = card([
    fieldRow('名称', nameInput),
    fieldRow('每月上限', budgetInput),
    fieldRow('活动上限', totalInput),
  ]);
  const sheet = openSheet({
    title: existing ? '编辑活动' : '添加活动',
    right: { text: '保存', onClick: save },
    body: [
      form,
      el('p', { class: 'note-text', text: '每月上限只看当月全家支出；活动上限看这个活动的全部支出。都不按账本拆分。日常生活不能删除。' }),
      existing && !existing.is_default ? el('button', {
        class: 'btn btn-danger btn-block', type: 'button', text: '删除',
        style: { marginTop: '14px' }, onclick: remove,
      }) : null,
    ],
  });

  async function save() {
    let budget = 0;
    let totalBudget = 0;
    try {
      budget = parseCap(draft.budget);
      totalBudget = parseCap(draft.totalBudget);
    } catch (error) {
      toast(error.message, true);
      return;
    }
    const body = {
      name: draft.name.trim(),
      budget,
      total_budget: totalBudget,
    };
    try {
      if (existing) await api.updateActivity(existing.id, body);
      else await api.createActivity(body);
      sheet.close();
      toast('已保存');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function remove() {
    if (!await confirmDialog({ title: '删除这个活动？', message: '有账单的活动需要先改挂到别的活动。' })) return;
    try {
      await api.deleteActivity(existing.id);
      sheet.close();
      toast('已删除');
      await onDone();
    } catch (error) {
      toast(error.message, true);
    }
  }
}

/* ── 数据 ─────────────────────────────────────────────────────────────────── */

function dataCard(ctx) {
  return card([
    el('a', { class: 'row tappable', href: '/api/export.csv' }, [
      el('div', { class: 'icon-badge', text: '📤' }),
      el('div', { class: 'row-main' }, [
        el('span', { class: 'row-title', text: '导出 CSV' }),
        el('span', { class: 'row-sub', text: '全部账目，可用 Excel 打开' }),
      ]),
      el('div', { class: 'row-chevron', text: '›' }),
    ]),
    el('div', { class: 'row tappable', onclick: () => logout(ctx) }, [
      el('div', { class: 'icon-badge', text: '🚪' }),
      el('div', { class: 'row-main' }, el('span', { class: 'row-title', style: { color: 'var(--danger)' }, text: '退出登录' })),
    ]),
  ]);
}

async function logout(ctx) {
  if (!await confirmDialog({ title: '退出登录？', confirmText: '退出' })) return;
  await ctx.logout();
}

/* ── Shared pieces ────────────────────────────────────────────────────────── */

function parseCap(raw) {
  const text = String(raw || '').trim();
  if (text === '' || /^0*(\.0{0,2})?$/.test(text)) return 0;
  return parseAmount(text);
}

function amountPick({ getValue, setValue, placeholder, title, symbol, maxLength }) {
  const btn = el('button', { class: 'row-input row-pick', type: 'button' });
  const paint = () => {
    const value = getValue();
    btn.classList.toggle('placeholder', !value);
    btn.textContent = value || placeholder;
  };
  btn.addEventListener('click', () => openAmountPad({
    title: title || placeholder,
    value: getValue(),
    symbol,
    maxLength,
    onDone: (next) => { setValue(next); paint(); },
  }));
  paint();
  return btn;
}

function nameInput(draft, placeholder) {
  return el('input', {
    class: 'row-input', type: 'text', maxlength: 12, placeholder, value: draft.name,
    oninput: (event) => { draft.name = event.target.value; },
  });
}

function iconCard(draft, repaint) {
  return card([
    el('div', { class: 'field' }, [
      el('span', { class: 'field-label', text: `图标 ${draft.icon}` }),
      el('div', { class: 'emoji-picks' }, EMOJIS.map((emoji) => el('button', {
        class: 'emoji-pick', type: 'button', text: emoji,
        'aria-selected': emoji === draft.icon ? 'true' : 'false',
        onclick: () => { draft.icon = emoji; repaint(); },
      }))),
    ]),
  ]);
}

function stateCard(draft, repaint) {
  return card([
    el('div', { class: 'field' }, [
      el('span', { class: 'field-label', text: '状态' }),
      segmented(STATES, draft.archived ? 'archived' : 'active', (value) => {
        draft.archived = value === 'archived';
        repaint();
      }),
    ]),
  ]);
}
