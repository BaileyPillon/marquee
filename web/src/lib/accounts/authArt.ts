// The theatre scene on the setup, sign-in and invite screens: a curtain, a skyline of stepped
// towers and a marquee sign with chasing bulbs. Ported number for number from the approved canvas
// (A-FirstRun.dc.html for the wide scene, A-SignIn.dc.html for the phone scene). Like heroArt.ts
// the shapes are style strings; colours are tokens (--art-*, --accent, --text, --ground).

const RULE = 'rgba(242,237,228,0.35)';
const ANTENNA = 'rgba(242,237,228,0.6)';
const CAP = 'border-top:1px solid rgba(242,237,228,0.4);';

/** A round bulb of `size` px; every other one is lit (full accent), the rest are dim. */
export function bulbStyle(size: number, k: number): string {
  const lit = k % 2 === 0;
  return `flex:0 0 ${size}px;width:${size}px;height:${size}px;border-radius:50%;background:var(--accent);opacity:${lit ? '1' : '0.28'};`;
}

/** n bulbs for a flex row or column of the wide scene; `offset` shifts which ones are lit. */
export function bulbRow(n: number, size: number, offset: (i: number) => number): string[] {
  return Array.from({ length: n }, (_, i) => bulbStyle(size, offset(i)));
}

export interface CurtainBand {
  style: string;
  foldStyle: string;
}

/** Wide scene: twelve curtain bands, six down each side (0% to 30% and 70% to 100%). */
export function buildWideCurtain(): CurtainBand[] {
  const tones = ['var(--art-curtain-a)', 'var(--art-curtain-b)'];
  const bands: CurtainBand[] = [];
  for (const start of [0, 70]) {
    for (let i = 0; i < 6; i++) {
      bands.push({
        style: `position:absolute;left:${start + i * 5}%;top:0;bottom:0;width:5%;box-sizing:border-box;z-index:1;background:${tones[i % 2]};box-shadow:inset -1px 0 0 rgba(242,237,228,0.12);`,
        foldStyle:
          'position:absolute;left:30%;top:0;bottom:0;width:24%;background:var(--art-curtain-fold);',
      });
    }
  }
  return bands;
}

interface TowerDef {
  /** Offset of the tower's centre from the middle of the scene, in px. */
  o: number;
  w: number;
  h: number;
  tone: string;
}

const WIDE_TOWERS: TowerDef[] = [
  { o: -132, w: 44, h: 40, tone: 'var(--art-tower-a)' },
  { o: -66, w: 54, h: 70, tone: 'var(--art-tower-b)' },
  { o: 0, w: 74, h: 104, tone: 'var(--art-tower-a)' },
  { o: 66, w: 54, h: 70, tone: 'var(--art-tower-b)' },
  { o: 132, w: 44, h: 40, tone: 'var(--art-tower-a)' },
];

/** Wide scene: five towers (three steps each plus an antenna) standing on the 72px floor. */
export function buildWideSkyline(floor = 72): string[] {
  const pieces: string[] = [];
  for (const t of WIDE_TOWERS) {
    const steps = [
      { w: t.w, h: t.h },
      { w: Math.round(t.w * 0.7), h: t.h + 22 },
      { w: Math.round(t.w * 0.4), h: t.h + 40 },
    ];
    for (const s of steps) {
      pieces.push(
        `position:absolute;left:50%;margin-left:${t.o - Math.round(s.w / 2)}px;bottom:${floor}px;width:${s.w}px;height:${s.h}px;box-sizing:border-box;z-index:2;background:${t.tone};${CAP}`,
      );
    }
    pieces.push(
      `position:absolute;left:50%;margin-left:${t.o}px;bottom:${floor}px;width:1px;height:${t.h + 62}px;z-index:2;background:${ANTENNA};`,
    );
  }
  return pieces;
}

