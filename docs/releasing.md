# Releasing

A release is a pushed tag. `.github/workflows/release.yml` builds `mur` for macOS, Linux and Windows
(amd64 and arm64) and publishes the binaries as a GitHub release, and `install.sh` and
`install.ps1` download from it.

## Cutting one

From a commit on `main` whose checks passed, and which a Codex (Sol) agent has reviewed whole for
bugs, security holes and anything else that shouldn't ship. That review is Tyler's rule
(2026-11-08): a release reaches every installed daemon within the hour (self-update), so a PR's
review isn't enough on its own; it never sees how the merged PRs fit together. Each finding is
fixed before the tag, filed as an issue if it can wait, or dropped if it isn't worth acting on.

```sh
git tag v0.1.0
git push origin v0.1.0
```

`gh run watch` follows the run; the release appears when it finishes.

- **The version** is the tag without its `v`, built into the binary with `-X main.version=...`, so
  `mur version` prints it. A plain `go build` shows the `0.1.0-dev` in `cmd/mur/main.go` instead.
- **A tag with a `-` in it** (`v0.2.0-pre.1`) is published as a prerelease. GitHub's "latest
  release" skips prereleases, so the installers' default finds only stable releases: while only
  prereleases exist, `install.sh` fails with "not found", and `MURMUR_VERSION=v0.2.0-pre.1` installs
  one.
- **A tag that isn't `vN.N.N` or `vN.N.N-suffix`** fails the run before anything is built.
- **What is published:** `mur-<os>-<arch>` (`.exe` on Windows) for darwin, linux and windows on
  amd64 and arm64, and `SHA256SUMS` listing them. The installers rely on those names.
- **To undo a release** (a bad build, or a test tag): `gh release delete <tag> --cleanup-tag --yes`.
  Re-running a tag's workflow needs its release deleted first, since the run creates the release.
  Installed daemons update to the latest stable release hourly and never go back, so deleting stops
  further updates but doesn't undo the ones taken. Try a build as a prerelease first: updates skip it.

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
  `install.sh`'s PATH edit doesn't reach it; anything that must find mur from there uses the
  absolute path the installer prints.

## Building from source

With Go 1.26 or later:

```sh
git clone https://github.com/a-Fig/murmur && cd murmur
go build -o ~/bin/mur ./cmd/mur      # anywhere on PATH; mur.exe on Windows
```

Such a build says `0.1.0-dev` in `mur version`, which is also why it never updates itself.
