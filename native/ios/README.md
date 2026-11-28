# Collaborate — native iOS client (reference source, NOT compiled)

`CollaborateApp.swift` is a single-file SwiftUI reference client for a
Liquid Glass-style Dev Guild chat UI. It has **not been compiled** in this
repository, and it is not the supported mobile path.

The free mobile path is the web app's PWA: open
[collaborate-asciidoc.fly.dev](https://collaborate-asciidoc.fly.dev) in
iPhone Safari and use **Add to Home Screen**. See
[`docs/DEPLOYMENT.md`](../../docs/DEPLOYMENT.md).

## Scope (current)

This reference is limited to the chat UI, not a full App Store product:

- glass-style navigation
- solid message bubbles
- a glass composer
- a translucent attachment sheet
- a plugin list
- adaptive photo controls
- system, light, and dark appearances
- reduced motion, reduced transparency, and increased contrast
- Dev Guild art

## Trying it in Xcode

To try it: create an iOS App (SwiftUI) project in Xcode with the iOS 26 SDK,
add `CollaborateApp.swift`, and build against a simulator or device. Until
someone does that here, treat compile and runtime behavior as **UNVERIFIED**.
