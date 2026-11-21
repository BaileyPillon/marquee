// Art-deco compositions for the title panels, built from solid shapes. Each one is ported shape for
// shape from its Cinema mockup (A-Title for the skyline, A-Show for the horizon, A-Artist for the
// keys), drawn on a 520px-wide stage. Shapes are absolutely positioned CSS strings in paint order.
//
// A shape marked `bleed` spans the whole panel (the sky, the ground); the rest sit on the stage,
// which is centred when the panel is wider than 520px.
import { buildHeroArt, toRoman } from './heroArt';

export interface ArtShape {
  css: string;
  bleed?: boolean;
}

export interface ArtComposition {
  shapes: ArtShape[];
  /** Small spaced capitals centred at the top: a year in Roman numerals. */
  numeral?: { text: string; tone: 'muted' | 'soft' };
  /** More small capitals, placed on the stage. */
  labels?: Array<{ text: string; left: number; top: number }>;
}

/** The four small squares at the corners of the frame, shared by every composition. */
export function frameCorners(): ArtShape[] {
  return [
    { css: 'position:absolute;left:11px;top:11px;width:7px;height:7px;background:#F2EDE4;' },
    { css: 'position:absolute;right:11px;top:11px;width:7px;height:7px;background:#F2EDE4;' },
    { css: 'position:absolute;left:11px;bottom:11px;width:7px;height:7px;background:#F2EDE4;' },
    { css: 'position:absolute;right:11px;bottom:11px;width:7px;height:7px;background:#F2EDE4;' },
  ];
}

/* ------------------------------------------------------------------ Skyline (Metropolis) */

const SKYLINE = buildHeroArt();

/**
 * A moon behind a skyline of stepped towers. The colours come from custom properties that the
 * panel sets: --hero-disc, --hero-tower-a and --hero-tower-b.
 */
export function skylineArt(year: number): ArtComposition {
  return {
    shapes: [
      // The ground the skyline stands on runs the full width of the panel.
      {
        css: 'position:absolute;left:0;right:0;bottom:0;height:14px;background:var(--hero-tower-b);',
        bleed: true,
      },
      ...SKYLINE.map((css) => ({ css })),
    ],
    numeral: { text: toRoman(year), tone: 'muted' },
  };
}

/* ------------------------------------------------------------------ Horizon (Pioneer One) */

/**
 * A dusk sky in six bands, an amber sun, a dark plain with a farmhouse, a silo and a pylon, and a
 * returning capsule hanging from its parachute.
 */
