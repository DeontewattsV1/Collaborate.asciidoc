# Accessibility Statement

## Commitment

`Collaborate.asciidoc` aims to make its command-line tools, shared agent
terminal web view (`agentterm`), mobile SSH workflows, and documentation usable
by people with diverse abilities, including people who rely on screen readers,
keyboard-only navigation, or mobile accessibility tools.

Because this repository is an active experimental prototype, accessibility
work is ongoing. This statement documents what is implemented today, what
environments are supported, and the limitations we already know about.

## Supported Environments

The repository is built around text-first and terminal-native workflows:

- **Command-line tools (`collaborate`, `collab`, `agentterm`, `agent-registry-check`, `scripts/locker_agents.py`, `scripts/check.sh`):**
  All CLIs produce line-oriented plain text, JSON, or AsciiDoc to standard
  output and standard error without interactive full-screen curses/TUI cursor
  repositioning. This keeps output compatible with terminal screen readers and
  braille displays.
- **Shared agent terminal web view (`internal/agentterm/ui.html`):**
  - Declares `<html lang="en">` and a responsive viewport meta tag.
  - Uses semantic landmarks (`<header>`, `<main>`, `<aside>`) and heading
    hierarchy (`<h1>`, `<h2>`).
  - Streams live entries into `<div id="term" role="log" aria-live="polite">` so
    assistive technologies receive updates politely without stealing focus.
  - Adapts to narrow screens (`@media (max-width:820px)`) by stacking the
    sidebar below the log.
- **iPhone / mobile SSH workflow (`docs/IPHONE-SANDBOX.md`):**
  Designed to be operated over standard SSH and `tmux` from iOS using Secure
  ShellFish, with short snippet commands (`collab-status`, `collab-check`,
  `collab-terminal`).
- **Documentation:**
  Plain UTF-8 Markdown and AsciiDoc with explicit section headings, tables, and
  copy-ready shell snippets. (`docs/IPHONE-SANDBOX.md` also warns against
  smart-dash `--` mangling by word processors.)

## Known Limitations

The following limitations exist in the current codebase (verified by source
inspection; formal WCAG 2.1/2.2 testing and automated screen-reader audits are
**NOT RUN**):

1. **No formal accessibility audit yet:** No automated accessibility linter
   (such as axe-core or pa11y) or structured screen-reader testing (VoiceOver,
   NVDA, JAWS) is part of `scripts/check.sh` yet.
2. **`agentterm` web view (`internal/agentterm/ui.html`):**
   - Uses a fixed dark color scheme (`#0d1117` background, `#8b949e` muted
     text) with no light-mode toggle and no `prefers-contrast` or
     `prefers-reduced-motion` media rules.
   - Does not include a "skip to main content" link.
   - Entry-type prefixes and verification indicators use Unicode glyphs (`▶`,
     `■`, `♥`, `⚑`, `⚐`, `✱`, `✓`, `✗`, `#`) without `aria-hidden` or
     dedicated text alternatives for the verification column; verification
     details are attached via `title` attributes (mouse tooltips), which are
     often inaccessible to keyboard-only and touch/VoiceOver users.
   - Scrollable command output blocks (`<pre class="out">`) do not have
     explicit keyboard focusability (`tabindex="0"`) or region labels.
   - At viewports `<= 820px`, `.ts` (timestamp) cells are hidden with
     `display: none`.
3. **ANSI escape output in `scripts/check.sh`:**
   Outside GitHub Actions, `scripts/check.sh` prints bold ANSI escape sequences
   (`\033[1m`) for section headers and does not currently honor `NO_COLOR`.
4. **Third-party mobile SSH client dependency:**
   Mobile accessibility when using Secure ShellFish on iPhone depends on iOS
   VoiceOver, Dynamic Type, and terminal rendering behavior inside that
   third-party app.

## Reporting Accessibility Barriers

If you encounter an accessibility barrier in any CLI command, the `agentterm`
web view, CI output, or documentation:

1. Open an issue using the **Accessibility Barrier** template:
   [`.github/ISSUE_TEMPLATE/accessibility_barrier.yml`](.github/ISSUE_TEMPLATE/accessibility_barrier.yml)
   (`https://github.com/DeontewattsV1/Collaborate.asciidoc/issues/new?template=accessibility_barrier.yml`).
2. Describe the surface (CLI, `agentterm` web UI, mobile SSH, or docs), your OS
   / browser / assistive technology, what blocked you, and any workaround or
   suggested improvement.
3. If you prefer not to file a structured form, contact `@DeontewattsV1` via a
   blank GitHub issue without sharing sensitive personal or credential data.

Accessibility fixes are welcomed as standard pull requests under
[`CONTRIBUTING.md`](CONTRIBUTING.md).
