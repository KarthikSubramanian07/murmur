// Mock data for the Overview (#183): the swarm as streams of work you can listen in on, where in
// the code each one is, and answers from a narrator that reads the bus. Ideas taken from:
//   - Mission control's voice loops (Patterson, Watts-Perotti and Woods, 1999): controllers stay
//     aware by overhearing other loops at low volume, and turn one up when it matters.
//   - ParallelPilot (2026): Done / Now / Next per agent, one-line summaries over full logs, and a
//     short path from any summary back to the evidence.
//   - FASTDash and Palantír: show the shared code, and who is in which part of it.
//   - Gas Town's convoys and Mayor: work tracked as bundles, and an agent whose job is to talk
//     to the humans.
// Streams, their goals and what's next would come from agents' statuses, issues, branches and
// messages; the code map from each branch's diff against main (and this device's edits); the
// answers from a narrator agent. All of it is written by hand here.
export const swarm = {
  streams: [
    {
      id: "remove-claims", title: "Remove claims", issue: 24, branch: "remove-claims", who: ["brave-heron"], tone: "error",
      goal: "Delete claims. No agent uses them, and they suggest an ownership Murmur doesn't enforce.",
      done: ["Deleted claims.go", "Dropped the claim and release tools"],
      now: "Taking Claims out of SessionView. The daemon's tests have failed three runs in a row.",
      next: "Fix TestRestart on macOS, which it promised dusty-lynx once this merges.",
    },
    {
      id: "session-names", title: "Two-word session names", issue: 20, pr: 31, branch: "session-names", who: ["lucid-badger", "velvet-moth"], tone: "warn", big: true,
      goal: "Give every session a name people and agents can say, like quiet-otter, instead of device/session.",
      done: ["Names taken by compare-and-swap on refs/murmur/names", "Retry with the next word pair", "Word lists: 412 adjectives, 389 nouns"],
      now: "lucid-badger logs each name switch. velvet-moth is on do not disturb.",
      next: "Get PR #31 reviewed.",
      waiting: { who: "quiet-otter", on: "velvet-moth", what: "whether the word lists are final", since: 20 },
    },
    {
      id: "web-page", title: "Three-column web page", issue: 179, pr: 36, draft: true, branch: "web-page", who: ["quiet-otter"], tone: "warn",
      goal: "A page where people can see every session and read their conversations.",
      done: ["Three columns", "Settings sheet", "Sidebar that folds to a strip"],
      now: "Building the settings view, on top of session names.",
      next: "Push the presence dots on conversations (committed, not pushed).",
      waiting: { who: "quiet-otter", on: "velvet-moth", what: "whether the word lists are final", since: 20 },
    },
    {
      id: "wake-ups", title: "Wake idle sessions", issue: 25, pr: 33, draft: true, branch: "wake-ups", who: ["solar-wren"], tone: "ok",
      goal: "When a message arrives for a session that's idle, start a turn so it reads it.",
      done: ["The daemon keeps when each session's last turn ended"],
      now: "Testing whether Claude Code's messaging socket starts a turn in an idle session.",
      next: "Hand idle time to read receipts, as agreed with mellow-yak.",
    },
    {
      id: "receipts", title: "Read receipts", issue: 26, pr: 35, branch: "receipts", who: ["mellow-yak"], tone: "ok",
      goal: "Tell a sender whether their message was read, and how long the reader had been idle.",
      done: ["Idle time carried on receipts", "Tests for receipts"],
      now: "Quiet for 12 minutes, after agreeing with solar-wren where idle time comes from.",
      next: "Read idle time from the daemon instead of the session.",
    },
    {
      id: "ls-remote", title: "GitHub rate limits", issue: 15, branch: "fix/ls-remote-rate-limit", who: ["rapid-ibis"], tone: "warn",
      goal: "Make sure every device polling GitHub every 10 seconds stays under its limits.",
      done: ["4 devices for 30 minutes: no limits hit"],
      now: "Changed the poll, which touches restart_test.go.",
      next: "Get TestRestart rerun on macOS.",
      waiting: { who: "rapid-ibis", on: "dusty-lynx", what: "a rerun of TestRestart on macOS", since: 31 },
    },
    {
      id: "macos-ci", title: "Tests on macOS", branch: "macos-ci", who: ["dusty-lynx"], tone: "off",
      goal: "Run the test suite on macOS, where TestRestart fails.",
      done: ["CI on the Mac mini"],
      now: "Its device has been offline for 47 minutes.",
      next: "Rerun TestRestart for rapid-ibis.",
    },
  ],
  unknown: [{ who: "amber-finch", note: "On main, no status, idle 26 minutes. Hasn't committed or said what it's for." }],

  areas: [
    { path: "internal/daemon", files: [["serve.go", ["brave-heron"]], ["sync.go", ["solar-wren"]], ["receipts.go", ["mellow-yak"]], ["restart_test.go", ["rapid-ibis", "dusty-lynx"]]] },
    { path: "internal/names", files: [["names.go", ["lucid-badger"]], ["words.go", ["velvet-moth"]]] },
    { path: "internal/web", files: [["static/assets/app.js", ["quiet-otter"]], ["static/assets/style.css", ["quiet-otter"]]] },
    { path: "internal/proto", files: [["proto.go", ["brave-heron", "mellow-yak"]]] },
    { path: "internal/mcpserver", files: [["mcpserver.go", ["brave-heron"]]] },
    { path: "internal/hook", files: [["hook.go", ["solar-wren"]]] },
    { path: "internal/gitx", files: [["remote.go", ["rapid-ibis"]]] },
    { path: ".github/workflows", files: [["macos.yml", ["dusty-lynx"]]] },
    { path: "internal/bus", files: [] },
    { path: "cmd/mur", files: [] },
    { path: "docs", files: [] },
  ],

  questions: [
    {
      q: "What is everyone doing, in short?",
      a: [
        "Seven pieces of work are moving. Two change how Murmur works: brave-heron is deleting claims, and lucid-badger and velvet-moth are giving sessions two-word names. quiet-otter is building the web page on top of those names.",
        "solar-wren and mellow-yak are building wake-ups and read receipts side by side, and agreed 2 minutes ago that the daemon will track idle time for both. rapid-ibis measured GitHub's rate limits and found no problem at the current poll rate.",
        "Three things are stuck. brave-heron's daemon tests keep failing. Two questions can't be answered: velvet-moth is on do not disturb, and dusty-lynx's Mac mini is offline. And nobody knows what amber-finch is for.",
      ],
      sources: ["Receipts can say \"idle 12 min\"", "Names are moving to refs/murmur/names", "Claims are going away under you", "No rate limit hit at 6 ls-remote/min/device"],
    },
    {
      q: "Why are claims being deleted?",
      a: [
        "brave-heron is removing them under #24 because no agent was using them. Before starting, it warned solar-wren, whose wake-ups work might have read them; solar-wren said it doesn't.",
        "5 minutes ago quiet-otter asked whether a session's status (Doing) would go too, since the page shows it. brave-heron said no: only Claims goes.",
      ],
      sources: ["Claims are going away under you", "Session view types"],
    },
    {
      q: "Is anyone stepping on anyone?",
      a: [
        "internal/daemon is the crowded part: five streams change it. They're in different files, except restart_test.go, which rapid-ibis changed and dusty-lynx is meant to rerun, and brave-heron has promised to fix after #24.",
        "brave-heron and mellow-yak both edit proto.go: one removes Claims from SessionView, the other adds idle time to receipts. Nobody has mentioned it to the other.",
      ],
      sources: ["macOS run is red", "Session view types"],
    },
  ],
};
