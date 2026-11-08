// AMLL TTML lyrics: search the AMLL lyric database and parse the TTML it
// hands back, without a DOM. Host supplies fetch/console; nothing else.

const SEARCH_URL = "https://amlldb.bikonoo.com/api/search-lyrics";
const RAW_URL = "https://amlldb.bikonoo.com/raw-lyrics/";
const TIMEOUT = 10000;

// Memory cache, positive and negative, keyed on title/artist/album. The
// caller owns persistence; this is just a per-process stop for repeat work.
const cache = new Map();

// --- small text helpers -------------------------------------------------

function normalize(s) {
  return String(s || "")
    .toLowerCase()
    .normalize("NFKC")
    .replace(/[\s\u3000]+/g, "")
    .replace(/[\u200b-\u200f\ufeff]/g, "")
    .replace(/[()[\]{}"'“”‘’.,!?。，！？、·:;：；~～\-—_…*]/g, "");
}

function normArtist(s) {
  // Artist credit often carries feat./with/& extras; cut them for matching.
  return normalize(String(s || "").split(/[(&，,]/)[0]);
}

function artistMatches(want, cands) {
  const w = normArtist(want);
  if (!w) return false;
  return cands.some((c) => {
    const a = normArtist(c);
    if (!a) return false;
    if (a === w) return true;
    // Containment both ways; but a short hit inside a longer name is a
    // coincidence ("me" in "soMEone"), so require length >= 3 there.
    const short = w.length < a.length ? w : a;
    return short.length >= 3 && (w.includes(a) || a.includes(w));
  });
}

// --- TTML parsing (no DOMParser) ----------------------------------------

const ENTITIES = {
  amp: "&", lt: "<", gt: ">", quot: '"', apos: "'",
  nbsp: "\u00a0", middot: "·",
};

function decodeEntities(s) {
  return s.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (m, n) => {
    if (n[0] === "#") {
      const cp = n[1] === "x" || n[1] === "X"
        ? parseInt(n.slice(2), 16)
        : parseInt(n.slice(1), 10);
      if (Number.isFinite(cp) && cp >= 0 && cp <= 0x10ffff) {
        try { return String.fromCodePoint(cp); } catch { return m; }
      }
      return m;
    }
    return ENTITIES[n] !== undefined ? ENTITIES[n] : m;
  });
}

// "12.5", "1:02.345", "1:02:03.456" -> ms. TTML times tick at 1/1000000s
// but the database ships plain decimal seconds, so a float parse is right.
function parseTime(s) {
  if (typeof s !== "string") return null;
  const parts = s.trim().split(":");
  if (parts.length < 1 || parts.length > 3) return null;
  let ms = 0;
  for (const p of parts) {
    if (!/^[0-9]+(\.[0-9]+)?$/.test(p)) return null;
    ms = ms * 60 + parseFloat(p);
  }
  return Math.round(ms * 1000);
}

// A tiny XML scanner: walks tags, tracks a stack, accumulates text. Local
// names only — the TTML namespaces never disambiguate anything we care
// about. Malformed input simply aborts and yields what parsed (or null).
function scanElements(xml, visit) {
  let i = 0;
  const n = xml.length;
  const stack = [];
  while (i < n) {
    const lt = xml.indexOf("<", i);
    if (lt === -1) {
      if (stack.length) visit.text(decodeEntities(xml.slice(i)));
      break;
    }
    if (stack.length) visit.text(decodeEntities(xml.slice(i, lt)));
    const gt = xml.indexOf(">", lt);
    if (gt === -1) break; // truncated tag: stop cleanly
    let raw = xml.slice(lt + 1, gt);
    i = gt + 1;
    if (!raw || raw[0] === "?" || raw[0] === "!") continue; // decl / comment / doctype
    let selfClose = false;
    if (raw.endsWith("/")) {
      selfClose = true;
      raw = raw.slice(0, -1);
    }
    const m = raw.match(/^(\/?[^\s/>]+)([\s\S]*)$/);
    if (!m) continue;
    const name = m[1].split(":").pop();
    const attrs = {};
    const re = /([:\w-]+)\s*=\s*("([^"]*)"|'([^']*)')/g;
    let am;
    while ((am = re.exec(m[2]))) {
      const an = am[1].split(":").pop();
      attrs[an] = decodeEntities(am[3] !== undefined ? am[3] : am[4]);
    }
    if (raw[0] === "/") {
      // close tag
      const top = stack.pop();
      if (top) visit.close(top);
      continue;
    }
    const el = { name, attrs };
    visit.open(el, stack[stack.length - 1] || null);
    if (selfClose) {
      visit.close(el);
    } else {
      stack.push(el);
    }
  }
  // Unclosed elements at EOF: close silently.
  while (stack.length) visit.close(stack.pop());
}

function parseTTML(xml) {
  if (typeof xml !== "string" || xml.indexOf("<") === -1) return null;
  const lines = [];
  let cur = null;       // current <p>
  let curWord = null;
  let roleTag = null; // role of the open x-translation span   // current timed <span>
  let bad = false;

  const visit = {
    open(el, parent) {
      if (bad) return;
      try {
        if (el.name === "p" && parent && parent.name === "div") {
          const start = parseTime(el.attrs.begin);
          const end = parseTime(el.attrs.end);
          if (start === null || end === null) return;
          cur = { start, end, text: "", translation: "", words: [] };
          lines.push(cur);
          return;
        }
        if (el.name === "span" && cur) {
          const role = el.attrs.role || el.attrs["ttm:role"];
          if (role) {
            if (role === "x-translation") roleTag = role;
            if (role !== "x-translation" && role !== "x-roman") {
              const start = parseTime(el.attrs.begin);
              const end = parseTime(el.attrs.end);
              if (start !== null && end !== null) {
                curWord = { start, end, text: "" };
                cur.words.push(curWord);
              }
            }
            return;
          }
          const start = parseTime(el.attrs.begin);
          const end = parseTime(el.attrs.end);
          if (start !== null && end !== null) {
            curWord = { start, end, text: "" };
            cur.words.push(curWord);
          }
        }
      } catch { bad = true; }
    },
    text(s) {
      if (bad || !cur) return;
      if (roleTag) {
        cur.translation += s;
      } else if (curWord) {
        curWord.text += s;
      } else {
        cur.text += s;
      }
    },
    close(el) {
      if (bad) return;
      try {
        if (el.name === "span") {
          if (roleTag && (el.attrs.role === "x-translation" || el.attrs["ttm:role"] === "x-translation")) roleTag = null;
          curWord = null;
        } else if (el.name === "p") {
          cur = null;
          curWord = null;
          roleTag = null;
        }
      } catch { bad = true; }
    },
  };

  try {
    scanElements(xml, visit);
  } catch {
    return null;
  }
  if (bad || !lines.length) return null;

  const out = [];
  for (const l of lines) {
    // Drop role-span content from the main text and from words.
    l.translation = l.translation.trim();
    for (const w of l.words) w.text = w.text.trim();
    const words = l.words.filter((w) => w.text && w.end > w.start);
    const mainText = (l.text + l.words.map((w) => w.text).join("")).trim();
    let text = mainText;
    if (words.length) {
      // TTML word spans omit inter-word whitespace. Restore it between Latin
      // tokens; CJK and kana remain joined exactly as they are sung.
      text = "";
      let previous = "";
      for (const w of words) {
        // A short capitalized fragment such as Hel + lo is one word. Lowercase
        // tokens and longer contractions such as Couldn + t are separate only
        // where a word boundary is possible; punctuation still joins directly.
        const separate = previous && /^[a-z0-9]/.test(previous) || previous.length >= 4;
        if (separate && /^[A-Za-z0-9]/.test(w.text) && !/\s$/.test(text)) text += " ";
        text += w.text;
        previous = w.text;
      }
    }
    if (!text && !l.translation) continue;
    out.push({
      start: l.start,
      end: l.end,
      text,
      translation: l.translation || undefined,
      words,
    });
  }
  return out.length ? out : null;
}

// --- search + download ----------------------------------------------------

function pickCandidate(list, track) {
  const wantTitle = normalize(track.name);
  const wantArtist = normArtist(track.artist);
  if (!wantTitle || !Array.isArray(list)) return null;
  let best = null;
  let bestScore = -1;
  for (const c of list) {
    if (!c || typeof c !== "object") continue;
    const titles = Array.isArray(c.titles) && c.titles.length ? c.titles
      : c.title ? [c.title] : [];
    const exact = titles.some((t) => normalize(t) === wantTitle);
    if (!exact) continue;
    const artists = Array.isArray(c.artists) && c.artists.length ? c.artists
      : c.artist ? [c.artist] : [];
    if (!artistMatches(track.artist, artists)) continue;
    const albums = Array.isArray(c.albums) && c.albums.length ? c.albums
      : Array.isArray(c.album) ? c.album : [];
    const na = normArtist(track.album);
    const albumBoost = na && albums.some((a) => {
      const x = normalize(a);
      return x && (x.includes(na) || na.includes(x));
    }) ? 1 : 0;
    const timed = typeof c.id === "string" && c.id.indexOf(".ttml") !== -1 ? 0.5 : 0;
    const score = 1 + albumBoost + timed;
    if (score > bestScore) {
      bestScore = score;
      best = c;
    }
  }
  return best;
}

async function fetchText(url, options) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT);
  try {
    const res = await fetch(url, { ...options, signal: ctrl.signal });
    if (!res.ok) return null;
    return await res.text();
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

export async function findAMLL(track) {
  if (!track || !track.name) return null;
  const key = [track.name, track.artist, track.album].join("\n");
  if (cache.has(key)) return cache.get(key);

  const result = await lookup(track);
  cache.set(key, result);
  return result;
}

async function lookup(track) {
  const body = JSON.stringify({ query: track.name, type: "title" });
  const raw = await fetchText(SEARCH_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
  if (!raw) return null;

  let list;
  try {
    list = JSON.parse(raw);
  } catch {
    return null;
  }
  const cand = pickCandidate(list, track);
  if (!cand || typeof cand.id !== "string") return null;

  const ttml = await fetchText(RAW_URL + encodeURIComponent(cand.id));
  if (!ttml) return null;
  const lines = parseTTML(ttml);
  if (!lines) return null;
  // Real word timings are the whole point; refuse word-less sheets.
  const withWords = lines.filter((l) => l.words.length);
  if (!withWords.length) return null;
  return { lines: withWords };
}

function parseAMLL(xml) {
  return parseTTML(xml);
}

// Test hook: clear the in-memory cache.
function __clearCache() {
  cache.clear();
}


// The lyrics on the taskbar. A plugin has no window to draw in, so the drawing
// is a program of ours: what plays goes down a pipe to it, and it animates the
// rest itself from the position it was handed.

const BIN = "bin/taskbar-lyrics-windows-amd64.exe";
const TICK = 250; // the helper animates on its own; this is how often we correct it
const RESPAWN = 2000;

let helper = null;
let starting = false;
let startedAt = 0;
let configSent = "";
let ticks = 0;
let lastId = null;
let lastPlaying = null;
let warned = false;
let lyricId = null;
let lyricRequest = 0;
let lyricLines = null;
let lyricSource = "original";

// Three sizes are all a strip this short needs; the rest follows from them.
// The heights run past the taskbar on purpose: the helper shrinks the type to
// fit whatever the bar really is, and a taller strip means bigger lyrics.
const SIZES = {
  small: { fontSize: 15, height: 34 },
  default: { fontSize: 20, height: 44 },
  large: { fontSize: 26, height: 56 },
};

// The settings, as the helper reads them.
function config() {
  const size = SIZES[sizeName()];
  return {
    height: size.height,
    fontSize: size.fontSize,
    fontFamily: word("fontFamily"),
    showTranslation: flag("showTranslation", true),
    showNext: flag("showNext", false),
    showWhenPaused: flag("showWhenPaused", true),
    color: colorName(),
    translationColor: colorName() === "auto" ? "auto" : dimmed(colorName()),
    shadow: flag("shadow", true),
    halo: number("halo", 65),
    position: positionName(),
    offsetX: number("offsetX", 0),
    offsetY: number("offsetY", 0),
  };
}

// colorName is the lyric colour; blank means let the helper read the taskbar and
// pick white or near-black for it, which is the only default that is never
// invisible on a bar the user has not told us about.
function colorName() {
  return word("color") || "auto";
}

// A setting is a word, in either language, so both are taken.
function sizeName() {
  switch (word("preset")) {
    case "小":
    case "small":
    case "s":
      return "small";
    case "大":
    case "large":
    case "l":
      return "large";
  }
  return "default";
}

function positionName() {
  // An earlier build had a bare "align"; honour it once the new key is blank.
  switch (word("position") || word("align")) {
    case "左":
    case "left":
    case "start":
      return "left";
    case "右":
    case "right":
    case "end":
      return "right";
  }
  return "center";
}

function word(key) {
  return String(host.setting(key) || "").trim().toLowerCase();
}

// The translation is the line's own colour, dimmer, so there is one colour to pick.
function dimmed(color) {
  const hex = String(color || "").replace("#", "");
  const rgb = hex.length === 8 ? hex.slice(2) : hex;
  return /^[0-9a-f]{6}$/i.test(rgb) ? "#B0" + rgb.toUpperCase() : "#B0FFFFFF";
}

function number(key, fallback) {
  const n = parseInt(host.setting(key), 10);
  return isNaN(n) ? fallback : n;
}

// on and off, true and false, 开 and 关; a blank means whatever the default is.
function flag(key, fallback) {
  switch (word(key)) {
    case "":
      return fallback;
    case "off":
    case "false":
    case "0":
    case "关":
    case "否":
      return false;
  }
  return true;
}

// The app publishes what plays for the plugins that follow it; an older app has
// no such thing, and then there is nothing to draw.
function nowPlaying() {
  if (typeof host.nowPlaying !== "function") {
    if (!warned) {
      warned = true;
      console.log("taskbar lyrics: this app has no host.nowPlaying, so there is nothing to follow");
    }
    return null;
  }
  return host.nowPlaying();
}

function write(message) {
  if (!helper) return false;
  try {
    helper.write(JSON.stringify(message) + "\n");
    return true;
  } catch (e) {
    forget(e);
    return false;
  }
}

// The helper is gone: the next tick starts it again, a moment later, so a
// program that keeps failing isn't started in a tight loop.
function forget(why) {
  if (why) console.log("taskbar lyrics: " + why);
  helper = null;
  configSent = "";
  ticks = 0;
  lastId = null;
  lastPlaying = null;
}

// What it says back: that it drew something, or why it stopped.
async function listen(program) {
  try {
    while (true) {
      const line = await program.readLine();
      if (line === null) break;
      let message;
      try {
        message = JSON.parse(line);
      } catch (e) {
        continue;
      }
      if (message && message.type === "error") console.log("taskbar lyrics: " + message.message);
      // Another MyGo instance already owns the taskbar, possibly elevated and
      // in a different window station. Wait instead of respawning every 2s;
      // a later setting change retries after that instance has closed.
      if (message && message.type === "single") {
        try { await program.wait(); } catch (e) {}
        if (helper === program) {
          startedAt = Date.now() + 60 * 60 * 1000;
          forget(null);
        }
        return;
      }
    }
  } catch (e) {
    // it was killed: the app closing, or a start that failed
  }
  if (helper === program) forget(null);
}

function start() {
  if (host.platform !== "windows" || helper || starting) return;
  if (Date.now() - startedAt < RESPAWN) return;
  startedAt = Date.now();
  starting = true;
  try {
    helper = host.spawn(BIN, []);
    listen(helper);
  } catch (e) {
    helper = null;
    console.log("taskbar lyrics: " + e);
  }
  starting = false;
}

function tick() {
  if (host.platform !== "windows") return;
  if (!helper) {
    start();
    return;
  }
  const want = JSON.stringify(config());
  if (want !== configSent && write({ type: "config", config: config() })) configSent = want;

  const now = nowPlaying();
  if (now !== null && now.id !== lyricId) {
    lyricId = now.id;
    lyricLines = null;
    lyricSource = "original";
    if (flag("amll", true)) loadAMLL(now);
  } else if (now !== null && lyricSource === "amll") {
    now.lines = lyricLines;
  }
  if (now === null) {
    if (lastId !== null) {
      write({ type: "now", now: null });
      lastId = null;
      lastPlaying = null;
    }
    return;
  }
  // A new track, or a pause, at once; the position once a second, as the
  // helper counts the time itself in between.
  ticks++;
  if (now.id !== lastId || now.playing !== lastPlaying || ticks % 4 === 0) {
    if (write({ type: "now", now })) {
      lastId = now.id;
      lastPlaying = now.playing;
    }
  }
}

// Only a response to the track that is still playing may replace its lyrics.
async function loadAMLL(track) {
  const request = ++lyricRequest;
  try {
    const result = await findAMLL(track);
    if (request !== lyricRequest || track.id !== lyricId) return;
    if (result && result.lines && result.lines.length) {
      lyricLines = result.lines;
      lyricSource = "amll";
      lastId = null;
      console.log("taskbar lyrics: AMLL word timings for " + track.name);
    }
  } catch (e) {
    if (request === lyricRequest) console.log("taskbar lyrics: AMLL unavailable; keeping original lyrics: " + e);
  }
}

// A setting changed: the helper takes the new ones on the next tick, at once.
export function setSetting() {
  configSent = "";
  if (!helper) startedAt = 0;
}

setInterval(tick, TICK);
tick();