export function horizonArt(year: number): ArtComposition {
  const shapes: ArtShape[] = [];
  const shape = (css: string, bleed = false): void => {
    shapes.push({ css, bleed });
  };
  const line = (x1: number, y1: number, x2: number, y2: number, color: string): void => {
    const dx = x2 - x1;
    const dy = y2 - y1;
    const len = Math.sqrt(dx * dx + dy * dy);
    const ang = (Math.atan2(dy, dx) * 180) / Math.PI;
    shape(
      `position:absolute;left:${x1}px;top:${y1}px;width:${len.toFixed(1)}px;height:1px;background:${color};transform-origin:0 0;transform:rotate(${ang.toFixed(2)}deg);`,
    );
  };

  const hz = 220;
  const cx = 330;
  const canTop = 40;
  const canR = 66;
  const hem = canTop + canR;
  const capTop = 158;
  const dark = '#0E0B10';

  const bands = ['#2A2030', '#3B2A3C', '#52364A', '#6E4553', '#8F5A58', '#B87555'];
  bands.forEach((color, b) => {
    const top = b * 36;
    const height = b === bands.length - 1 ? hz - top : 36;
    shape(`position:absolute;left:0;right:0;top:${top}px;height:${height}px;background:${color};`, true);
  });

  const stars = [
    [46, 26],
    [92, 58],
    [186, 42],
    [236, 72],
    [440, 34],
    [484, 66],
    [458, 98],
  ] as const;
  for (const [x, y] of stars) {
    shape(
      `position:absolute;left:${x}px;top:${y}px;width:2px;height:2px;background:rgba(242,237,228,0.7);`,
    );
  }

  shape(
    'position:absolute;left:40px;top:110px;width:220px;height:220px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.16);',
  );
  shape(
    'position:absolute;left:64px;top:134px;width:172px;height:172px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.26);',
  );
  shape(
    'position:absolute;left:86px;top:156px;width:128px;height:128px;border-radius:50%;background:var(--accent);',
  );
  shape('position:absolute;left:86px;top:190px;width:128px;height:2px;background:#B87555;');
  shape('position:absolute;left:86px;top:200px;width:128px;height:3px;background:#B87555;');
  shape('position:absolute;left:86px;top:210px;width:128px;height:4px;background:#B87555;');

  shape(`position:absolute;left:0;right:0;top:${hz}px;bottom:0;background:${dark};`, true);
  shape(
    `position:absolute;left:0;right:0;top:${hz}px;height:1px;background:rgba(242,237,228,0.6);`,
    true,
  );
  shape(
    `position:absolute;left:14px;right:14px;top:${hz + 16}px;height:1px;background:rgba(242,237,228,0.22);`,
  );
  shape(
    `position:absolute;left:14px;right:14px;top:${hz + 36}px;height:1px;background:rgba(242,237,228,0.16);`,
  );
  shape(
    `position:absolute;left:14px;right:14px;top:${hz + 62}px;height:1px;background:rgba(242,237,228,0.12);`,
  );

  // Farmhouse, silo and pylon on the horizon.
  shape(`position:absolute;left:410px;top:${hz - 22}px;width:38px;height:22px;background:${dark};`);
  shape(
    `position:absolute;left:407px;top:${hz - 36}px;width:0;height:0;border-left:22px solid transparent;border-right:22px solid transparent;border-bottom:14px solid ${dark};`,
  );
  shape(`position:absolute;left:456px;top:${hz - 30}px;width:11px;height:30px;background:${dark};`);
  shape(
    `position:absolute;left:456px;top:${hz - 35}px;width:11px;height:11px;border-radius:50%;background:${dark};`,
  );
  shape(`position:absolute;left:486px;top:${hz - 38}px;width:1px;height:38px;background:${dark};`);
  shape(`position:absolute;left:480px;top:${hz - 34}px;width:13px;height:1px;background:${dark};`);

  // The parachute canopy with its seams, the cords and the capsule.
  shape(
    `position:absolute;left:${cx - canR}px;top:${canTop}px;width:${canR * 2}px;height:${canR}px;border-radius:${canR}px ${canR}px 0 0;background:${dark};box-shadow:inset 0 1px 0 rgba(242,237,228,0.5);`,
  );
  for (const d of [-44, -22, 0, 22, 44]) {
    const seamTop = hem - Math.round(Math.sqrt(canR * canR - d * d));
    shape(
      `position:absolute;left:${cx + d}px;top:${seamTop}px;width:1px;height:${hem - seamTop}px;background:rgba(242,237,228,0.22);`,
    );
  }
  shape(
    `position:absolute;left:${cx - canR}px;top:${hem}px;width:${canR * 2}px;height:1px;background:rgba(242,237,228,0.4);`,
  );

  const cord = 'rgba(242,237,228,0.5)';
  line(cx - 66, hem, cx - 11, capTop, cord);
  line(cx - 33, hem, cx - 5, capTop, cord);
  line(cx + 33, hem, cx + 5, capTop, cord);
  line(cx + 66, hem, cx + 11, capTop, cord);

  shape(
    `position:absolute;left:${cx - 19}px;top:${capTop}px;width:38px;height:28px;box-sizing:border-box;border-radius:19px 19px 6px 6px / 22px 22px 6px 6px;background:${dark};box-shadow:inset 0 1px 0 rgba(242,237,228,0.5);`,
  );
  shape(
    `position:absolute;left:${cx - 22}px;top:${capTop + 27}px;width:44px;height:4px;border-radius:2px;background:${dark};`,
  );
  shape(
    `position:absolute;left:${cx - 4}px;top:${capTop + 10}px;width:8px;height:8px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.6);`,
  );

  shape(
    'position:absolute;left:14px;top:14px;right:14px;bottom:14px;box-sizing:border-box;border:1px solid rgba(242,237,228,0.28);',
  );
  shapes.push(...frameCorners());

  return { shapes, numeral: { text: toRoman(year), tone: 'soft' } };
}

/* ------------------------------------------------------------------ Keys (Chopin) */

/**
 * A full moon behind a staff with a beamed run of notes, over one octave and a half of piano keys.
 * `born` and `died` become the Roman numerals at the top; `caption` is set small beside the staff.
 */
