# Pilot UI checklist (real phones)

Run on one Android phone (Chrome) and one iPhone (Safari, then the Home Screen app) against the
deployed HTTPS domain before onboarding pilot users. Tick each line on both phones; note the phone
model and OS version at the top.

Phones: Android ______________ · iPhone ______________

## Install and sign-in
- [ ] Landing page (`/`) reads well; "Start with Google" opens `/app`.
- [ ] Google sign-in works with one tap (no forms). A second visit signs in without asking again.
- [ ] Install: Android shows "Install CalmFit" (or the ⋮ menu steps work); iPhone Share → Add to Home Screen.
- [ ] The installed app opens full-screen with the CalmFit icon and name; the guide is gone on Today.

## First session (the aha)
- [ ] Onboarding: tap a number of gym days, see the Push → Pull → Legs explainer.
- [ ] Today shows the next workout; Start workout opens it.
- [ ] First exercise asks the usual weight; the stepper and "I don't know" both work.
- [ ] A set logs with one tap ✓; +/− change weight and reps; no keyboard appears.
- [ ] "How to do it" shows two images, cues (marked unchecked by a coach) and steps.
- [ ] "Do later" moves the exercise to the end.
- [ ] Finish → "next time" screen: today vs next per exercise with the why line.
- [ ] Reminder offer appears after the workout. Android: allow → notification arrives with
      `make reminders ARGS="-user <uuid>"` on the server. iPhone: only in the Home Screen app.
- [ ] Tapping the notification opens the app.

## Never lose a set
- [ ] Mid-workout, pull down to refresh / close and reopen the app: logged sets are still there.
- [ ] Airplane mode, log a set: it shows "Not saved yet". Turn airplane mode off, tap retry (or
      reopen): it saves. Finish is disabled while a set waits.

## Other screens
- [ ] Plate: Protein/Fiber toggles, score %, streak, last 7 days; the guide link and its "Back to CalmFit".
- [ ] Progress: this week, previous weeks (← →), both charts, picking another lift.
- [ ] Share card: Share opens the share sheet (Instagram story); Download saves the image.
- [ ] Account: change gym days; reminders off/on; Download my data; Log out; delete flow (test account only).
- [ ] Dark mode (phone setting) is readable on every screen.
- [ ] Text is large enough to read in the gym; nothing needs zooming; no sideways scrolling.

## Notes
Anything confusing, slow or broken, with screenshots:
