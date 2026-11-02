-- One row per user, team and UTC day that user's daemon checked in from that team's repo (#148).
-- A user is one machine's user account, by a random ID; a team is one repo, by a hash of its
-- remote. Nothing else about either is stored: no names, no IP addresses.
CREATE TABLE checkins (
  day     TEXT NOT NULL, -- YYYY-MM-DD, UTC, the server's
  user    TEXT NOT NULL, -- 32 hex digits
  team    TEXT NOT NULL, -- 32 hex digits
  version TEXT NOT NULL, -- mur's, without the v
  os      TEXT NOT NULL, -- Go's GOOS
  arch    TEXT NOT NULL, -- Go's GOARCH
  PRIMARY KEY (day, user, team)
);
CREATE INDEX checkins_user ON checkins (user, day);
CREATE INDEX checkins_team ON checkins (team, day);
