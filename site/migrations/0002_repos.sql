-- One row per public repo that uses Murmur (#199). Murmur sends a repo's address once, and only if
-- the repo is public (internal/publicrepo); it is the only thing Murmur sends. Nothing else is
-- stored: no IP addresses. The checkins table stays as history and gets no new rows.
CREATE TABLE repos (
  repo     TEXT PRIMARY KEY, -- github.com/<owner>/<name>, lowercased
  name     TEXT NOT NULL,    -- the same, as GitHub spells it
  first    TEXT NOT NULL,    -- YYYY-MM-DD, UTC, the server's: when it was first reported
  verified INTEGER NOT NULL  -- 1 if GitHub showed it as public when it was reported; 0 if GitHub couldn't say
);
