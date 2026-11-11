# Decisions

Product questions the build spec doesn't answer. Each one takes the most conservative option;
revisit any of them by changing the code, tests and this file together.

## Day 3

### D1. Can "do later" move an exercise that already has logged sets?
- **Choice:** Yes. It only changes the order within today's workout.
- **Why:** Order has no effect on plans, sets or progression, so blocking it would protect nothing.

### D2. How is the new order stored?
- **Choice:** The moved exercise gets `max(position) + 1`, so positions can have gaps
  (the `0-based` comment in migration 00009 describes the initial order only).
- **Why:** Reads only `ORDER BY position`. Moving one row can't collide with the non-deferrable
  `UNIQUE (workout_id, position)`, and other rows are never rewritten.

### D3. Where do the plate-check weights and timezone live?
- **Choice:** `config/app.json` (path in `APP_CONFIG`, default `config/app.json`), validated at
  startup: unknown keys, an invalid timezone, non-positive points or a threshold outside 1–max all
  stop the server. Tests load the same file.
- **Why:** The spec says "config, not code". One reviewed file is the simplest config that can't
  silently drift from the tests.

### D4. Whose timezone is "the user's local date"?
- **Choice:** One app-wide zone from config (Asia/Kolkata). No per-user timezone column, and the
  client can't send its own offset.
- **Why:** The pilot is one gym in India. A per-user zone is a new feature, and trusting a client
  offset would let dates be gamed.

### D5. The spec's 5 example days (6/6/7/5/4 points)
- **Choice:** The spec's meal-by-meal breakdowns weren't available, so the tests use combinations
  that score those totals: 6 good, 6 good, 7 good, 5 not, 4 not (threshold 6).
- **Why:** Totals and verdicts are what the scoring has to get right; swap in the spec's exact
  meals if they differ.

### D6. Which dates can be logged?
- **Choice:** Today and up to 7 days back (local). Future dates → 400. GET ranges are at most
  31 days and can't end in the future.
- **Why:** Lets people catch up on a missed day without allowing unlimited rewriting of history.

### D7. How do missing meals, percent and the streak work?
- **Choice:** A meal with no entry scores 0. Percent = points / max, rounded down (6/9 → 66%).
  The streak counts consecutive good days ending today; if today isn't good *yet*, it ends at
  yesterday (an unfinished day doesn't break it). Lookback is capped at 366 days.
- **Why:** Rounding down never overstates. Breaking the streak at breakfast would be wrong and demotivating.

### D8. What does "Protein ✓" mean on the plate check?
- **Choice:** The guide says to tick it for one protein-rich serving, or two moderate ones.
  Tier cut-offs (<5 g low, 5–10 g moderate, >10 g rich) are marked "verify before launch" like every other figure.
- **Why:** The spec names the tiers but not the rule. This is the simplest rule a gym member can apply at the table.

### D9. Is the protein guide public, and what else is on it?
- **Choice:** `GET /guide/protein` needs no sign-in. The isabgol note includes "check with a doctor
  if you take regular medication".
- **Why:** It's static education with no user data. Psyllium can affect how medicines are absorbed,
  so the cautious wording is the safer default.
