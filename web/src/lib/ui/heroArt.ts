// Art-deco composition for the featured title: a framed panel, a moon disc behind a skyline of
// stepped towers. Ported shape for shape from the Cinema mockup (Main.dc.html), drawn on a
// 520px-wide stage; top shapes hang from the top edge, towers and the floor sit on the bottom.
// Colours come from custom properties set by Hero: --hero-disc, --hero-tower-a, --hero-tower-b.

const RULE = 'rgba(242,237,228,0.35)';

interface Tower {
  x: number;
  w: number;
  h: number;
  tone: 'a' | 'b';
}

const TOWERS: Tower[] = [
  { x: 24, w: 50, h: 48, tone: 'a' },
  { x: 82, w: 62, h: 86, tone: 'b' },
  { x: 150, w: 60, h: 118, tone: 'a' },
  { x: 218, w: 84, h: 150, tone: 'b' },
  { x: 310, w: 60, h: 106, tone: 'a' },
  { x: 376, w: 62, h: 92, tone: 'b' },
  { x: 446, w: 50, h: 56, tone: 'a' },
];

export function buildHeroArt(): string[] {
  const shapes: string[] = [];
  const shape = (style: string): void => {
    shapes.push(style);
  };

  shape(
    'position:absolute;left:14px;top:14px;right:14px;bottom:14px;box-sizing:border-box;border:1px solid rgba(242,237,228,0.28);',
  );
  shape(`position:absolute;left:28px;top:25px;width:160px;height:1px;background:${RULE};`);
  shape(`position:absolute;left:332px;top:25px;width:160px;height:1px;background:${RULE};`);
  shape(
    'position:absolute;left:129px;top:-13px;width:262px;height:262px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.14);',
  );
  shape(
    'position:absolute;left:153px;top:11px;width:214px;height:214px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.24);',
  );
  shape(
    'position:absolute;left:175px;top:33px;width:170px;height:170px;border-radius:50%;background:var(--hero-disc);',
  );

  for (const t of TOWERS) {
    const tone = `var(--hero-tower-${t.tone})`;
    const steps = [
      { l: t.x, w: t.w, h: t.h },
      { l: t.x + Math.round(t.w * 0.15), w: Math.round(t.w * 0.7), h: t.h + 22 },
      { l: t.x + Math.round(t.w * 0.3), w: Math.round(t.w * 0.4), h: t.h + 40 },
    ];
    for (const s of steps) {
      shape(
        `position:absolute;left:${s.l}px;bottom:14px;width:${s.w}px;height:${s.h}px;box-sizing:border-box;background:${tone};border-top:1px solid rgba(242,237,228,0.4);`,
      );
    }
    shape(
      `position:absolute;left:${t.x + Math.round(t.w / 2)}px;bottom:14px;width:1px;height:${t.h + 62}px;background:rgba(242,237,228,0.6);`,
    );
  }

  // The floor bar (14px, tower-b) is drawn by HeroArt itself so that it spans the whole panel.
  shape('position:absolute;left:11px;top:11px;width:7px;height:7px;background:#F2EDE4;');
  shape('position:absolute;right:11px;top:11px;width:7px;height:7px;background:#F2EDE4;');
  shape('position:absolute;left:11px;bottom:11px;width:7px;height:7px;background:#F2EDE4;');
  shape('position:absolute;right:11px;bottom:11px;width:7px;height:7px;background:#F2EDE4;');

  return shapes;
}

/** 1927 -> "MCMXXVII": the year in Roman numerals, set small above the skyline. */
export function toRoman(value: number): string {
  const table: Array<[number, string]> = [
    [1000, 'M'],
    [900, 'CM'],
    [500, 'D'],
    [400, 'CD'],
    [100, 'C'],
    [90, 'XC'],
    [50, 'L'],
    [40, 'XL'],
    [10, 'X'],
    [9, 'IX'],
    [5, 'V'],
    [4, 'IV'],
    [1, 'I'],
  ];
  let rest = Math.max(0, Math.floor(value));
  let out = '';
  for (const [n, glyph] of table) {
    while (rest >= n) {
      out += glyph;
      rest -= n;
    }
  }
  return out;
}
