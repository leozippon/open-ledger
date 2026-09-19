import { money, shortPeriodLabel } from './fmt.js';

const NS = 'http://www.w3.org/2000/svg';

// Presentation attributes do not resolve var(), so themed colours go through
// the style attribute instead.
function node(tag, attrs = {}, children = []) {
  const element = document.createElementNS(NS, tag);
  for (const [key, value] of Object.entries(attrs)) element.setAttribute(key, value);
  for (const child of [].concat(children)) if (child) element.append(child);
  return element;
}

function label(x, y, text, style, size) {
  return node('text', { x, y, 'text-anchor': 'middle', 'font-size': size, style }, [document.createTextNode(text)]);
}

// donut draws one ring per slice using dash offsets, newest first.
export function donut(slices, { caption, total, size = 188, thickness = 22 }) {
  const center = size / 2;
  const radius = (size - thickness) / 2;
  const ring = 2 * Math.PI * radius;
  const sum = slices.reduce((acc, slice) => acc + slice.amount, 0);
  const svg = node('svg', { viewBox: `0 0 ${size} ${size}`, width: size, height: size });
  const rotated = node('g', { transform: `rotate(-90 ${center} ${center})` });
  rotated.append(node('circle', {
    cx: center, cy: center, r: radius, fill: 'none',
    'stroke-width': thickness, style: 'stroke: var(--sunken)',
  }));

  const gap = slices.length > 1 ? 2.5 : 0;
  let offset = 0;
  for (const slice of slices) {
    const length = sum > 0 ? (slice.amount / sum) * ring : 0;
    if (length <= 0) continue;
    const drawn = Math.max(length - gap, 1);
    rotated.append(node('circle', {
      cx: center, cy: center, r: radius, fill: 'none',
      stroke: slice.color, 'stroke-width': thickness,
      'stroke-dasharray': `${drawn} ${ring - drawn}`,
      'stroke-dashoffset': -offset,
    }));
    offset += length;
  }
  svg.append(rotated);
  svg.append(label(center, center - 4, caption, 'fill: var(--muted)', 12));
  svg.append(label(center, center + 18, money(total), 'fill: var(--text); font-weight: 600', 17));
  return svg;
}

function token(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

function showTick(index, selected, count) {
  if (count <= 12) return true;
  return index === 0 || index === count - 1 || index === selected;
}

function n(value) {
  return Math.round(value * 100) / 100;
}

function column(x, y, width, height, radius) {
  const r = Math.min(radius, width / 2, height);
  const x2 = x + width;
  const y2 = y + height;
  if (r <= 0) return `M${n(x)} ${n(y2)}H${n(x2)}V${n(y)}H${n(x)}Z`;
  return `M${n(x)} ${n(y2)}V${n(y + r)}Q${n(x)} ${n(y)} ${n(x + r)} ${n(y)}H${n(x2 - r)}Q${n(x2)} ${n(y)} ${n(x2)} ${n(y + r)}V${n(y2)}Z`;
}

// lines draws one focused series as round-top columns.
export function lines(points, { onSelect, selected = -1, focus = 'expense' } = {}) {
  const padX = 10;
  const top = 10;
  const plot = 136;
  const width = 320;
  const height = top + plot + 22;
  const baseline = top + plot;
  const slot = (width - padX * 2) / Math.max(points.length, 1);
  const monthly = (width - padX * 2) / 12;
  const barW = Math.max(2.5, Math.min(slot, monthly) * (points.length <= 12 ? 0.58 : 0.72));
  const peak = Math.max(1, ...points.map((point) => point[focus]));
  const color = token(`--${focus}`) || `var(--${focus})`;
  const xAt = (index) => padX + slot * index + slot / 2;
  const radius = Math.min(barW / 2, points.length <= 12 ? 7 : 5);

  const svg = node('svg', {
    viewBox: `0 0 ${width} ${height}`,
    preserveAspectRatio: 'xMidYMid meet',
    style: `width: 100%; height: auto; aspect-ratio: ${width} / ${height}`,
  });
  svg.append(node('line', {
    x1: padX, y1: baseline, x2: width - padX, y2: baseline,
    'stroke-width': 1, style: 'stroke: var(--line); opacity: 0.7',
  }));

  points.forEach((point, index) => {
    const value = point[focus];
    if (value <= 0) return;
    const h = Math.max(baseline - (top + plot - (value / peak) * plot), 3);
    svg.append(node('path', {
      d: column(xAt(index) - barW / 2, baseline - h, barW, h, radius),
      fill: color, style: `opacity: ${index === selected ? 1 : 0.28}`,
    }));
  });

  points.forEach((point, index) => {
    if (!showTick(index, selected, points.length)) return;
    svg.append(label(
      xAt(index), height - 5, shortPeriodLabel(point.month),
      index === selected ? 'fill: var(--text); font-weight: 600' : 'fill: var(--muted)',
      points.length <= 12 ? 10 : 11,
    ));
  });

  if (onSelect) {
    points.forEach((point, index) => {
      const left = index === 0 ? 0 : (xAt(index) + xAt(index - 1)) / 2;
      const right = index === points.length - 1 ? width : (xAt(index) + xAt(index + 1)) / 2;
      const hit = node('rect', {
        x: left, y: 0, width: Math.max(right - left, 1), height,
        fill: 'transparent', style: 'cursor: pointer',
      });
      hit.addEventListener('click', () => onSelect(point, index));
      svg.append(hit);
    });
  }
  return svg;
}
