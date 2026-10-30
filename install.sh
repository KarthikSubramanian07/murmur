#!/bin/sh
# Installs t4 on macOS or Linux: downloads the latest release's binary for this machine, checks it
# against the release's SHA256SUMS, puts it in ~/.local/bin, and adds that folder to PATH if it
# isn't there. It never needs sudo. Windows has install.ps1.
#
#   curl -fsSL https://raw.githubusercontent.com/a-Fig/t4/main/install.sh | sh
#
# Environment:
#   T4_VERSION    install this release (v0.1.0 or 0.1.0) instead of the latest stable one
#   GITHUB_TOKEN  download through the GitHub API, which works while the repo is private
#   T4_REPO       fetch releases of this repo (owner/name) instead of a-Fig/t4
#
# docs/releasing.md says where the binaries come from and what the checksum does and doesn't prove.
set -eu

repo=${T4_REPO:-a-Fig/t4}

say() { printf '%s\n' "$*"; }
die() { printf 't4 install: %s\n' "$*" >&2; exit 1; }

[ -n "${HOME:-}" ] || die 'HOME is not set, so there is nowhere to install to.'
command -v curl >/dev/null 2>&1 || die 'curl is required and was not found.'

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*) die 'this is Windows: run install.ps1 in PowerShell instead.' ;;
  *) die "unsupported operating system $(uname -s); t4 has binaries for macOS, Linux and Windows." ;;
esac

case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m); t4 has binaries for amd64 and arm64." ;;
esac
# A shell running under Rosetta reports x86_64 on an Apple Silicon Mac; the hardware answers truthfully.
if [ "$os" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
  arch=arm64
fi

asset=t4-$os-$arch
dir=$HOME/.local/bin
tmp=$(mktemp -d "${TMPDIR:-/tmp}/t4-install.XXXXXX") || die 'could not make a temporary folder.'
trap 'rm -rf "$tmp"; rm -f "$dir/.t4.new.$$"' EXIT INT TERM

tag=
if [ -n "${T4_VERSION:-}" ]; then
  case $T4_VERSION in v*) tag=$T4_VERSION ;; *) tag=v$T4_VERSION ;; esac
fi

# fetch NAME: downloads that release asset to $tmp/NAME.
if [ -n "${GITHUB_TOKEN:-}" ]; then
  # releases/latest/download needs no login only on a public repo; a private one is read through
  # the API. The API names assets by id, and sends the file itself from a redirect that must not
  # get the token (its storage host rejects a request that carries two kinds of credentials).
  api=https://api.github.com/repos/$repo/releases
  if [ -n "$tag" ]; then api=$api/tags/$tag; else api=$api/latest; fi
  curl -fsS --retry 2 -H "Authorization: Bearer $GITHUB_TOKEN" -H 'Accept: application/vnd.github+json' \
    -o "$tmp/release.json" "$api" ||
    die "could not read the release at $api (is the token valid, with access to $repo, and does the release exist? 'latest' skips prereleases; pin one with T4_VERSION. To download without a token, unset GITHUB_TOKEN)."
  fetch() {
    # Asset objects list "url" (ending /releases/assets/<id>) before "name". Splitting on commas
    # puts every field on its own line, so this works whether or not the JSON is pretty-printed.
    id=$(tr ',' '\n' <"$tmp/release.json" | awk -v want="$1" '
      /"url": *"https:\/\/api\.github\.com\/repos\/[^"]*\/releases\/assets\/[0-9]+"/ {
        id = $0; sub(/.*\/assets\//, "", id); sub(/".*/, "", id)
      }
      /"name": *"/ {
        name = $0; sub(/.*"name": *"/, "", name); sub(/".*/, "", name)
        if (name == want && id != "") { print id; exit }
      }')
    [ -n "$id" ] || die "the release has no asset named $1."
    url=$(curl -fsS --retry 2 -o /dev/null -w '%{redirect_url}' -H "Authorization: Bearer $GITHUB_TOKEN" \
      -H 'Accept: application/octet-stream' "https://api.github.com/repos/$repo/releases/assets/$id") ||
      die "could not start the download of $1."
    [ -n "$url" ] || die "GitHub did not say where to download $1 from."
    curl -fsS --retry 2 -L -o "$tmp/$1" "$url" || die "the download of $1 failed."
  }
