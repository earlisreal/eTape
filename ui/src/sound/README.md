# Sound

Price alerts use the approved 650 ms Beacon cue: two short rounded 740 Hz pulses
and one longer pulse. The global price-alert listener handles only live deltas,
claims one playback per engine burst across all connections, and drops muted,
locked, disconnected, or overlapping playback. Unlock/unmute never replays a
missed cue. General → Sounds exposes its enable toggle and Preview; master enable
and volume apply to alerts. Monitoring and history continue without any window.

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

Scanner Sound is owned by mounted Scanner Panels, not by the window-global
sound hook. Each unmuted Scanner Panel subscribes to its local ScannerStore;
the existing 200 ms scanner-channel coalescing reduces multiple panels in one
workspace window to one cue, while separate windows may each play their local
cue. The Scanner Panel Header's persisted `scannerSoundMuted` setting removes
that panel from eligibility and defaults to unmuted. It does not change the
global Enable sounds, Scanner sound selection, volume, or General-settings
previews. Muted or absent panels never queue or replay missed hits.
