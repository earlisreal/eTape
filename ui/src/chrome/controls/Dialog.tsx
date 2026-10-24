import { useId, useRef, type CSSProperties, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Button } from "./Button";
import { useDismissibleLayer } from "./useDismissibleLayer";

export function Dialog({ title, onClose, children, footer, width = 440, className = "", style,
  dismissible = true, testId, scrimTestId, closeLabel = "Close dialog", headerProps }: {
  title: string; onClose: () => void; children: ReactNode; footer?: ReactNode;
  width?: number; className?: string; style?: CSSProperties; dismissible?: boolean;
  testId?: string; scrimTestId?: string; closeLabel?: string;
  headerProps?: React.HTMLAttributes<HTMLElement>;
}): JSX.Element {
  const ref = useRef<HTMLDivElement>(null);
  const backdropStart = useRef<boolean | null>(null);
  const titleId = useId();
  useDismissibleLayer(ref, onClose, { modal: true, dismissible });
  return createPortal(
    <div className="dialog-scrim" data-testid={scrimTestId} data-drawing-ui="true"
      onPointerDown={(event) => { backdropStart.current = event.target === event.currentTarget; event.stopPropagation(); }}
      onMouseDown={(event) => { backdropStart.current = event.target === event.currentTarget; }}
      onClick={(event) => {
        event.stopPropagation();
        if (dismissible && event.target === event.currentTarget && backdropStart.current !== false) onClose();
        backdropStart.current = null;
      }}>
      <div ref={ref} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1}
        className={`dialog ${className}`} data-testid={testId} style={{ width, ...style }}>
        <header className="dialog-header" {...headerProps}>
          <h2 id={titleId}>{title}</h2>
          <Button variant="quiet" iconOnly aria-label={closeLabel} title="Close (Esc)" disabled={!dismissible}
            onPointerDown={(event) => event.stopPropagation()} onClick={onClose} className="dialog-close">×</Button>
        </header>
        <div className="dialog-body">{children}</div>
        {footer && <footer className="dialog-footer">{footer}</footer>}
      </div>
    </div>, document.body,
  );
}
