Closes #238

## Why

Tyler wants the only data Murmur collects to be a public repo's name (decided on #199, 2026-11-08). The daily check-in sent a random user ID, a hash of the remote, the version, OS and architecture from every daemon every day, behind an opt-out. A public repo's address is already public and its bus is readable by anyone, so the counts can come from the bus (the census, still open in #199), and Murmur needs to send nothing else.

## Change

- **`internal/publicrepo`** replaces `internal/checkin`. `mur init`, `mur join` and daemon start look for `refs/murmur/reported`. Without it, they ask GitHub, signed out, whether the repo is public; if it is, they POST `{"repo": "github.com/<owner>/<name>"}` to `/v1/repo` and then push the ref (leased, create-only), so each public repo reaches us once in its life. A private or non-GitHub repo sends nothing and is looked at again once a UTC day. No opt-out. The User-Agent carries no version, so "only the address" holds to the byte. Dev builds and an empty version never report.
- **Old daemons** list and fetch the new ref but ignore it (they read only mesh, presence and names).
- **Site:** `/v1/repo` stores a repo only if GitHub shows it public (optional `GITHUB_TOKEN` secret raises the rate limit). `/v1/checkin` stores nothing but still answers with the latest release. Migration `0002_repos.sql`. `/stats` and `report.md` keep the check-in history, say it ended on 2026-11-08, and list the public repos.
- **`mur version`'s newer-release notice** now comes from the updater, which records the latest release in the state dir. With updates off it makes no request (an existing guarantee its test holds), so those users no longer get the notice.
- **Docs:** join.md, setup.md, README, llms.txt and VISION ("Counting use") say plainly that the only data collected is a public repo's name, and why: a count of repos and people. **The landing page gets one new line** under Setup saying the same; cut it if you'd rather not.

## Verified

- `go vet ./...` and `gofmt` clean. New tests in `internal/publicrepo` cover a public repo reported once, a second clone asking nobody, a private repo sending nothing and being re-checked the next day, an unstored report retried with no ref written, and dev and non-GitHub repos sending nothing. They pass, as do `internal/update` (with a new `Newer` test), `internal/join` and the rest except below.
- Locally, `cmd/mur`'s `TestInitJoinAndLeave` fails the same way on origin/main (agent config dirs on this machine), and `internal/daemon` hit the 10-minute timeout while other sessions were running Go tests on the same laptop; its only change is the renamed `Report` option, which tests leave unset. CI is the check for both.
- Site JS passes `node --check`. The `/v1/repo` pattern was checked against real and malformed addresses. Not run against Cloudflare: after merge it needs `npx wrangler d1 migrations apply murmur-usage --remote` and a deploy.

## Review

Pending: a Sol review of dabf293.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
