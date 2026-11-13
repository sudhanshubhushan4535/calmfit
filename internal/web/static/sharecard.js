// Weekly share card (D46): a 1080×1920 Instagram-story PNG drawn on a <canvas> in the browser from
// GET /review/weekly. No server rendering, no fonts to ship, no new endpoint. All text sits
// between y=250 and y=H-250, outside the areas Instagram covers with its own story UI.
// cardModel is pure (tested with node --test); drawCard only paints the model.
(function (root) {
  const W = 1080, H = 1920, PAD = 96;
  const GREEN = '#166534', SOFT = '#dcfce7', INK = '#18181b', MUTED = '#52525b';
  const FONT = 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
  const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

  // Escapes, so the card never depends on how the script's encoding is detected.
  const DASH = '\u2013', ARROW = '\u2192', ELLIPSIS = '\u2026';

  const kg = g => String(g / 1000);

  // "9–15 Nov" or "28 Oct – 3 Nov" from two YYYY-MM-DD dates.
  function weekLabel(start, end) {
    const [, sm, sd] = start.split('-').map(Number);
    const [, em, ed] = end.split('-').map(Number);
    return sm === em ? `${sd}${DASH}${ed} ${MONTHS[em - 1]}` : `${sd} ${MONTHS[sm - 1]} ${DASH} ${ed} ${MONTHS[em - 1]}`;
  }

  // One line per lift that went up (D14): more weight, or the same weight for more reps.
  function changeLine(c) {
    return c.to.weight_g > c.from.weight_g
      ? `${c.name}: ${kg(c.from.weight_g)} ${ARROW} ${kg(c.to.weight_g)} kg`
      : `${c.name}: ${kg(c.to.weight_g)} kg, ${c.from.reps} ${ARROW} ${c.to.reps} reps`;
  }

  // Biggest weight gain first, then the biggest rep gain, then name, so the card is deterministic.
  function byGain(a, b) {
    const dw = (b.to.weight_g - b.from.weight_g) - (a.to.weight_g - a.from.weight_g);
    if (dw) return dw;
    const dr = (b.to.reps - b.from.reps) - (a.to.reps - a.from.reps);
    return dr || a.name.localeCompare(b.name);
  }

  const MAX_LIFTS = 3;

  // cardModel turns a weekly review into the card's text. Plate score = good plate days of the
  // logged days (D16); days with no entries are neutral, so no logged days shows a dash, not 0.
  function cardModel(rv) {
    const up = [...(rv.went_up || [])].sort(byGain);
    const lifts = up.slice(0, MAX_LIFTS).map(changeLine);
    if (up.length > MAX_LIFTS) lifts.push(`+${up.length - MAX_LIFTS} more`);
    const plateDays = rv.plate ? rv.plate.days : 0;
    return {
      week: weekLabel(rv.week_start, rv.week_end),
      stats: [
        { value: `${rv.done}/${rv.planned}`, label: 'workouts' },
        { value: plateDays ? `${rv.plate.good_days}/${plateDays}` : DASH, label: 'good plate days' },
        { value: String(up.length), label: up.length === 1 ? 'lift went up' : 'lifts went up' },
      ],
      strength: up.length ? 'Strength gained' : 'Building strength',
      lifts: lifts.length ? lifts : ['Same weights, cleaner reps. Next week builds on this.'],
      footer: 'Stop counting grams. Start getting stronger.',
    };
  }

  // fitText returns the largest font size (down to min) at which text fits maxWidth, and the text,
  // shortened with an ellipsis if even min is too wide. measure(text, size) gives the width.
  function fitText(text, maxWidth, size, min, measure) {
    for (let s = size; s >= min; s -= 2) {
      if (measure(text, s) <= maxWidth) return { text, size: s };
    }
    let t = text;
    while (t.length > 1 && measure(t + ELLIPSIS, min) > maxWidth) t = t.slice(0, -1);
    return { text: t + ELLIPSIS, size: min };
  }

  // drawCard paints the model onto a 1080×1920 2D context and returns every text it drew with its
  // position and width (used by the tests to check nothing leaves the card).
  function drawCard(ctx, m) {
    const drawn = [];
    const text = (t, x, y, size, color, weight = 600, max = W - 2 * PAD, min = size) => {
      const f = fitText(t, max, size, min, (tt, s) => { ctx.font = `${weight} ${s}px ${FONT}`; return ctx.measureText(tt).width; });
      ctx.font = `${weight} ${f.size}px ${FONT}`;
      ctx.fillStyle = color;
      ctx.fillText(f.text, x, y);
      drawn.push({ text: f.text, x, y, size: f.size, width: ctx.measureText(f.text).width });
    };

    // Greedy word wrap for the short tile labels (at most two lines fit a tile).
    const wrap = (t, max, size) => {
      ctx.font = `500 ${size}px ${FONT}`;
      const lines = [];
      for (const w of t.split(' ')) {
        const last = lines[lines.length - 1];
        if (last !== undefined && ctx.measureText(last + ' ' + w).width <= max) lines[lines.length - 1] = last + ' ' + w;
        else lines.push(w);
      }
      return lines.slice(0, 2);
    };

    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, W, H);
    ctx.fillStyle = GREEN;
    ctx.fillRect(0, 0, W, 620);
    ctx.textBaseline = 'alphabetic';

    text('CalmFit', PAD, 330, 72, '#ffffff', 800);
    text('My week', PAD, 450, 96, '#ffffff', 800);
    text(m.week, PAD, 540, 56, SOFT, 500);

    // Three stat tiles.
    const gap = 32, tileW = (W - 2 * PAD - 2 * gap) / 3, tileY = 700, tileH = 300;
    m.stats.forEach((s, i) => {
      const x = PAD + i * (tileW + gap);
      ctx.fillStyle = SOFT;
      ctx.fillRect(x, tileY, tileW, tileH);
      text(s.value, x + 32, tileY + 150, 104, GREEN, 800, tileW - 64, 56);
      wrap(s.label, tileW - 64, 36).forEach((line, j) => text(line, x + 32, tileY + 215 + j * 44, 36, INK, 500, tileW - 64, 24));
    });

    text(m.strength, PAD, 1140, 64, INK, 800);
    m.lifts.forEach((l, i) => text(l, PAD, 1240 + i * 80, 48, INK, 500, W - 2 * PAD, 30));

    ctx.fillStyle = GREEN;
    ctx.fillRect(PAD, H - 380, W - 2 * PAD, 6);
    text(m.footer, PAD, H - 300, 44, MUTED, 600, W - 2 * PAD, 28);
    return drawn;
  }

  const api = { W, H, cardModel, fitText, drawCard, weekLabel };
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.CalmFitCard = api;
})(typeof self !== 'undefined' ? self : this);
