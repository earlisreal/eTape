// Hand-rolled TV-style icons. currentColor so the button controls color via CSS.
// Geometry is refined against TV screenshots during the manual fidelity pass.
import type { ReactNode } from "react";

function Svg({ size = 16, children }: { size?: number; children: ReactNode }): JSX.Element {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round"
      aria-hidden="true" focusable="false">
      {children}
    </svg>
  );
}
type P = { size?: number };

export const IconSearch = ({ size }: P) => <Svg size={size}><circle cx="11" cy="11" r="6" /><path d="M20 20l-4-4" /></Svg>;
export const IconCandles = ({ size }: P) => <Svg size={size}><path d="M7 4v4M7 16v4M7 8h0M7 8a1 1 0 011 1v6a1 1 0 01-2 0V9a1 1 0 011-1z" /><path d="M17 7v3M17 17v3M17 10a1 1 0 011 1v5a1 1 0 01-2 0v-5a1 1 0 011-1z" /></Svg>;
export const IconBars = ({ size }: P) => <Svg size={size}><path d="M7 5v14M7 8h-3M7 13h3" /><path d="M17 6v12M17 9h3M17 15h-3" /></Svg>;
export const IconLine = ({ size }: P) => <Svg size={size}><path d="M4 16l5-5 4 3 7-8" /></Svg>;
export const IconArea = ({ size }: P) => <Svg size={size}><path d="M4 16l5-5 4 3 7-8" /><path d="M4 16l5-5 4 3 7-8V20H4z" fill="currentColor" opacity="0.15" stroke="none" /></Svg>;
export const IconIndicators = ({ size }: P) => <Svg size={size}><path d="M4 12h3l2 6 3-14 2 8h6" /></Svg>;
export const IconCamera = ({ size }: P) => <Svg size={size}><path d="M4 8h3l2-2h6l2 2h3v11H4z" /><circle cx="12" cy="13" r="3.2" /></Svg>;
export const IconGear = ({ size }: P) => <Svg size={size}><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M5 5l2 2M17 17l2 2M19 5l-2 2M7 17l-2 2" /></Svg>;
export const IconCursor = ({ size }: P) => <Svg size={size}><path d="M6 4l12 8-5 1 3 6-3 1-3-6-4 3z" /></Svg>;
export const IconTrend = ({ size }: P) => <Svg size={size}><path d="M4 19L20 5" /><circle cx="5" cy="18" r="1.6" /><circle cx="19" cy="6" r="1.6" /></Svg>;
export const IconRay = ({ size }: P) => <Svg size={size}><path d="M4 19L20 5" /><circle cx="5" cy="18" r="1.6" /><path d="M20 5l-3 1M20 5l-1 3" /></Svg>;
export const IconHLine = ({ size }: P) => <Svg size={size}><path d="M3 12h18" /><circle cx="12" cy="12" r="1.6" /></Svg>;
export const IconHRay = ({ size }: P) => <Svg size={size}><path d="M4 12h16" /><circle cx="5" cy="12" r="1.6" /><path d="M20 12l-3-2M20 12l-3 2" /></Svg>;
export const IconRect = ({ size }: P) => <Svg size={size}><rect x="4" y="6" width="16" height="12" rx="1" /></Svg>;
export const IconMeasure = ({ size }: P) => <Svg size={size}><path d="M12 4v16M12 4l-3 3M12 4l3 3M12 20l-3-3M12 20l3-3" /></Svg>;
export const IconMagnet = ({ size }: P) => <Svg size={size}><path d="M6 4v7a6 6 0 0012 0V4h-4v7a2 2 0 01-4 0V4z" /></Svg>;
export const IconEye = ({ size }: P) => <Svg size={size}><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6-10-6-10-6z" /><circle cx="12" cy="12" r="2.5" /></Svg>;
export const IconEyeOff = ({ size }: P) => <Svg size={size}><path d="M2 12s3.5-6 10-6c2 0 3.7.6 5.2 1.4M22 12s-3.5 6-10 6c-2 0-3.7-.6-5.2-1.4" /><path d="M4 4l16 16" /></Svg>;
export const IconTrash = ({ size }: P) => <Svg size={size}><path d="M5 7h14M9 7V5h6v2M6 7l1 13h10l1-13" /></Svg>;
export const IconChevronDown = ({ size }: P) => <Svg size={size}><path d="M6 9l6 6 6-6" /></Svg>;
export const IconCornerArrow = ({ size }: P) => <Svg size={size}><path d="M15 15l4 4M19 15v4h-4" /></Svg>;
export const IconClose = ({ size }: P) => <Svg size={size}><path d="M6 6l12 12M18 6L6 18" /></Svg>;
export const IconMore = ({ size }: P) => <Svg size={size}><circle cx="5" cy="12" r="1.4" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none" /><circle cx="19" cy="12" r="1.4" fill="currentColor" stroke="none" /></Svg>;
export const IconClone = ({ size }: P) => <Svg size={size}><rect x="8" y="8" width="11" height="11" rx="1" /><path d="M5 15V6a1 1 0 011-1h9" /></Svg>;