export function keysArt(born: number, died: number, caption: string): ArtComposition {
  const shapes: ArtShape[] = [];
  const shape = (css: string): void => {
    shapes.push({ css });
  };
  const rule = 'rgba(242,237,228,0.35)';
  const ivory = '#E8E0CE';
  const ink = '#0F0A18';

  shape(
    'position:absolute;left:14px;top:14px;right:14px;bottom:14px;box-sizing:border-box;border:1px solid rgba(242,237,228,0.28);',
  );
  shape(`position:absolute;left:28px;top:25px;width:132px;height:1px;background:${rule};`);
  shape(`position:absolute;left:360px;top:25px;width:132px;height:1px;background:${rule};`);

  shape(
    'position:absolute;left:278px;top:34px;width:188px;height:188px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.14);',
  );
  shape(
    'position:absolute;left:298px;top:54px;width:148px;height:148px;box-sizing:border-box;border-radius:50%;border:1px solid rgba(242,237,228,0.24);',
  );
  shape(
    'position:absolute;left:316px;top:72px;width:112px;height:112px;border-radius:50%;background:#EFE7D2;',
  );

  const craters = [
    { x: 336, y: 96, d: 24 },
    { x: 378, y: 132, d: 28 },
    { x: 350, y: 150, d: 12 },
    { x: 392, y: 88, d: 10 },
  ];
  for (const c of craters) {
    shape(
      `position:absolute;left:${c.x}px;top:${c.y}px;width:${c.d}px;height:${c.d}px;border-radius:50%;background:#D9D0B9;`,
    );
  }

  // The staff: five lines and two bar lines.
  for (let k = 0; k < 5; k++) {
    shape(
      `position:absolute;left:34px;top:${70 + k * 12}px;width:216px;height:1px;background:rgba(242,237,228,0.3);`,
    );
  }
  shape('position:absolute;left:34px;top:70px;width:1px;height:49px;background:rgba(242,237,228,0.3);');
  shape('position:absolute;left:249px;top:70px;width:1px;height:49px;background:rgba(242,237,228,0.3);');

  const notes = [
    { cx: 78, cy: 112, up: true },
    { cx: 112, cy: 100, up: true },
    { cx: 146, cy: 88, up: false },
    { cx: 180, cy: 100, up: true },
    { cx: 214, cy: 112, up: true },
  ];
  for (const n of notes) {
    if (n.up) {
      shape(
        `position:absolute;left:${n.cx + 6}px;top:${n.cy - 33}px;width:2px;height:33px;background:${ivory};`,
      );
    } else {
      shape(
        `position:absolute;left:${n.cx - 8}px;top:${n.cy + 2}px;width:2px;height:33px;background:${ivory};`,
      );
    }
  }
  shape(
    `position:absolute;left:84px;top:77px;width:36px;height:4px;background:${ivory};transform-origin:0 50%;transform:rotate(-19.4deg);`,
  );
  shape(
    `position:absolute;left:186px;top:65px;width:36px;height:4px;background:${ivory};transform-origin:0 50%;transform:rotate(19.4deg);`,
  );
  for (const n of notes) {
    shape(
      `position:absolute;left:${n.cx - 8}px;top:${n.cy - 6}px;width:16px;height:12px;border-radius:50%;background:${ivory};transform:rotate(-20deg);`,
    );
  }

  // The keyboard: 14 white keys and the black ones between them.
  shape(`position:absolute;left:24px;bottom:111px;width:476px;height:5px;background:${ink};`);
  for (let w = 0; w < 14; w++) {
    shape(
      `position:absolute;left:${24 + w * 34}px;bottom:15px;width:34px;height:96px;box-sizing:border-box;background:${ivory};border-right:1px solid #2B2142;border-radius:0 0 3px 3px;`,
    );
  }
  for (const b of [0, 1, 3, 4, 5, 7, 8, 10, 11, 12]) {
    shape(
      `position:absolute;left:${24 + (b + 1) * 34 - 10}px;bottom:53px;width:20px;height:58px;box-sizing:border-box;background:${ink};border-radius:0 0 2px 2px;`,
    );
  }

  shapes.push(...frameCorners());

  return {
    shapes,
    numeral: { text: `${toRoman(born)} · ${toRoman(died)}`, tone: 'muted' },
    labels: [{ text: caption, left: 34, top: 146 }],
  };
}
