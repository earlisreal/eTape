// React 19 no longer ships a global JSX namespace.
// Re-export from react so existing code referencing JSX.Element etc. still compiles.
import type { ReactElement, Component, JSX as ReactJSX } from "react";

declare global {
  namespace JSX {
    // eslint-disable-next-line @typescript-eslint/no-empty-object-type
    interface Element extends ReactElement {}
    // eslint-disable-next-line @typescript-eslint/no-empty-object-type
    interface ElementClass extends Component {}
    interface IntrinsicElements extends ReactJSX.IntrinsicElements {} // eslint-disable-line @typescript-eslint/no-empty-object-type
  }
}