/** Phone scene: the whole 390 x 300 composition, one absolutely placed shape per entry. */
export function buildPhoneArt(): string[] {
  const art: string[] = [];
  const box = (l: number, t: number, w: number, h: number, extra: string): void => {
    art.push(
      `position:absolute;left:${l}px;top:${t}px;width:${w}px;height:${h}px;box-sizing:border-box;${extra}`,
    );
  };
  const lamp = (x: number, y: number, size: number, k: number): void => {
    const lit = k % 2 === 0;
    box(
      Math.round(x),
      Math.round(y),
      size,
      size,
      `border-radius:50%;background:var(--accent);opacity:${lit ? '1' : '0.28'};`,
    );
  };

  const floorTop = 262;

  // Rings and the moon.
  box(59, 20, 272, 272, 'border-radius:50%;border:1px solid rgba(242,237,228,0.1);');
  box(83, 44, 224, 224, 'border-radius:50%;border:1px solid rgba(242,237,228,0.24);');
  box(107, 68, 176, 176, 'border-radius:50%;background:var(--art-moon);');

  // Curtains: five bands down each edge, each with a lighter fold.
  const tones = ['var(--art-curtain-a)', 'var(--art-curtain-b)'];
  const bandX = [0, 21, 42, 63, 84, 285, 306, 327, 348, 369];
  bandX.forEach((x, i) => {
    box(
      x,
      0,
      21,
      floorTop,
      `background:${tones[i % 2]};box-shadow:inset -1px 0 0 rgba(242,237,228,0.12);`,
    );
    box(x + 6, 0, 6, floorTop, 'background:var(--art-curtain-fold);');
  });
  box(104, 0, 1, floorTop, `background:${RULE};`);
  box(285, 0, 1, floorTop, `background:${RULE};`);

  // Skyline.
  const towers: TowerDef[] = [
    { o: -76, w: 26, h: 12, tone: 'var(--art-tower-a)' },
    { o: -38, w: 32, h: 22, tone: 'var(--art-tower-b)' },
    { o: 0, w: 44, h: 34, tone: 'var(--art-tower-a)' },
    { o: 38, w: 32, h: 22, tone: 'var(--art-tower-b)' },
    { o: 76, w: 26, h: 12, tone: 'var(--art-tower-a)' },
  ];
  for (const t of towers) {
    const steps = [
      { w: t.w, h: t.h },
      { w: Math.round(t.w * 0.7), h: t.h + 8 },
      { w: Math.round(t.w * 0.4), h: t.h + 14 },
    ];
    for (const s of steps) {
      box(195 + t.o - Math.round(s.w / 2), floorTop - s.h, s.w, s.h, `background:${t.tone};${CAP}`);
    }
    box(195 + t.o, floorTop - (t.h + 22), 1, t.h + 22, `background:${ANTENNA};`);
  }

  // The marquee sign: two poles, a framed panel and a ring of lamps.
  const sx = 70;
  const sy = 92;
  const sw = 250;
  const sh = 128;
  const bs = 9;
  box(115, 52, 1, 40, `background:${ANTENNA};`);
  box(274, 52, 1, 40, `background:${ANTENNA};`);
  box(112, 89, 7, 7, 'background:var(--text);');
  box(271, 89, 7, 7, 'background:var(--text);');
  box(sx, sy, sw, sh, 'background:var(--ground);box-shadow:inset 0 0 0 1px rgba(242,237,228,0.45);');
  box(sx + 24, sy + 24, sw - 48, sh - 48, 'border:1px solid rgba(242,237,228,0.2);');

  const pitchX = (sw - 16 - bs) / 10;
  const gapY = (sh - 16 - 2 * bs - 4 * bs) / 5;
  const ring: Array<{ x: number; y: number }> = [];
  for (let i = 0; i < 11; i++) ring.push({ x: sx + 8 + i * pitchX, y: sy + 8 });
  for (let j = 0; j < 4; j++) {
    ring.push({ x: sx + sw - 8 - bs, y: sy + 8 + bs + gapY * (j + 1) + bs * j });
  }
  for (let i = 10; i >= 0; i--) ring.push({ x: sx + 8 + i * pitchX, y: sy + sh - 8 - bs });
  for (let j = 3; j >= 0; j--) ring.push({ x: sx + 8, y: sy + 8 + bs + gapY * (j + 1) + bs * j });
  ring.forEach((p, i) => lamp(p.x, p.y, bs, i));

  // Valance across the top.
  box(0, 0, 390, 36, 'background:var(--art-valance);box-shadow:inset 0 -1px 0 rgba(242,237,228,0.28);');
  for (let i = 0; i < 13; i++) {
    box(
      i * 30,
      36,
      30,
      16,
      'background:var(--art-valance);border:1px solid rgba(242,237,228,0.28);border-top:0;border-radius:0 0 50% 50% / 0 0 100% 100%;',
    );
  }

  // Floor and footlights.
  box(0, floorTop, 390, 38, `background:var(--art-floor);${CAP}`);
  for (let i = 0; i < 16; i++) lamp(24 + i * ((342 - 7) / 15), 277, 7, i);

  // Frame with corner studs.
  box(10, 10, 370, 280, 'border:1px solid rgba(242,237,228,0.28);');
  box(7, 7, 7, 7, 'background:var(--text);');
  box(376, 7, 7, 7, 'background:var(--text);');
  box(7, 286, 7, 7, 'background:var(--text);');
  box(376, 286, 7, 7, 'background:var(--text);');

  return art;
}
