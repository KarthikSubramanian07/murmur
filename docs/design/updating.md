# Updating

Murmur updates itself by default (#192, Tyler, 2026-11-07), so fixes reach an installed daemon
without a person reinstalling it. `internal/update` checks GitHub's `a-Fig/murmur` releases/latest
at daemon startup and hourly. No repo information goes in these GET requests. Network failures are quiet and wait for the next hour.
A development build (`-dev`) never updates itself. Versions before this updater need one manual
install to acquire it.

The daemon downloads the `mur-<os>-<arch>[.exe]` asset and that release's `SHA256SUMS`, verifies
SHA-256, and stages a closed, synced executable beside the running binary's resolved symlink target.
The downloads never leave https (2026-11-09): a redirect from https to plain http, or an asset URL
that isn't https, fails the check, since whoever could serve the binary over plain http could serve
its checksum too. Hosts aren't limited, because GitHub has moved its asset host before and a daemon
refusing the next one could never update past it. Unix renames it atomically over the old file.
Windows renames the running exe to a unique backup, installs the staged file, and restores the
backup if installation fails. Backups still held by processes are cleaned on a later daemon start. A
lock beside the executable serializes clones sharing an install; a daemon whose executable has
already been replaced restarts onto it. Identity is checked again after downloading so a manual
reinstall during the download is left alone. An unwritable install (for example, a package
manager's) is logged once and left alone until restart. Successful updates log the old and new
versions in the daemon log.

After installation, the daemon waits until no message handoff is in flight and no page counts as
open, or until the page asks for the restart (#240), and until no key to its page that another
clone's page handed a tab is still good (#348), since the next daemon's page doesn't know it. From
then on it gives no keys, and the daemon asking for one asks the next daemon. It then blocks new handoffs and shuts down
normally, saving state and releasing the clone lock. It starts the installed binary with the same detached
process helper clients use, forwarding its own flags (including poll and idle settings). Sessions
reconnect through the existing grace and inbox restoration. Running MCP servers keep their loaded binary until their sessions
restart; if they need to launch a daemon they use the installed path. The protocol number above
still handles incompatible clients and daemons; release versions do not become a second protocol.

On restart the daemon hands the page's listening socket to the new daemon, which inherits it
(descriptor 3 on Unix, the same handle on Windows, named to it by `MURMUR_PAGE_LISTENER`), and
leaves the page's address and token beside it in `page.json` in its state directory. The port is
bound from the old daemon's last answer to the new one's first, so a tab's requests in between wait
in the socket's queue, and the new daemon answers them; the tab sees a new daemon and reloads onto
the new version. The reload waits while a person has text in a composer or a message that failed,
since those live only in the page (#242); the tab works with the new daemon until then. Each daemon serves the page from a listener made from a second hold on the socket,
which keeps it bound after the page's server closes its listener.

Decided 2026-11-09 (#255, from Sol's pre-release review). Before, the new daemon bound the old port
again and kept the token. In the gap, another user's process could take the port, be sent one of the
tab's requests with its token, and let the port go before the new daemon bound it, and the new
daemon then answered that token. Checking that the port is free can't prove it stayed free, so the
token now goes only with the socket: a daemon that gets `page.json` without a socket at that address
(a start through WMI, which can't hand handles over, or a socket it can't take up) serves the page
on a new port with a new token, and the tab needs `mur ui` again. Rotating the token on every
restart, with the tab fetching the new one, was set aside: whatever the tab holds, a page served on
its port in the gap could read (a reload there runs that page in the tab's origin), and the tab
can't tell who is listening. On Windows a socket stays tied to the I/O completion port of the
process that served it, so the new daemon detaches it before taking it up.

The page shows a newer release above Settings in its sidebar (a dot on Settings on a phone), with
the version in Settings: "Restart to update" while an installed update waits on the open page,
"Updating to X" while the daemon will install it, and "X is out" with the install command when
updates are off or the last check failed. It learns the latest release from the updater's last
check, else from the `latest.json` a daemon in this clone last recorded, and never asks GitHub
itself. With updates off nothing checks, so "X is out" shows only a release an earlier check
recorded. Builds from source (`-dev`) show no notice.

`git config --global murmur.update false` opts out; a repo's own git config overrides the global
value. `MURMUR_NO_UPDATE=1` opts out for a daemon started with that environment. The config is read
on each check. Agents explain updates and this separate opt-out before joining (`docs/agents/join.md`).
`git config murmur.channel prerelease` opts into prereleases and nightlies as well (#244, Tyler,
2026-11-08), so a release runs on our own machines before a stable tag sends it to everyone;
`docs/releasing.md` has the channels. A binary
shared across clones is shared across their update settings too: to prevent every clone from
updating it, use the global setting without local overrides.

The checksum catches corruption or a download inconsistent with the release. It does not prove
who published it or protect against a compromised GitHub account or release with a replaced
checksum file. Release signing is separate work (#193).
