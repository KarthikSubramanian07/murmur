-- What the census (#259) reads from each public repo in the repos table: its bus (refs/murmur/*)
-- and its commit history, both public. site/census/census.mjs counts them in a GitHub Action each
-- day and sends the counts to /v1/census. Only counts are kept, and a hash for each contributor so
-- one person in two repos counts once: no names, no message text.
--
-- A count is only ever raised. A bus log can be rewritten, and history can be pushed over, and a
-- count that has been seen stays seen.

-- One row per repo per UTC day with anything on its bus.
CREATE TABLE repo_days (
  repo     TEXT NOT NULL,              -- repos.repo
  day      TEXT NOT NULL,              -- YYYY-MM-DD, UTC
  devices  INTEGER NOT NULL DEFAULT 0, -- clones running Murmur that wrote to the bus that day
  people   INTEGER NOT NULL DEFAULT 0, -- of them, distinct git user names, where the bus shows one
  sessions INTEGER NOT NULL DEFAULT 0, -- agent sessions started
  messages INTEGER NOT NULL DEFAULT 0, -- messages between agents, where the bus shows the kind
  records  INTEGER NOT NULL DEFAULT 0, -- everything the devices wrote to their logs
  PRIMARY KEY (repo, day)
);

-- One row per repo: its totals over all time, from the last census.
CREATE TABLE repo_census (
  repo      TEXT PRIMARY KEY,           -- repos.repo
  devices   INTEGER NOT NULL DEFAULT 0,
  people    INTEGER NOT NULL DEFAULT 0,
  sessions  INTEGER NOT NULL DEFAULT 0,
  messages  INTEGER NOT NULL DEFAULT 0,
  records   INTEGER NOT NULL DEFAULT 0,
  first_day TEXT,                       -- the first day with anything on its bus
  last_day  TEXT,                       -- the last
  counted   TEXT NOT NULL               -- when the census last read it, an ISO time
);

-- Everyone who has committed to a repo's default branch, by a hash of their GitHub login or email.
CREATE TABLE contributors (
  repo  TEXT NOT NULL, -- repos.repo
  id    TEXT NOT NULL, -- the first 16 hex digits of the SHA-256 of the login or email
  first TEXT NOT NULL, -- YYYY-MM-DD, UTC: their first commit there
  PRIMARY KEY (repo, id)
);
