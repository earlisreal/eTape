# Sound

Web Audio order-event cues and Session Transition Announcements. Inputs: normalized execution events, session transitions, and preferences; output: local audio. Unlock audio only through user gesture; failures never block execution UI. Test: `npx vitest run --project chrome-regressions` (includes the sound tests).

The first pointer or keyboard gesture unlocks the shared AudioContext and preloads
the four female-voice MP3s in `ui/public/audio/`. Settings select the recorded
Google US English or Microsoft Zira voice for both announcements. The Session voice setting
enables or disables those announcements, and its preview button plays both clips in sequence
when enabled.
Announcements also use the existing Enable sounds setting and master volume, with no browser speech synthesis or
runtime speech service. A muted, locked, loading, or failed player drops the
announcement without queuing it; only a workspace that starts playback claims
the shared transition token. Clips are copied into the Vite build and embedded
in release builds. Sound preference edits broadcast their config key so every
open workspace reloads the same settings. Recording details are in [the audio README](../../public/audio/README.md).
