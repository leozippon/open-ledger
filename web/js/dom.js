// Minimal element builder: el('div', { class: 'card' }, [child, '文本'])
export function el(tag, props = {}, children = []) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === null || value === undefined || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key === 'style') Object.assign(node.style, value);
    else if (key === 'value') node.value = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  append(node, children);
  return node;
}

export function append(node, children) {
  for (const child of [].concat(children)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child);
  }
  return node;
}

export function frag(children) {
  return append(document.createDocumentFragment(), children);
}

export function card(children, cls = '') {
  return el('div', { class: cls ? `card ${cls}` : 'card' }, children);
}

export function empty(mark, text) {
  return el('div', { class: 'empty' }, [
    el('div', { class: 'empty-mark', text: mark }),
    el('p', { text }),
  ]);
}

// segmented builds an iOS-style switch; options is [{ value, label }].
export function segmented(options, selected, onSelect) {
  return el('div', { class: 'segmented', role: 'tablist' },
    options.map((option) => el('button', {
      type: 'button',
      role: 'tab',
      'aria-selected': option.value === selected ? 'true' : 'false',
      'data-value': option.value,
      text: option.label,
      onclick: (event) => {
        if (event.currentTarget.getAttribute('aria-selected') === 'true') return;
        for (const button of event.currentTarget.parentElement.children) {
          button.setAttribute('aria-selected', button === event.currentTarget ? 'true' : 'false');
        }
        onSelect(option.value);
      },
    })));
}
