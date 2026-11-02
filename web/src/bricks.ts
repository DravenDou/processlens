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
