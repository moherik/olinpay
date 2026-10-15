import type { SVGProps } from "react";

/*
 * Icons drawn for this console's own vocabulary rather than imported from a
 * library set. Fourteen glyphs, one stroke weight, all functional.
 */

type IconProps = SVGProps<SVGSVGElement> & { size?: number };

function Base({ size = 16, children, ...props }: IconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      {...props}
    >
      {children}
    </svg>
  );
}

export const IconOverview = (p: IconProps) => (
  <Base {...p}>
    <path d="M4 14a8 8 0 0 1 16 0" />
    <path d="M12 14l4-4" />
    <path d="M3 20h18" />
  </Base>
);

export const IconTransactions = (p: IconProps) => (
  <Base {...p}>
    <path d="M4 8h13l-3-3" />
    <path d="M20 16H7l3 3" />
  </Base>
);

export const IconWebhooks = (p: IconProps) => (
  <Base {...p}>
    <circle cx="6" cy="6" r="2.2" />
    <circle cx="18" cy="18" r="2.2" />
    <path d="M8.2 6H14a4 4 0 0 1 4 4v5.8" />
  </Base>
);

export const IconMerchants = (p: IconProps) => (
  <Base {...p}>
    <path d="M4 9l1.4-4h13.2L20 9" />
    <path d="M4 9v10h16V9" />
    <path d="M9 19v-5h6v5" />
  </Base>
);

export const IconProviders = (p: IconProps) => (
  <Base {...p}>
    <rect x="4" y="4" width="16" height="6" rx="1.5" />
    <rect x="4" y="14" width="16" height="6" rx="1.5" />
    <path d="M8 7h.01M8 17h.01" />
  </Base>
);

export const IconUsers = (p: IconProps) => (
  <Base {...p}>
    <circle cx="9" cy="8" r="3" />
    <path d="M3.5 19a5.5 5.5 0 0 1 11 0" />
    <path d="M16 5.5a3 3 0 0 1 0 5.8" />
    <path d="M17.5 19a5.5 5.5 0 0 0-2-4.3" />
  </Base>
);

export const IconAudit = (p: IconProps) => (
  <Base {...p}>
    <path d="M6 3h9l4 4v14H6z" />
    <path d="M15 3v4h4" />
    <path d="M9 12h7M9 16h5" />
  </Base>
);

export const IconSearch = (p: IconProps) => (
  <Base {...p}>
    <circle cx="11" cy="11" r="6" />
    <path d="M20 20l-3.6-3.6" />
  </Base>
);

export const IconChevronDown = (p: IconProps) => (
  <Base {...p}>
    <path d="M6 9l6 6 6-6" />
  </Base>
);

export const IconChevronRight = (p: IconProps) => (
  <Base {...p}>
    <path d="M9 6l6 6-6 6" />
  </Base>
);

export const IconClose = (p: IconProps) => (
  <Base {...p}>
    <path d="M6 6l12 12M18 6L6 18" />
  </Base>
);

export const IconRetry = (p: IconProps) => (
  <Base {...p}>
    <path d="M20 12a8 8 0 1 1-2.3-5.6" />
    <path d="M20 4v5h-5" />
  </Base>
);

export const IconCheck = (p: IconProps) => (
  <Base {...p}>
    <path d="M5 12.5l4.5 4.5L19 7" />
  </Base>
);

export const IconAlert = (p: IconProps) => (
  <Base {...p}>
    <path d="M12 4l9 16H3z" />
    <path d="M12 10v4M12 17h.01" />
  </Base>
);

export const IconMore = (p: IconProps) => (
  <Base {...p}>
    <circle cx="5" cy="12" r="1.2" />
    <circle cx="12" cy="12" r="1.2" />
    <circle cx="19" cy="12" r="1.2" />
  </Base>
);

export const IconFilter = (p: IconProps) => (
  <Base {...p}>
    <path d="M4 6h16M7 12h10M10 18h4" />
  </Base>
);

export const IconPlus = (p: IconProps) => (
  <Base {...p}>
    <path d="M12 5v14M5 12h14" />
  </Base>
);

export const IconRouting = (p: IconProps) => (
  <Base {...p}>
    <circle cx="5" cy="12" r="2" />
    <circle cx="19" cy="6.5" r="2" />
    <circle cx="19" cy="17.5" r="2" />
    <path d="M7 12h2.5c3 0 5.5-2.5 5.5-5.5H17" />
    <path d="M7 12h2.5c3 0 5.5 2.5 5.5 5.5H17" />
  </Base>
);

export const IconLogout = (p: IconProps) => (
  <Base {...p}>
    <path d="M14 4h4v16h-4" />
    <path d="M10 12H4" />
    <path d="M7 9l-3 3 3 3" />
  </Base>
);
