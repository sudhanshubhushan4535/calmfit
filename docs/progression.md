# CalmFit progression rules

This file is the **single source of truth** for how CalmFit picks weights and reps. The engine in
`internal/progression` implements exactly these rules. Its tests are named after the rule IDs
below (R1–R17), and every worked example here is also a test case. If you change a rule, change
this file, the code and the tests together.

The engine is a deterministic, pure function. It does no I/O, reads no clock (dates are passed
in) and uses no LLM. Given an exercise's config and its recent history, it returns the next
session's targets.

---

## R1. Sets and rep targets
Every exercise has **3 working sets**. Rep targets come from the exercise's rep scheme:

| Scheme | Top | rep_step | Normal targets | Shifted targets (R8, R12) |
|---|---|---|---|---|
| `standard` | 12 | 2 | 12 / 10 / 8 | 10 / 8 / 6 |
| `high_rep` | 18 | 3 | 18 / 15 / 12 | 15 / 12 / 9 |

`rep_step` comes from the exercise config and must match its scheme; a mismatch is a config error.
`progression.Targets` is the only place these numbers live; the API and test page read them from it.

## R2. Weight step
Each exercise stores its step as `weight_step_g` (integer grams). The engine uses that value and
never guesses one; a step ≤ 0 is a config error. The seed fills it with these defaults:

| Equipment | Seeded step | Lightest option |
|---|---|---|
| barbell | 2.5 kg | 20 kg (empty bar) |
| dumbbell | 2.5 kg (per dumbbell) | one step |
| machine | 5 kg (one pin) | one step |
| cable | 5 kg (one pin) | one step |

The lightest option depends on the equipment: the 20 kg bar for barbells, otherwise one step.
Any other equipment is an error. Internally, weights are **integer grams**, so 2.5 kg is 2500
and no float drift can creep in.

## R3. Ascending weights
Set 1 is the session's **base weight W**. Sets go W / W+step / W+2·step, except for the
small-muscle cap in R9.

## R4. Warm-ups (suggested, not logged), compound exercises only
- Two warm-ups at about 50% and 75% of set 1's weight, **2 reps each**.
- Each is rounded to the nearest multiple of the step, with ties rounding down.
- No warm-up goes below the lightest option.
- A warm-up that would be at or above set 1's weight is dropped, and duplicates are merged.
- Isolation exercises get no warm-ups.

Examples:
- Barbell squat 60 kg → 30 kg × 2, 45 kg × 2.
- Cable pulldown 25 kg → 10 kg × 2, 20 kg × 2.
- Dumbbell press 5 kg → 2.5 kg × 2.
- Barbell 20 kg → none.

## Set 1 decides the session
How set 1 went last session decides what comes next (sets 2 and 3 only matter for R9, R10 and
R12). The thresholds always use the **normal** scheme, even while the targets are shifted:

| Set 1 reps (standard / high_rep) | Meaning |
|---|---|
| ≥ 13 / ≥ 19 (above top) | **Increase** (R5) |
| 10–12 / 15–18 (top − rep_step … top) | **Hold zone** (R6) |
| ≤ 9 / ≤ 14 (below top − rep_step) | **Failure** (R7) |

## R5. Increase
- If set 1 goes above the top, **all sets move up one step** next session, and the exercise
  enters the post-increase phase (*building*).
- Example (squat): 60 kg × 13 → next session 62.5 / 65 / 67.5.
- An increase can only happen from the *normal* or *building* phase. Shifted (R8) and capped (R9)
  sessions must first build back to normal targets.

## R6. Hold zone
- If set 1 lands in the hold zone, **repeat the weight**. This applies at any time, not only after
  an increase.
- It means "not a failure". It is **not** automatically counted as progress (R11).

## R7. Failure
- A failed set 1 repeats the weight.
- **Two failures in a row** on this exercise drop **one step**, with normal targets.
- Any session that isn't a failure resets the streak.
- A drop never goes below the lightest option. At the lightest option, the weight holds instead.

## R8. After an increase: big muscles
- In the building phase, if set 1 reaches **exactly top − rep_step** (10 or 15), keep the weight
  but **shift all targets down by rep_step** (10/8/6 or 15/12/9). Weights still ascend.
- Building back:
  - Stay shifted until set 1 reaches the **normal top** (12 or 18).
  - Then go back to normal targets **at the same weight**. That happens even if set 1 got 13 while
    shifted: no increase until you've built back.
- In the building phase, set 1 at 11–12 (16–18) holds with normal targets. Set 1 at the top ends
  the building phase.

Example (squat, after 60 → 62.5): 62.5 × 10 → 62.5 / 65 / 67.5 targeting 10/8/6, then
62.5 × 12 → back to 12/10/8 at 62.5.

## R9. After an increase: small muscles
1. An increase moves **all** sets up one step, just as for big muscles: W+s / W+2s / W+3s.
2. If **set 2 misses its target** in that post-increase session, R10 keeps set 3 at set 2's weight
   in-session. What happens next session depends on set 1:
   - Set 1 **below the normal top** but not a failure (10–11 or 15–17): **set 3 is capped** from the
     next session. It uses set 2's weight and set 2's target reps, i.e. W+s / W+2s / W+2s
     targeting 12/10/10.
   - Set 1 **at the normal top** (12 or 18): the miss is handled **in-session only**. The next
     session is uncapped.
   - Set 1 failed: R7 applies, with no cap.
   Set 2's miss is judged against its prescribed target. A set 2 that R10 held at set 1's weight
   still misses if it falls short of that target.
