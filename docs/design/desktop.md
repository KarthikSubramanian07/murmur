# The desktop app

Added 2026-11-15 (#23). Tyler: "murmur, desktop app. I want it. I want it to be blazingly fast &
performant & light weight." `mur app` shows [the page](page.md) in a window of its own: the same
page, from the same daemon, so the app and the browser can't drift apart. The code is
`internal/desktop` and `cmd/mur/app.go`.

## What the window adds

Only what a browser tab can't do:

- **Its own window and icon**, in the taskbar or the Dock, with a dark title bar when the page's
  theme is dark.
- **The unread count**: a dot on the taskbar button and the count in the notification-area icon's
  tooltip on Windows, a badge on the Dock icon on macOS, and "(3) Murmur" in the title everywhere.
- **A notification for each message to the person** that arrives while the window isn't in front,
  three at most at once. Windows shows a balloon from the notification-area icon, which Windows 10
  and 11 turn into a toast (none shows under Do not disturb), and clicking it brings the window up.
  macOS uses `osascript`, so the banner is Script Editor's: a binary outside an app bundle has no
  bundle identifier, which `UNUserNotificationCenter` needs. Linux uses `notify-send` where it is
  installed.
- **Links to other sites open in the browser**, never in the window.

## How the page and the window talk

The window gives the page two functions. `murmurApp` takes what the page shows (its host, the unread
count, the theme) and the messages to the person as they arrive; the page calls it only when one of
those changes (`tellApp` in `app.js`). `murmurOpen` opens a link in the browser; a script the window
runs in every document sends it every link to another origin. Any page in the window could call
either, so neither does anything a page shouldn't: they set a title and a count, show a
notification, and open an http or https address in the browser.

The page decides what is news, as it does for its screen reader announcements. The window decides
whether the person is looking, because a web view keeps its focus when its window is minimized:
`document.hasFocus()` stayed true in a minimized WebView2 (2026-11-15). Only Linux takes the page's
guess.

## The web view: glaze

The window is the system's web view: WebView2 on Windows, WKWebView on macOS, WebKitGTK on Linux.
It comes through [glaze](https://github.com/crgimenes/glaze), which loads it at run time with
purego, so there is no cgo.

- *Why not Electron:* it ships a browser engine of its own, about 100 MB, with each app.
- *Why not Wails v2:* it needs cgo on macOS and Linux, its own build tool, and a project laid out
  around assets it bundles, when the page here is served by the daemon.
- *Why not webview/webview_go:* cgo on every platform, and a C++ compiler on Windows.
- *What no cgo keeps:* the release job still cross-compiles every target from one Linux runner with
  `CGO_ENABLED=0` (`docs/releasing.md`), and no build needs a C toolchain.
- *What glaze costs:* it is young, with one maintainer (v0.0.83, November 2026). On Windows it calls
  `CreateWebViewEnvironmentWithOptionsInternal`, an undocumented export of the WebView2 runtime, so
  it ships no `WebView2Loader.dll`; a runtime that drops it makes `mur app` fail with an error that
  names it. It needs Go 1.27.1, so `go.mod` went from 1.26.0 to 1.27.1, with go1.27.3 as the
  toolchain releases are built with.
- *Tray and notifications aren't glaze's:* the author's `native` module has both, but its tray runs
  an event loop of its own, which can't share a process with the window's. Each platform's file here
  does them with a few system calls instead (`native_*.go`).

## In `mur`, except on Linux

The app is a command of `mur`, not a second binary, so the installers, the self-update and the
release stay as they are, and the app is always the daemon's version.

On Linux it is left out unless `mur` is built with `-tags desktop`. purego makes the Go runtime link
glibc dynamically there: a `mur` built with it is no longer static, and would fail to start where
glibc isn't at the usual path (Alpine, NixOS, a distroless container), for every command, the hook
included. On macOS a Go binary links the system's libraries anyway, and on Windows purego makes
plain system calls, so neither changes: `mur help` took 20 to 26 ms on the macOS server before and
after (2026-11-15).

## Which repo

A daemon serves one clone's page, so the app picks one:

1. the clone it was started in, as `mur ui` does, if that clone joined;
2. else the clone it showed last (`last.json` in `murmur/app` in the user's config directory);
3. else a joined clone whose daemon is running, else any joined clone (`internal/clones`).

The page's repo menu moves the window to another clone's page, as it moves a tab. The page tells
the window its host, the window finds the clone whose `daemon.json` names that host, and that clone
is the one remembered.

## Keeping its daemon

The app is someone waiting for messages, so its page doesn't mark its calls hidden when the window
is minimized, and its daemon doesn't exit while the window is open. A daemon can still go: a crash,
`mur stop`. Every 10 seconds the window asks for its clone's page, starting the daemon if it isn't
running, and goes to the new address when the daemon came back on another port. So `mur stop` with
the app open is undone within 10 seconds; leaving the repo isn't, as a clone that left is never
started again.

## Starting it

`mur app` starts the window as a process of its own and prints "Opened.", so the terminal can be
closed. The window's output goes to `app.log` beside `last.json`. `--wait` keeps it in the
foreground, `--devtools` lets the web view open its developer tools, and `--page ADDR` shows that
address and no clone's, for working on the app against the devserver
(`go run ./internal/web/devserver`).

## What it costs (2026-11-15)

Measured on the Windows 11 laptop (15 GB of RAM, other agents keeping the CPU between 40 and 100%)
and on the macOS server (macOS 12, Intel).

- **Size.** `mur` grows by 0.7 MB on windows/amd64 (27.9 to 28.6 MB) and by 0.5 MB on darwin/arm64.
  Linux's is unchanged, since the app isn't in it.
- **Start to first paint.** 0.9 to 1.3 s from launching `mur app --wait` to the page's first paint
  on Windows; about 0.5 s of it is WebView2 making its window. A tab in a browser that is already
  running paints about 0.2 s after the address reaches it (184 ms from navigation to first paint).
  A browser started cold couldn't be timed cleanly: Windows opened its window behind the one in
  front, and Chromium doesn't paint a window nothing can see.
- **Memory, idle.** On Windows the app is 7 processes, about 400 MB of working set and 190 MB
  private. A Chromium started for the page alone was 9 processes, 500 MB and 240 MB; one more tab in
  a browser already open costs its renderer, about 100 MB and 53 MB. WebView2 is Chromium, so on
  Windows the app is lighter than a browser started for it and heavier than a tab. On macOS the app
  was about 120 MB in all (mur 39 MB, WebKit's content process 69 MB, its network process 13 MB).
- **One flag.** The app runs WebView2's GPU work in its browser process (`--in-process-gpu`, unless
  `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` is already set). It took the app from 470 MB of working set
  and 310 MB private to 400 MB and 190 MB; a page of text needs no GPU process of its own.

## Unsigned

The binaries aren't signed, as `mur`'s never were. The installers download from the command line,
which macOS's Gatekeeper and Windows' SmartScreen don't check (`docs/releasing.md`), and `mur app`
is that same binary. A downloaded app bundle or installer would be blocked or warned about. Signing
would take:

- **macOS:** the Apple Developer Program ($99 a year), a Developer ID Application certificate,
  `codesign` with the hardened runtime, and `notarytool` to notarize each release, from a macOS
  runner or with the key on the Linux one. A bare binary can't have its ticket stapled, so Gatekeeper
  checks it online; an app bundle can.
- **Windows:** a code-signing certificate (an OV or EV one, a few hundred dollars a year, or Azure
  Trusted Signing at about $10 a month) and `signtool` or `osslsigncode` in the release job.
  SmartScreen still warns until a signature has built a reputation; EV and Trusted Signing build it
  faster.

## What it doesn't do

- No launcher: it starts from a terminal. A Start menu shortcut, an app bundle and a `.desktop` file
  would let it start like an app, and a bundle would make macOS's notifications Murmur's.
- One window per `mur app`: a second one opens a second window, with a second icon in the
  notification area.
- A notification brings the window up, on Windows, but doesn't open its conversation.
- WebView2 keeps its data in `%APPDATA%\mur.exe`, which `mur uninstall` doesn't remove.
