# Limit-if-Touched uses engine custody

Status: accepted and implemented.

Initial eTape Limit-if-Touched orders use engine custody at every Execution
Venue, even though moomoo exposes a native LIT type. This reuses the existing
durable parent/LIMIT-child lifecycle and keeps trigger, recovery, and order
display behavior consistent across venues instead of adding a second native
lifecycle. The trade-off is that no broker order exists before activation and
eTape depends on a healthy trigger feed; future native routing requires an
explicit custody change rather than an automatic fallback.
