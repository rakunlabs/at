interface NodeBounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Move a newly added card below occupied space without moving existing nodes. */
export function findNodePlacement(preferred: { x: number; y: number }, size: { width: number; height: number }, occupied: NodeBounds[]) {
  const position = { ...preferred };
  const gap = 40;
  for (let attempt = 0; attempt <= occupied.length; attempt++) {
    const collisions = occupied.filter(node =>
      position.x < node.x + node.width + gap && position.x + size.width + gap > node.x &&
      position.y < node.y + node.height + gap && position.y + size.height + gap > node.y);
    if (!collisions.length) break;
    position.y = Math.max(...collisions.map(node => node.y + node.height + gap));
  }
  return position;
}
