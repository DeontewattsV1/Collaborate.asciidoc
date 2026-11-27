# Collaborate — native iOS client (reference source, NOT compiled)

`CollaborateApp.swift` is a single-file SwiftUI reference client carried over
from the v5 staging package. **It has not been compiled, type-checked, or run
in a simulator or on a device.** Only a Swift syntax parse was done (on Linux,
in the v5 package). Treat it as a starting point, not a shipped app.

What it shows:

- Native Liquid Glass on iOS 26+ via `GlassEffectContainer` and `glassEffect()`,
  falling back to `.regularMaterial` on iOS 17–25.
- `NavigationStack`, `PhotosPicker`, `fileImporter`, and a System/Light/Dark
  `preferredColorScheme` toggle.
- Local-only text messages and a sample persona.

What it does **not** do yet: authenticate, call the backend, upload
attachments, or persist chat. Before release it needs URLSession over HTTPS to
`/api/rooms/*` (see `docs/DEV-GUILD.md`, "Durable rooms"), the identity
credential stored in the Keychain, and server-side membership checks (already
enforced by the API). Adding a persona in the app must never grant room
permissions.

To try it: create an iOS App (SwiftUI) project in Xcode with the iOS 26 SDK,
set the deployment target to iOS 17+, and replace the generated App source with
this file. Requires a Mac with Xcode and, for devices, an Apple Developer account.
