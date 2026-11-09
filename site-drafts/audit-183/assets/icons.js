// Icons from Lucide 0.564.0 (ISC; see THIRD_PARTY_NOTICES), the set T3 Code uses, drawn at its
// 1.75 stroke.
const shapes = {
  panel: [["rect", { x: 3, y: 4, width: 18, height: 16, rx: 2 }], ["path", { d: "M9 4v16" }]],
  clock: [["circle", { cx: 12, cy: 12, r: 9 }], ["path", { d: "M12 7v5l3 2" }]],
  copy: [["rect", { x: 8, y: 8, width: 14, height: 14, rx: 2 }], ["path", { d: "M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" }]],
  search: [["circle", { cx: 11, cy: 11, r: 8 }], ["path", { d: "m21 21-4.3-4.3" }]],
  chevronDown: [["path", { d: "m6 9 6 6 6-6" }]],
  check: [["path", { d: "M20 6 9 17l-5-5" }]],
  branch: [
    ["path", { d: "M15 6a9 9 0 0 0-9 9V3" }],
    ["circle", { cx: 18, cy: 6, r: 3 }],
    ["circle", { cx: 6, cy: 18, r: 3 }],
  ],
  pr: [
    ["circle", { cx: 5, cy: 6, r: 3 }],
    ["path", { d: "M5 9v12" }],
    ["circle", { cx: 19, cy: 18, r: 3 }],
    ["path", { d: "m15 9-3-3 3-3" }],
    ["path", { d: "M12 6h5a2 2 0 0 1 2 2v7" }],
  ],
  prDraft: [
    ["circle", { cx: 18, cy: 18, r: 3 }],
    ["circle", { cx: 6, cy: 6, r: 3 }],
    ["path", { d: "M18 6V5" }],
    ["path", { d: "M18 11v-1" }],
    ["line", { x1: 6, x2: 6, y1: 9, y2: 21 }],
  ],
  folder: [
    [
      "path",
      {
        d: "M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z",
      },
    ],
  ],
  settings: [
    ["path", { d: "M14 17H5" }],
    ["path", { d: "M19 7h-9" }],
    ["circle", { cx: 17, cy: 17, r: 3 }],
    ["circle", { cx: 7, cy: 7, r: 3 }],
  ],
  message: [
    [
      "path",
      {
        d: "M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z",
      },
    ],
  ],
  chevronRight: [["path", { d: "m9 18 6-6-6-6" }]],
  arrowLeft: [
    ["path", { d: "m12 19-7-7 7-7" }],
    ["path", { d: "M19 12H5" }],
  ],
  broadcast: [
    ["path", { d: "M16.247 7.761a6 6 0 0 1 0 8.478" }],
    ["path", { d: "M19.075 4.933a10 10 0 0 1 0 14.134" }],
    ["path", { d: "M4.925 19.067a10 10 0 0 1 0-14.134" }],
    ["path", { d: "M7.753 16.239a6 6 0 0 1 0-8.478" }],
    ["circle", { cx: 12, cy: 12, r: 2 }],
  ],
  laptop: [
    [
      "path",
      {
        d: "M18 5a2 2 0 0 1 2 2v8.526a2 2 0 0 0 .212.897l1.068 2.127a1 1 0 0 1-.9 1.45H3.62a1 1 0 0 1-.9-1.45l1.068-2.127A2 2 0 0 0 4 15.526V7a2 2 0 0 1 2-2z",
      },
    ],
    ["path", { d: "M20.054 15.987H3.946" }],
  ],
  users: [
    ["path", { d: "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" }],
    ["path", { d: "M16 3.128a4 4 0 0 1 0 7.744" }],
    ["path", { d: "M22 21v-2a4 4 0 0 0-3-3.87" }],
    ["circle", { cx: 9, cy: 7, r: 4 }],
  ],
  arrowRight: [
    ["path", { d: "M5 12h14" }],
    ["path", { d: "m12 5 7 7-7 7" }],
  ],
  bellOff: [
    ["path", { d: "M10.268 21a2 2 0 0 0 3.464 0" }],
    ["path", { d: "M17 17H4a1 1 0 0 1-.74-1.673C4.59 13.956 6 12.499 6 8a6 6 0 0 1 .258-1.742" }],
    ["path", { d: "m2 2 20 20" }],
    ["path", { d: "M8.668 3.01A6 6 0 0 1 18 8c0 2.687.77 4.653 1.707 6.05" }],
  ],
  activity: [["path", { d: "M22 12h-4l-3 9L9 3l-3 9H2" }]],
  commit: [["circle", { cx: 12, cy: 12, r: 3 }], ["path", { d: "M3 12h6" }], ["path", { d: "M15 12h6" }]],
  alert: [["path", { d: "m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" }], ["path", { d: "M12 9v4" }], ["path", { d: "M12 17h.01" }]],
  play: [["path", { d: "M6 4l14 8-14 8z" }]],
  square: [["rect", { x: 5, y: 5, width: 14, height: 14, rx: 2 }]],
  tag: [["path", { d: "M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z" }], ["circle", { cx: 7.5, cy: 7.5, r: 0.5 }]],
  pencil: [["path", { d: "M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z" }]],
  inbox: [["path", { d: "M22 12h-6l-2 3h-4l-2-3H2" }], ["path", { d: "M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z" }]],
  reply: [["path", { d: "M20 18v-2a4 4 0 0 0-4-4H4" }], ["path", { d: "m9 17-5-5 5-5" }]],
  x: [["path", { d: "M18 6 6 18" }], ["path", { d: "m6 6 12 12" }]],
};

const NS = "http://www.w3.org/2000/svg";

export function icon(name, className = "") {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.75");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", ("icon " + className).trim());
  for (const [tag, attrs] of shapes[name]) {
    const el = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, String(v));
    svg.appendChild(el);
  }
  return svg;
}
