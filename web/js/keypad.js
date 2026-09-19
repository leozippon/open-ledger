import { el } from './dom.js';
import { openSheet } from './ui.js';

export function applyKey(text, key, { maxLength } = {}) {
  const raw = String(text ?? '');
  if (key === 'back') return raw.slice(0, -1);
  if (maxLength) {
    if (key === '.' || raw.length >= maxLength) return raw;
    return raw + key;
  }
  if (key === '.') return raw.includes('.') ? raw : `${raw || '0'}.`;
  const [whole, fraction] = raw.split('.');
  if (raw.includes('.')) {
    if ((fraction ?? '').length >= 2) return raw;
  } else if (whole.length >= 8) {
    return raw;
  }
  return raw === '0' ? key : raw + key;
}

export function keypad(press, save, { digitsOnly } = {}) {
  const key = (label, cls, handler) => el('button', { class: cls, type: 'button', text: label, onclick: handler });
  return el('div', { class: 'keypad' }, [
    key('1', '', () => press('1')),
    key('2', '', () => press('2')),
    key('3', '', () => press('3')),
    key('⌫', '', () => press('back')),
    key('4', '', () => press('4')),
    key('5', '', () => press('5')),
    key('6', '', () => press('6')),
    key('完成', 'k-save', save),
    key('7', '', () => press('7')),
    key('8', '', () => press('8')),
    key('9', '', () => press('9')),
    digitsOnly ? el('div', { class: 'k-empty' }) : key('.', '', () => press('.')),
    key('0', 'k-wide', () => press('0')),
  ]);
}

export function openAmountPad({ title, value = '', symbol = '', maxLength, onDone }) {
  let amount = String(value ?? '');
  const display = el('div', { class: 'entry-amount' });
  const sheet = openSheet({
    title,
    tight: true,
    body: [display],
    footer: keypad((key) => {
      amount = applyKey(amount, key, { maxLength });
      paint();
    }, finish, { digitsOnly: Boolean(maxLength) }),
  });
  paint();

  function paint() {
    const empty = amount === '';
    const parts = [];
    if (symbol) parts.push(el('span', { class: 'sym', text: symbol }));
    parts.push(el('span', {
      class: empty ? 'val placeholder' : 'val',
      text: empty ? (maxLength ? '0'.repeat(maxLength) : '0.00') : amount,
    }));
    display.replaceChildren(...parts);
  }

  function finish() {
    sheet.close();
    onDone(amount);
  }
}
