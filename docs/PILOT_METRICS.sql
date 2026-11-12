-- CalmFit pilot metrics (build spec "Analytics and launch pilot"; D48).
--
-- Run against the production database:
--   psql "$DATABASE_URL" -v pilot_start=2026-11-23 -v pilot_end=2026-12-21 -v as_of=now -f docs/PILOT_METRICS.sql
--
-- pilot_start / pilot_end: the cohort is every user whose signup_completed falls in [start, end).
-- as_of: a user counts only once their whole window has ended by then (e.g. week-2 needs signup +
--   14 days <= as_of), so a reported number never changes later. Use `now` for live numbers.
-- Windows are measured from each user's own signup time, in exact hours, not calendar days:
--   "within 24 hours" = [signup, signup + 24 h); "days 8-14" = [signup + 7 d, signup + 14 d);
--   "week 4" = [signup + 21 d, signup + 28 d).
-- A deleted account's events are deleted with it, so that user leaves every metric.
-- Each query returns: metric, users, out_of, pct, target. The Go test runs every query in this file.

-- name: first_workout_activation
-- Signups completing a workout within 24 hours. Target 70%+.
WITH cohort AS (
    SELECT user_id, min(occurred_at) AS signup_at
    FROM analytics_events WHERE name = 'signup_completed'
    GROUP BY user_id
    HAVING min(occurred_at) >= :'pilot_start'::timestamptz AND min(occurred_at) < :'pilot_end'::timestamptz
), eligible AS (
    SELECT * FROM cohort WHERE signup_at + interval '24 hours' <= :'as_of'::timestamptz
), hit AS (
    SELECT c.user_id FROM eligible c
    WHERE EXISTS (SELECT 1 FROM analytics_events e
                  WHERE e.user_id = c.user_id AND e.name = 'first_workout_completed'
                    AND e.occurred_at < c.signup_at + interval '24 hours')
)
SELECT 'first_workout_activation' AS metric,
       (SELECT count(*) FROM hit) AS users,
       (SELECT count(*) FROM eligible) AS out_of,
       round(100.0 * (SELECT count(*) FROM hit) / nullif((SELECT count(*) FROM eligible), 0), 1) AS pct,
       '70%+' AS target;

-- name: second_workout
-- Activated users with another workout within 7 days of their first. Target 60%+.
WITH cohort AS (
    SELECT user_id, min(occurred_at) AS signup_at
    FROM analytics_events WHERE name = 'signup_completed'
    GROUP BY user_id
    HAVING min(occurred_at) >= :'pilot_start'::timestamptz AND min(occurred_at) < :'pilot_end'::timestamptz
), activated AS (
    SELECT c.user_id, f.occurred_at AS first_at
    FROM cohort c JOIN analytics_events f ON f.user_id = c.user_id AND f.name = 'first_workout_completed'
    WHERE f.occurred_at < c.signup_at + interval '24 hours'
), eligible AS (
    SELECT * FROM activated WHERE first_at + interval '7 days' <= :'as_of'::timestamptz
), hit AS (
    SELECT a.user_id FROM eligible a
    WHERE EXISTS (SELECT 1 FROM analytics_events e
                  WHERE e.user_id = a.user_id AND e.name = 'second_workout_completed'
                    AND e.occurred_at <= a.first_at + interval '7 days')
)
SELECT 'second_workout' AS metric,
       (SELECT count(*) FROM hit) AS users,
       (SELECT count(*) FROM eligible) AS out_of,
       round(100.0 * (SELECT count(*) FROM hit) / nullif((SELECT count(*) FROM eligible), 0), 1) AS pct,
       '60%+' AS target;

-- name: week2_retention
-- Activated users logging a workout on days 8-14 after signup. Target 30%+.
WITH cohort AS (
    SELECT user_id, min(occurred_at) AS signup_at
    FROM analytics_events WHERE name = 'signup_completed'
    GROUP BY user_id
    HAVING min(occurred_at) >= :'pilot_start'::timestamptz AND min(occurred_at) < :'pilot_end'::timestamptz
), activated AS (
    SELECT c.user_id, c.signup_at
    FROM cohort c JOIN analytics_events f ON f.user_id = c.user_id AND f.name = 'first_workout_completed'
    WHERE f.occurred_at < c.signup_at + interval '24 hours'
), eligible AS (
    SELECT * FROM activated WHERE signup_at + interval '14 days' <= :'as_of'::timestamptz
), hit AS (
    SELECT a.user_id FROM eligible a
    WHERE EXISTS (SELECT 1 FROM analytics_events e
                  WHERE e.user_id = a.user_id AND e.name = 'workout_completed'
                    AND e.occurred_at >= a.signup_at + interval '7 days'
                    AND e.occurred_at <  a.signup_at + interval '14 days')
)
SELECT 'week2_retention' AS metric,
       (SELECT count(*) FROM hit) AS users,
       (SELECT count(*) FROM eligible) AS out_of,
       round(100.0 * (SELECT count(*) FROM hit) / nullif((SELECT count(*) FROM eligible), 0), 1) AS pct,
       '30%+' AS target;

-- name: week4_active
-- Pilot success: users (activated or not) still logging workouts in week 4 after signup.
-- Target: at least 5 people (users >= 5).
WITH cohort AS (
    SELECT user_id, min(occurred_at) AS signup_at
    FROM analytics_events WHERE name = 'signup_completed'
    GROUP BY user_id
    HAVING min(occurred_at) >= :'pilot_start'::timestamptz AND min(occurred_at) < :'pilot_end'::timestamptz
), eligible AS (
    SELECT * FROM cohort WHERE signup_at + interval '28 days' <= :'as_of'::timestamptz
), hit AS (
    SELECT c.user_id FROM eligible c
    WHERE EXISTS (SELECT 1 FROM analytics_events e
                  WHERE e.user_id = c.user_id AND e.name = 'workout_completed'
                    AND e.occurred_at >= c.signup_at + interval '21 days'
                    AND e.occurred_at <  c.signup_at + interval '28 days')
)
SELECT 'week4_active' AS metric,
       (SELECT count(*) FROM hit) AS users,
       (SELECT count(*) FROM eligible) AS out_of,
       round(100.0 * (SELECT count(*) FROM hit) / nullif((SELECT count(*) FROM eligible), 0), 1) AS pct,
       '>= 5 people' AS target;
