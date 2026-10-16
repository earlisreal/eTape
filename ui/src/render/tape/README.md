# Tape Renderer

Print prices use four decimal places for positive prices below $1, otherwise
three, including numeric zero. Precision follows each print's raw price,
independently of the latest trade, and is identical in paint and hover rows.
Paused/filtered history retains that precision when the live price crosses $1.

Canvas time-and-sales painter over a bounded tick ring. Inputs: ordered ticks already stamped by the Go core/UI hub with Trade-Report Condition evidence, delivery source, independent eligibility permissions, and `none`, `large`, or `exceptional` significance; output: clean Price/Size/Time rows with Aggressor Direction-colored significance styling. Full condition evidence is available only through the default-off “Show details on hover” setting. Large and Exceptional affect price/size weight only; Exceptional adds a stronger direction tint and edge marker, while timestamps remain normal-weight and neutral stays neutral. The separate low-rate status store powers read-only settings text. Avoid allocations and React updates on the tick path. `tapeLayout.ts` is the shared responsive Price/Size/Time geometry used by the canvas and header: Time appears at 162px and Dockview keeps the panel at 100px minimum. Test: `npm test -- tape`.
