import test from "node:test";
import assert from "node:assert/strict";
import { createState } from "../src/state.js";

// fakeServer is the page's _state route in memory, with a log of requests.
function fakeServer() {
  const srv = { items: {}, revision: 0, log: [] };
  srv.fetch = async (url, init) => {
    srv.log.push(`${init.method} ${url}`);
    const reply = (status, body) => ({ ok: status < 300, status, json: async () => body });
    if (init.method === "GET") {
      const since = new URL(url, "http://x").searchParams.get("since");
      if (since !== null && Number(since) === srv.revision) return reply(200, { revision: srv.revision });
      return reply(200, { revision: srv.revision, items: JSON.parse(JSON.stringify(srv.items)) });
    }
    const { items } = JSON.parse(init.body);
    for (const [k, v] of Object.entries(items)) {
      if (v === null) delete srv.items[k];
      else srv.items[k] = v;
    }
    srv.revision++;
    return reply(200, { revision: srv.revision });
  };
  return srv;
}

// pollMs: 0 turns the timer off; these tests drive refresh() by hand.
function env() {
  const win = new EventTarget();
  const doc = new EventTarget();
  doc.visibilityState = "visible";
  return { win, doc, pollMs: 0 };
}

test("get, set, patch and delete round-trip through the page's route", async () => {
  const srv = fakeServer();
  const s = createState({ url: "/_state", fetchImpl: srv.fetch, ...env() });
  assert.equal(await s.get("missing"), null);
  assert.equal(await s.set("todo:1", { done: true }), 1);
  await s.patch({ a: 1, b: "two", "todo:1": null });
  assert.deepEqual(await s.all(), { a: 1, b: "two" });
  await s.delete("a");
  assert.deepEqual(srv.items, { b: "two" });
  assert.equal(s.revision, 3);
  // Values handed out are copies: mutating one changes nothing stored.
  const all = await s.all();
  all.b = "changed";
  assert.equal(await s.get("b"), "two");
  // One load, then every read is answered from memory.
  assert.equal(srv.log.filter((l) => l.startsWith("GET")).length, 1);
});

test("bad keys and values are refused before anything is sent", async () => {
  const srv = fakeServer();
  const s = createState({ url: "/_state", fetchImpl: srv.fetch, ...env() });
  await assert.rejects(s.set("a b", 1), TypeError);
  await assert.rejects(s.set("x".repeat(129), 1), TypeError);
  await assert.rejects(s.set("k", () => 1), TypeError);
  await assert.rejects(s.set("k", "x".repeat(64 * 1024)), RangeError);
  await assert.rejects(s.patch(Object.fromEntries(Array.from({ length: 257 }, (_, i) => [`k${i}`, i]))), RangeError);
  await assert.rejects(s.patch([1]), TypeError);
  assert.deepEqual(srv.log, []);
  // A failed write does not jam the ones after it.
  assert.equal(await s.set("ok", 1), 1);
});

test("change listeners hear the initial state, local writes and remote ones", async () => {
  const srv = fakeServer();
  srv.items = { seen: 1 };
  srv.revision = 5;
  const { win, doc } = env();
  const s = createState({ url: "/_state", fetchImpl: srv.fetch, win, doc, pollMs: 0 });
  const events = [];
  const windowEvents = [];
  win.addEventListener("byagent:state", (e) => windowEvents.push(e.detail.source));
  const stop = s.on("change", (e) => events.push(e));
  await s.all();
  assert.deepEqual(events[0], { source: "initial", revision: 5, items: { seen: 1 } });

  await s.set("mine", true);
  assert.deepEqual(events[1], { source: "local", revision: 6, changes: { mine: true } });

  // Another reader writes; the page notices on its next check.
  srv.items.theirs = "hi";
  delete srv.items.seen;
  srv.revision = 7;
  await s.refresh();
  assert.deepEqual(events[2], { source: "remote", revision: 7, changes: { theirs: "hi", seen: null } });
  // Nothing new: a check costs one small request and fires nothing.
  await s.refresh();
  assert.equal(events.length, 3);
  assert.ok(srv.log.includes("GET /_state?since=7"));

  assert.deepEqual(windowEvents, ["initial", "local", "remote"]);
  stop();
  await s.set("after", 1);
  assert.equal(events.length, 3);
});

test("a write that raced another reader's fetches the real state", async () => {
  const srv = fakeServer();
  const s = createState({ url: "/_state", fetchImpl: srv.fetch, ...env() });
  await s.all();
  srv.items.other = 1;
  srv.revision++; // someone else's write lands first
  await s.set("mine", 1);
  await new Promise((r) => setTimeout(r, 0));
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(await s.all(), { other: 1, mine: 1 });
});

test("server errors reject with the server's message", async () => {
  const s = createState({
    url: "/_state",
    fetchImpl: async () => ({ ok: false, status: 404, json: async () => ({ error: { code: "NOT_FOUND", message: "page state not found" } }) }),
    ...env(),
  });
  await assert.rejects(s.get("a"), (e) => e.name === "StateError" && e.status === 404 && /not found/.test(e.message));
});
