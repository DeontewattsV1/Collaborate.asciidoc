/*
 * Collaborate playground: a static, synthetic walkthrough of Collaborate Check.
 *
 * Isolation guarantees (enforced by tests/test_playground.py):
 * - No network: this file never calls fetch, XMLHttpRequest, WebSocket,
 *   EventSource, or sendBeacon, and playground.html sets connect-src 'none'.
 * - No storage, no accounts, no AI: every agent, person, PR, and SHA below is
 *   fictional and every message is scripted.
 * - The evaluation engine mirrors internal/ghcoord (hierarchical path
 *   overlap, earliest-opened PR holds the lease, reviews count only on the
 *   current head, human approval is reported separately from agent approval).
 */
(function (root) {
  "use strict";

  var AGENTS = {
    claude: { name: "Claude", kind: "ai", login: "claude[bot]", avatar: "avatars/claude.webp", evidence: "verified GitHub login" },
    codex: { name: "Codex", kind: "ai", login: "chatgpt-codex-connector[bot]", avatar: "avatars/codex.webp", evidence: "verified GitHub login" },
    copilot: { name: "Copilot", kind: "ai", login: "copilot-swe-agent[bot]", avatar: "avatars/copilot.webp", evidence: "verified GitHub login" },
    maya: { name: "Maya", kind: "human", login: "maya-example", avatar: "", evidence: "verified GitHub login (listed human)" }
  };

  function overlaps(a, b) {
    return a === b || b.indexOf(a + "/") === 0 || a.indexOf(b + "/") === 0;
  }

  function lease(pr) {
    return (pr.files || []).concat(pr.claims || []);
  }

  function overlapPaths(a, b) {
    var set = {};
    lease(a).forEach(function (x) {
      lease(b).forEach(function (y) {
        if (overlaps(x, y)) set[x.length >= y.length ? x : y] = true;
      });
    });
    return Object.keys(set).sort();
  }

  function opensFirst(a, b) {
    return a.opened !== b.opened ? a.opened < b.opened : a.number < b.number;
  }

  // evaluate returns the coordination report for state.target, mirroring the
  // Go checker's PASS / BLOCKED decision for a complete scan.
  function evaluate(state) {
    var prs = state.prs || [];
    var target = prs.filter(function (p) { return p.number === state.target; })[0];
    if (!target) throw new Error("unknown target PR " + state.target);
    var others = prs.filter(function (p) { return p.number !== target.number && p.state === "open"; });
    var report = {
      schema_version: "collaborate.check/v1",
      pr: target.number,
      head_sha: target.head,
      pr_state: target.state,
      lease_held: target.state === "open",
      coverage: { complete: true, other_open_compared: others.length, other_open_listed: others.length, gaps: [] },
      conflicts: [],
      blocking_conflict_count: 0,
      independent_approval: false,
      human_approval: false,
      changes_requested: false,
      enforce_independent_review: Boolean(state.policy && state.policy.requireIndependent),
      reviews: [],
      outcome: "PASS"
    };
    if (!report.lease_held) return report;
    others.forEach(function (o) {
      var paths = overlapPaths(target, o);
      if (!paths.length) return;
      var holder = opensFirst(target, o) ? target.number : o.number;
      var blocking = holder !== target.number;
      if (blocking) report.blocking_conflict_count++;
      report.conflicts.push({ other: o.number, agent: o.agent, paths: paths, holder: holder, blocking: blocking });
    });
    var latest = {};
    var order = [];
    (state.reviews || []).forEach(function (r) {
      if (r.pr !== target.number) return;
      if (!(r.reviewer in latest)) order.push(r.reviewer);
      latest[r.reviewer] = r;
    });
    order.forEach(function (k) {
      var r = latest[k];
      var who = AGENTS[r.reviewer];
      var v = {
        reviewer: r.reviewer,
        kind: who ? who.kind : "unknown",
        state: r.state,
        current: r.commit === target.head,
        independent: r.reviewer !== target.agent
      };
      report.reviews.push(v);
      if (v.state === "APPROVED" && v.current && v.independent) {
        report.independent_approval = true;
        if (v.kind === "human") report.human_approval = true;
      }
      if (v.state === "CHANGES_REQUESTED") report.changes_requested = true;
    });
    var failed = report.blocking_conflict_count > 0 ||
      (report.enforce_independent_review && (!report.independent_approval || report.changes_requested));
    report.outcome = failed ? "BLOCKED" : "PASS";
    return report;
  }

  var AUTH_PRS = [
    { number: 24, title: "Fix session refresh race", agent: "claude", opened: 1, state: "open", head: "3f9c2e1a7b",
      files: ["src/auth/session.ts"], claims: ["src/auth/token.ts"] },
    { number: 27, title: "Redesign login form", agent: "codex", opened: 2, state: "open", head: "a1b2c3d4e5",
      files: ["src/auth/session.ts", "src/ui/LoginForm.tsx"], claims: [] },
    { number: 25, title: "Docs: contributor guide", agent: "copilot", opened: 1.5, state: "open", head: "9d8e7f6a5b",
      files: ["docs/CONTRIBUTING.md"], claims: [] }
  ];

  function clone(v) { return JSON.parse(JSON.stringify(v)); }

  var SCENARIOS = [
    {
      id: "collide",
      title: "Two agents collide",
      blurb: "Claude and Codex independently touch the same authentication file. The check finds the overlap and shows which PR must yield.",
      steps: [
        { caption: "Claude opens PR #24 and declares its lease in the PR description.",
          prs: [AUTH_PRS[0], AUTH_PRS[2]], target: 24,
          say: [{ who: "claude", text: "Taking the session refresh race. Collaborate-Claim: src/auth/token.ts" }] },
        { caption: "Codex opens PR #27, which also changes src/auth/session.ts. Claude opened first, so Claude holds the lease.",
          prs: AUTH_PRS, target: 27,
          say: [{ who: "codex", text: "Opened #27 to redesign the login form." }] },
        { caption: "Codex narrows #27 to unclaimed files. The scan is complete and nothing overlaps, so the result is PASS.",
          patch: { 27: { files: ["src/ui/LoginForm.tsx"], head: "b7c8d9e0f1" } }, target: 27,
          say: [{ who: "codex", text: "Dropped session.ts from #27; I'll leave the auth change to Claude." }] }
      ]
    },
    {
      id: "handoff",
      title: "Agent handoff",
      blurb: "One agent finishes, releases its lease by merging, and hands the next task to another agent.",
      steps: [
        { caption: "Codex needs src/auth/session.ts too, so it asks for a handoff instead of fighting over the file.",
          prs: AUTH_PRS, target: 27,
          say: [{ who: "codex", text: "#27 overlaps #24 on src/auth/session.ts. Claude, can you hand it off when the fix lands?" },
                { who: "claude", text: "Yes. Finishing the race fix now." }] },
        { caption: "PR #24 merges. A merged PR no longer holds a lease, and reconciliation re-checks #27 automatically.",
          patch: { 24: { state: "merged" } }, target: 24, reconciled: [27],
          say: [{ who: "claude", text: "#24 merged. Handoff: src/auth/session.ts is yours, and token rotation in src/auth/token.ts is next." }] },
        { caption: "Codex picks up the next task and declares it before writing code. The lease is clear, so the result is PASS.",
          patch: { 27: { claims: ["src/auth/token.ts"], head: "c4d5e6f7a8" } }, target: 27,
          say: [{ who: "codex", text: "Took the handoff. Collaborate-Claim: src/auth/token.ts" }] }
      ]
    },
    {
      id: "review",
      title: "Verifiable review",
      blurb: "A human reviews the current commit, checks the evidence, and authorizes the next step. An approval of an older commit does not count.",
      steps: [
        { caption: "The repository policy requires an independent approval on the current head. Nobody has reviewed yet, so #27 is blocked.",
          prs: [{ number: 27, title: "Rotate refresh tokens", agent: "codex", opened: 2, state: "open", head: "a1b2c3d4e5", files: ["src/auth/token.ts"], claims: [] }],
          target: 27, policy: { requireIndependent: true }, reviews: [],
          say: [{ who: "codex", text: "#27 is ready for review." }] },
        { caption: "Codex approves its own work. The author's identity is not independent, so it is still blocked.",
          reviews: [{ pr: 27, reviewer: "codex", state: "APPROVED", commit: "a1b2c3d4e5" }],
          say: [{ who: "codex", text: "Approving #27." }] },
        { caption: "Maya, a human listed in the registry, reviews commit a1b2c3d, checks the evidence table, and approves it.",
          reviews: [{ pr: 27, reviewer: "codex", state: "APPROVED", commit: "a1b2c3d4e5" },
                    { pr: 27, reviewer: "maya", state: "APPROVED", commit: "a1b2c3d4e5" }],
          say: [{ who: "maya", text: "Reviewed a1b2c3d: tests cover rotation, and attribution is a verified login. Approved." }] },
        { caption: "Codex pushes another commit. Maya's approval was for the old head, so it no longer counts.",
          patch: { 27: { head: "e4f5a6b7c8" } },
          say: [{ who: "codex", text: "Pushed a follow-up fix for the expiry edge case." }] },
        { caption: "Maya re-reviews the current head and authorizes the merge.",
          reviews: [{ pr: 27, reviewer: "codex", state: "APPROVED", commit: "a1b2c3d4e5" },
                    { pr: 27, reviewer: "maya", state: "APPROVED", commit: "e4f5a6b7c8" }],
          say: [{ who: "maya", text: "Re-reviewed e4f5a6b. Approved. Go ahead and merge." }] }
      ]
    }
  ];

  // stateAt replays a scenario's steps up to index i (inclusive).
  function stateAt(scenario, i) {
    var state = { prs: [], reviews: [], policy: {}, target: null, chat: [], reconciled: [] };
    for (var s = 0; s <= i; s++) {
      var step = scenario.steps[s];
      if (step.prs) state.prs = clone(step.prs);
      if (step.patch) {
        Object.keys(step.patch).forEach(function (n) {
          state.prs.forEach(function (p) { if (String(p.number) === n) Object.assign(p, step.patch[n]); });
        });
      }
      if (step.reviews) state.reviews = clone(step.reviews);
      if (step.policy) state.policy = clone(step.policy);
      if (step.target) state.target = step.target;
      state.reconciled = step.reconciled ? step.reconciled.slice() : [];
      (step.say || []).forEach(function (m) { state.chat.push(m); });
      state.caption = step.caption;
    }
    return state;
  }

  var api = { AGENTS: AGENTS, SCENARIOS: SCENARIOS, evaluate: evaluate, overlaps: overlaps, stateAt: stateAt };
  root.CollaboratePlayground = api;

  if (typeof document === "undefined") return;

  // ---- Rendering (DOM only, textContent only: no HTML injection) ----
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }

  function avatar(who) {
    var a = AGENTS[who];
    if (a && a.avatar) {
      var img = el("img", "avatar");
      img.src = a.avatar;
      img.alt = "";
      img.width = 32;
      img.height = 32;
      return img;
    }
    return el("span", "avatar avatar-human", a ? a.name.charAt(0) : "?");
  }

  function short(sha) { return String(sha || "").slice(0, 7); }

  function renderChat(container, state) {
    container.replaceChildren();
    state.chat.forEach(function (m) {
      var row = el("div", "msg");
      row.appendChild(avatar(m.who));
      var body = el("div", "bubble");
      var a = AGENTS[m.who];
      body.appendChild(el("span", "who", a.name + (a.kind === "human" ? " · human" : " · AI agent")));
      body.appendChild(el("span", "text", m.text));
      row.appendChild(body);
      container.appendChild(row);
    });
    container.scrollTop = container.scrollHeight;
  }

  function renderBoard(container, state) {
    container.replaceChildren();
    state.prs.slice().sort(function (a, b) { return a.number - b.number; }).forEach(function (p) {
      var card = el("div", "pr" + (p.state !== "open" ? " pr-closed" : "") + (p.number === state.target ? " pr-target" : ""));
      var head = el("div", "pr-head");
      head.appendChild(avatar(p.agent));
      head.appendChild(el("strong", null, "#" + p.number + " " + p.title));
      card.appendChild(head);
      card.appendChild(el("div", "pr-meta", AGENTS[p.agent].name + " · " + p.state + " · head " + short(p.head)));
      var paths = el("ul", "paths");
      (p.files || []).forEach(function (f) { paths.appendChild(el("li", null, f)); });
      (p.claims || []).forEach(function (f) { paths.appendChild(el("li", "claim", f + " (claimed)")); });
      card.appendChild(paths);
      container.appendChild(card);
    });
  }

  function row(table, cells, th) {
    var tr = el("tr");
    cells.forEach(function (c) { tr.appendChild(el(th ? "th" : "td", null, c)); });
    table.appendChild(tr);
  }

  function renderComment(container, state, report) {
    container.replaceChildren();
    var card = el("article", "comment outcome-" + report.outcome.toLowerCase());
    card.appendChild(el("div", "comment-by", "github-actions[bot] · sticky comment on #" + report.pr));
    card.appendChild(el("h3", null, "Collaborate coordination: " + report.outcome));
    var status;
    if (!report.lease_held) status = "ℹ️ This PR is " + report.pr_state + "; it no longer holds a lease. Overlapping PRs are re-checked.";
    else if (report.blocking_conflict_count) status = "❌ " + report.blocking_conflict_count + " lease conflict(s): an earlier open PR already holds overlapping paths.";
    else if (report.outcome === "BLOCKED") status = "❌ Independent approval on the current head is required.";
    else status = "✅ No blocking coordination issue.";
    card.appendChild(el("p", null, status));
    if (report.blocking_conflict_count) {
      card.appendChild(el("p", "resolution", "Recommended resolution: narrow this PR to unclaimed files, or request a handoff from the lease holder."));
      var t = el("table");
      row(t, ["Path", "Lease holder"], true);
      report.conflicts.filter(function (c) { return c.blocking; }).forEach(function (c) {
        c.paths.forEach(function (p) { row(t, [p, AGENTS[c.agent].name + " · #" + c.holder]); });
      });
      card.appendChild(t);
    }
    var ev = el("table", "evidence");
    var target = state.prs.filter(function (p) { return p.number === report.pr; })[0];
    row(ev, ["Evidence", "Status"], true);
    row(ev, ["Checked head", short(report.head_sha)]);
    row(ev, ["Scan coverage", report.coverage.other_open_compared + "/" + report.coverage.other_open_listed + " other open PRs (complete)"]);
    row(ev, ["GitHub account attribution", AGENTS[target.agent].evidence]);
    row(ev, ["Underlying AI model identity", "not verified"]);
    if (report.enforce_independent_review || report.reviews.length) {
      row(ev, ["Independent approval on head", report.independent_approval ? "✅ yes" : "⏳ pending" + (report.enforce_independent_review ? " (required)" : "")]);
      row(ev, ["Independent human approval", report.human_approval ? "✅ on the current head" : "⏳ pending"]);
    }
    card.appendChild(ev);
    if (report.reviews.length) {
      var rv = el("table");
      row(rv, ["Reviewer", "Verdict", "On head", "Independent"], true);
      report.reviews.forEach(function (v) {
        row(rv, [AGENTS[v.reviewer].name + (v.kind === "human" ? " (human)" : " (agent)"), v.state, v.current ? "yes" : "no (stale)", v.independent ? "yes" : "no (author)"]);
      });
      card.appendChild(rv);
    }
    if (state.reconciled.length) {
      state.reconciled.forEach(function (n) {
        var r = evaluate({ prs: state.prs, reviews: state.reviews, policy: state.policy, target: n });
        card.appendChild(el("p", "reconciled", "Reconciled #" + n + ": re-checked after this change, now " + r.outcome + "."));
      });
    }
    card.appendChild(el("p", "footer", "Powered by Collaborate · open-source agent coordination"));
    container.appendChild(card);
  }

  function mount(doc) {
    var tabs = doc.getElementById("scenario-tabs");
    var blurb = doc.getElementById("scenario-blurb");
    var chat = doc.getElementById("chat");
    var board = doc.getElementById("board");
    var comment = doc.getElementById("comment");
    var caption = doc.getElementById("caption");
    var progress = doc.getElementById("progress");
    var prev = doc.getElementById("prev");
    var next = doc.getElementById("next");
    if (!tabs || !chat) return;
    var current = { scenario: 0, step: 0 };

    function render() {
      var sc = SCENARIOS[current.scenario];
      var state = stateAt(sc, current.step);
      var report = evaluate(state);
      Array.prototype.forEach.call(tabs.children, function (b, i) {
        b.setAttribute("aria-selected", String(i === current.scenario));
      });
      blurb.textContent = sc.blurb;
      caption.textContent = state.caption;
      progress.textContent = "Step " + (current.step + 1) + " of " + sc.steps.length;
      prev.disabled = current.step === 0;
      next.textContent = current.step === sc.steps.length - 1
        ? (current.scenario < SCENARIOS.length - 1 ? "Next scenario →" : "Start over")
        : "Next step →";
      renderChat(chat, state);
      renderBoard(board, state);
      renderComment(comment, state, report);
    }

    SCENARIOS.forEach(function (sc, i) {
      var b = el("button", "tab", (i + 1) + ". " + sc.title);
      b.type = "button";
      b.setAttribute("role", "tab");
      b.addEventListener("click", function () { current = { scenario: i, step: 0 }; render(); });
      tabs.appendChild(b);
    });
    prev.addEventListener("click", function () { if (current.step > 0) { current.step--; render(); } });
    next.addEventListener("click", function () {
      var sc = SCENARIOS[current.scenario];
      if (current.step < sc.steps.length - 1) current.step++;
      else current = { scenario: (current.scenario + 1) % SCENARIOS.length, step: 0 };
      render();
    });
    render();
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", function () { mount(document); });
  else mount(document);
})(typeof globalThis !== "undefined" ? globalThis : this);
