# Releasing

A release is a pushed tag. `.github/workflows/release.yml` builds t4 for macOS, Linux and Windows
(amd64 and arm64) and publishes the binaries as a GitHub release, and `install.sh` and
`install.ps1` download from it.

## Cutting one

From a commit on `main` whose checks passed:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`gh run watch` follows the run; the release appears when it finishes.

- **The version** is the tag without its `v`, built into the binary with `-X main.version=...`, so
  `t4 version` prints it. A plain `go build` shows the `0.1.0-dev` in `cmd/t4/main.go` instead.
- **A tag with a `-` in it** (`v0.2.0-pre.1`) is published as a prerelease. GitHub's "latest
  release" skips prereleases, so the installers' default finds only stable releases: while only
  prereleases exist, `install.sh` fails with "not found", and `T4_VERSION=v0.2.0-pre.1` installs one.
- **A tag that isn't `vN.N.N` or `vN.N.N-suffix`** fails the run before anything is built.
- **What is published:** `t4-<os>-<arch>` (`.exe` on Windows) for darwin, linux and windows on amd64
  and arm64, and `SHA256SUMS` listing them. The installers rely on those names.
- **To undo a release** (a bad build, or a test tag): `gh release delete <tag> --cleanup-tag --yes`.
  Re-running a tag's workflow needs its release deleted first, since the run creates the release.

## Installing from a release

The README has the one-line installs, and each script's header lists its options. This is what
those don't say:

- **What the checksum proves.** The binaries and `SHA256SUMS` come from the same release, so the
  check catches a damaged or truncated download, not a release someone with write access tampered
  with.
- **Unsigned binaries.** A binary downloaded by a browser is blocked by Gatekeeper on macOS and
  warned about by SmartScreen on Windows, because it isn't signed; a download from the command
  line avoids both (2026-10-29). That is why the installers download from the command line, and why
  the release page isn't the way to install.
- **Dock apps.** On macOS, an app started from the Dock doesn't get the Terminal's PATH, so
  `install.sh`'s PATH edit doesn't reach it; anything that must find t4 from there uses the
  absolute path the installer prints.

## While the repo is private

Until `a-Fig/t4` is public (#54), even the script can't be fetched without a login. On 2026-10-29
the way to install was:

```sh
gh api repos/a-Fig/t4/contents/install.sh -H 'Accept: application/vnd.github.raw' | GITHUB_TOKEN=$(gh auth token) sh
```

and in PowerShell (`Out-String` joins the script's lines into one input for `iex`):

```powershell
$env:GITHUB_TOKEN = gh auth token
gh api repos/a-Fig/t4/contents/install.ps1 -H 'Accept: application/vnd.github.raw' | Out-String | iex
```