3. The cap persists until **set 1 reaches the normal top**. The next session then uses the proper
   weights again (W+s / W+2s / W+3s at 12/10/8). If set 1 got 13 while capped, uncap first; there's
   no increase in the same step.
4. Small muscles never use the R8 target shift.

Example (dumbbell lateral raise, 5 → 7.5):
- 7.5 × 11 (set 1 missed), so in-session set 2 holds at 7.5 kg × 12. Set 2 gets 9, below its
  target of 10, so set 3 also holds at 7.5 kg.
- Next session: 7.5 / 10 / 10 at 12/10/10.
- Once set 1 hits 12: 7.5 / 10 / 12.5 at 12/10/8.

Counter-example: 7.5 × 12, then 10 × 8 (set 2 missed). In-session set 3 becomes 10 kg × 10, but set
1 already reached the top, so the next session is the normal 7.5 / 10 / 12.5.

## R10. In-session adjustment (small muscles only)
- During a session, if a set **misses its target**, the next set **does not go up**. It repeats
  the weight just lifted and aims for the missed set's target reps.
- Misses cascade: two misses keep sets 2 and 3 both at set 1's weight and target.
- After a hold, later sets still rise **at most one step per set** (R14).
- Big muscles keep the prescribed plan unchanged.

Example (5 / 7.5 / 10 at 12/10/8):
- Set 1 = 5 × 11 → set 2 becomes 5 kg × 12 and set 3 becomes 7.5 kg × 8.

## R11. Progress
A session **progressed** if, compared with the previous session on that exercise:
- set 1's weight is **higher**, or
- set 1's weight is the same **and total reps** across logged sets are higher.

A lower weight (e.g. after a drop) is not progress.

## R12. Stall
- **Stall** means 3 sessions in a row, each failing to progress over the session before it. That
  needs at least 4 sessions of history.
- The counter resets whenever the engine changes something: an increase, a drop, a target shift,
  going back to normal targets, an uncap, or a stall fix.
- On a stall, the engine sets `stalled = true` and applies a fix in the same call, based on the
  latest session's **shortfall**. Shortfall is the sum over its 3 target sets of
  max(0, target − actual), and a set that wasn't logged counts as 0 reps:

| Shortfall | Fix |
|---|---|
| 0 (all targets met, but set 1 never got above the top) | Flag only, same weight: "Stalled at 12 reps: push set 1 for 13" |
| 1–2 reps | Same weight, **shifted targets** (as R8), then build back |
| 3+ reps | **Drop one step** |

## R13. Cold start (no history)
- The user gives their **usual set-1 weight**. It is rounded down to the step and raised to the
  lightest option if needed. If they answer **"don't know"**, the lightest option (R2) is used.
- The first session is a *calibration* session. The next session starts from the **heaviest
  set actually completed**, where completed means reps ≥ that set's target. That weight is capped
  at **one step** above the calibration set 1 (R14).
- If no set was completed, the normal rules apply.
- Example: 60 / 62.5 / 65 all completed → next set 1 = 62.5, not 65.
- Edge case: if set 1 got 13 but was the only completed set, the cold-start rule wins and set 1 stays.

## R14. Safety
- Set 1's weight never moves more than **one step** from the set-1 weight the user **actually
  lifted** last session.
- Within a session, sets rise by at most one step.
- **Logged weights are the truth.** If the user lifted something other than the prescription, the
  next targets are computed from what they lifted.

## R15. Input validation
These are rejected with an `ErrInvalidInput` error:
- weight ≤ 0;
- reps < 1 or > 50;
- 0 or more than 3 logged sets in a session (1–3 is fine; set 1 is required, and a missing set 2 or 3 counts as missed);
- sessions out of date order (the same date is allowed);
- a session without its prescribed plan;
- a cold start with no usual weight and no "don't know".

A bad exercise config (unknown equipment, scheme, size or movement, or a rep_step that doesn't
match the scheme) returns an `ErrConfig` error.

## R16. Why
- Every target has a one-line **why**, and the plan has a summary.
- When several rules apply, priority is: safety > stall > failure/drop > increase > shift/cap/build-back > hold.

Examples:
- "+2.5 kg: you hit 13 reps last time"
- "Hold: 11 reps on set 1; hit 13 to go up"
- "Hold: set 1 got 9, below 10"
- "-2.5 kg: set 1 was under 10 reps two sessions in a row"
- "Same weight, targets 10/8/6: set 1 reached only 10 after the increase"
- "Hold: set 2 missed its reps" (in-session)

## R17. Determinism and state
- The engine never calls `time.Now()`.
- Each logged session stores the plan it was given: phase, targets, failure streak and
  no-progress count. The engine reads only the **last two sessions**, so changing a rule later
  never reinterprets old sessions.
- The same input always gives the same output.

Phases: `calibration` → `normal` ⇄ `building` → `shifted` (big) or `capped` (small) → `normal`.
