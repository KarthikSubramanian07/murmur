-- The GitHub account behind each device (#323), for the stats page's users. The census matches the
-- git user name a device's presence shows to a commit author of that name in the repo's history
-- (site/census/census.mjs, accounts()). Kept as a hash, like contributors.id, so the two compare.
-- NULL until the census finds a match; once found, it stays.
ALTER TABLE devices ADD COLUMN account TEXT;
