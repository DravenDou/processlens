// The block-tower set as itself: one color per brick id.
const BRICK: Record<string, string> = {
  'red-base': '#d23b3b',
  'blue-left': '#2f6fd0',
  'yellow-right': '#e3b008',
  'orange-bridge': '#e8821e',
  'white-top': '#e8e5de',
  'black-cap': '#232323',
}

export function brickColor(id: string): string {
  return BRICK[id] || '#999'
}

// Semantic colors for real-world demo objects, matched on the label or id.
// Anything unknown falls back to a stable hash of the id, so the same
// object keeps the same color in every view (workflow, report, history).
const SEMANTIC: [RegExp, string][] = [
  [/helmet|hard.?hat/, '#e3a008'],
  [/goggle|glasses|visor/, '#0e7c86'],
  [/glove/, '#e8821e'],
  [/vest|jacket/, '#7a4fa3'],
  [/box|cardboard|carton/, '#b07a45'],
  [/book/, '#2f6fd0'],
  [/mug|cup/, '#d23b3b'],
  [/ball/, '#e3b008'],
  [/remote/, '#3d3d3d'],
  [/charger|cable/, '#5b8c5a'],
  [/spoon|fork|knife/, '#8a8f98'],
  [/tape/, '#e3b008'],
  [/hammer/, '#4a6fa5'],
  [/screw/, '#6b7c8d'],
  [/plate|base/, '#a34d4d'],
  [/screwdriver|tool/, '#2e7d6b'],
  [/label|sticker/, '#c95d8f'],
  [/product|item|package/, '#5470a3'],
  [/red/, '#d23b3b'],
  [/blue/, '#2f6fd0'],
  [/yellow/, '#e3b008'],
  [/orange/, '#e8821e'],
  [/white/, '#c9c4b8'],
  [/black/, '#232323'],
  [/green/, '#2e7d32'],
]

const PALETTE = [
  '#d23b3b', '#2f6fd0', '#e3a008', '#2e7d6b',
  '#7a4fa3', '#c95d8f', '#4a6fa5', '#5b8c5a',
]

export function hashHue(key: string): string {
  let h = 0
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) >>> 0
  return PALETTE[h % PALETTE.length]
}

export function objectColor(id: string, label?: string): string {
  if (BRICK[id]) return BRICK[id]
  const hay = `${id} ${label ?? ''}`.toLowerCase()
  for (const [re, color] of SEMANTIC) {
    if (re.test(hay)) return color
  }
  return hashHue(id || label || '?')
}

// One color per finding type, used on pills, card edges and markers.
export function findingColor(type: string): string {
  if (type === 'missing') return '#b07d0a'
  if (type === 'wrong-order') return '#1d5fbf'
  if (type === 'misplaced') return '#c4242a'
  return '#6b6b6b'
}

export function inkOn(hex: string): string {
  const n = parseInt(hex.slice(1), 16)
  const r = (n >> 16) & 255
  const g = (n >> 8) & 255
  const b = n & 255
  return 0.299 * r + 0.587 * g + 0.114 * b > 165 ? '#141414' : '#ffffff'
}

export function paleTint(hex: string): string {
  const n = parseInt(hex.slice(1), 16)
  const r = (n >> 16) & 255
  const g = (n >> 8) & 255
  const b = n & 255
  const mix = (c: number) => Math.round(c + (255 - c) * 0.86)
  return `rgb(${mix(r)}, ${mix(g)}, ${mix(b)})`
}