else
  if [ -n "$tag" ]; then base=https://github.com/$repo/releases/download/$tag
  else base=https://github.com/$repo/releases/latest/download; fi
  fetch() {
    curl -fsSL --retry 2 -o "$tmp/$1" "$base/$1" ||
      die "could not download $base/$1 (is the release published, and is $repo public? Otherwise set GITHUB_TOKEN)."
  }
fi

say "Downloading $asset${tag:+ ($tag)} from $repo..."
fetch "$asset"
fetch SHA256SUMS

# SHA256SUMS lines read "<hash>  <name>"; sha256sum marks binary files "<hash> *<name>".
want=$(awk -v n="$asset" '{ f = $2; sub(/^\*/, "", f); if (f == n) { print tolower($1); exit } }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "SHA256SUMS has no line for $asset."
# The file goes in on stdin: given a path, sha256sum puts a backslash before the hash if the path
# contains one.
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum <"$tmp/$asset" | awk '{ print tolower($1) }')
elif command -v shasum >/dev/null 2>&1; then got=$(shasum -a 256 <"$tmp/$asset" | awk '{ print tolower($1) }')
elif command -v openssl >/dev/null 2>&1; then got=$(openssl dgst -sha256 <"$tmp/$asset" | awk '{ print tolower($NF) }')
else die 'no way to compute a SHA-256 checksum (sha256sum, shasum or openssl), so the download was not installed.'
fi
[ "$got" = "$want" ] || die "the checksum of $asset does not match SHA256SUMS (wanted $want, got $got); nothing was installed."
say 'Checksum OK.'

mkdir -p "$dir" || die "could not create $dir."
# Copy next to the destination and rename over it: the rename is atomic, and replacing a running
# binary by rename is safe on macOS and Linux (copying over it would fail with "text file busy").
cp "$tmp/$asset" "$dir/.t4.new.$$" || die "could not write to $dir."
chmod 755 "$dir/.t4.new.$$"
mv -f "$dir/.t4.new.$$" "$dir/t4" || die "could not put t4 at $dir/t4."

# The rc file should hold the literal $HOME, not this shell's value of it.
# shellcheck disable=SC2016
line='export PATH="$HOME/.local/bin:$PATH"'
path_note=
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    # Which files to edit depends on which shells read them: zsh reads .zshenv in every
    # invocation; bash reads .bashrc when interactive and the first of the profile files when it
    # is a login shell (which is how Terminal.app on macOS and agents' "bash -lc" start it).
    files=
    case ${SHELL:-} in
      */zsh) files=${ZDOTDIR:-$HOME}/.zshenv ;;
      */bash)
        login=$HOME/.profile
        for f in .bash_profile .bash_login .profile; do
          if [ -f "$HOME/$f" ]; then login=$HOME/$f; break; fi
        done
        files="$HOME/.bashrc
$login"
        ;;
      */fish)
        files=$HOME/.config/fish/conf.d/t4.fish
        # shellcheck disable=SC2016
        line='set -gx PATH $HOME/.local/bin $PATH'
        ;;
      *) files=$HOME/.profile ;;
    esac
    changed=
    oldifs=$IFS
    IFS='
'
    for f in $files; do
      if [ -f "$f" ] && grep -qF '# added by the t4 installer' "$f"; then continue; fi
      mkdir -p "$(dirname "$f")"
      printf '\n# added by the t4 installer\n%s\n' "$line" >>"$f"
      changed="$changed $f"
    done
    IFS=$oldifs
    if [ -n "$changed" ]; then
      path_note="Added $dir to PATH in${changed}. Open a new terminal for it to take effect."
    else
      path_note="$dir is already added to PATH in your shell's startup files. Open a new terminal for it to take effect."
    fi
    if [ "$os" = darwin ]; then
      path_note="$path_note
Apps started from the Dock don't read those files, so they won't find t4 on PATH; run it by the absolute path below."
    fi
    ;;
esac

"$dir/t4" version || die "installed $dir/t4 but it did not run; the file is at that path."
say "Installed: $dir/t4"
if [ -n "$path_note" ]; then say "$path_note"; else say "$dir is already on PATH."; fi
