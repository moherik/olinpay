# AwsemPay (`openpay.id`) admin console: design direction

Design direction for the AwsemPay (`openpay.id`) admin console (Next.js and shadcn/ui). Read
together with `antislop.md` (the filter); this file supplies the identity the
filter cannot invent.

The character was chosen by the product owner: a dense operations console. The
palette, type and density values below are a proposal built from that choice.
Agent-chosen visuals drift toward the model's default taste, so treat the exact
values as open until they are signed off. The reasons are written so a wrong one
is easy to spot and change.

## What this is

- Product: the internal console for AwsemPay (`openpay.id`), a payment orchestrator between
  Indonesian businesses and the country's payment gateways (PJP).
- Audience: owners, operators and the technical team. People who read this
  screen for hours, often under pressure when a payment is stuck.
- Job: configure the platform (merchants, providers, environments, routing, keys)
  and watch money and callbacks move. The console is an instrument panel, not a
  landing page.

Reading this as: an internal payment operations console for owners and operators,
in a dense data-first console language, dial ENERGY 1 / RHYTHM 2 / MOTION 1.

## Character

- Precise, quiet, unornamented. The data carries the weight, the chrome stays out
  of the way.
- Built for long sessions: calm contrast, aligned numbers, no visual noise that
  competes with a failing transaction.
- It must never read as a marketing page. No greeting hero, no stat-card
  wallpaper, no celebration states.

## Dials

| Dial | Value | What it means here |
| --- | --- | --- |
| ENERGY | 1 | Low ornament. The table or the record is the design. No gradients, glass, glow or decorative illustration. |
| RHYTHM | 2 | List screens stay uniform on purpose. Detail screens are shaped by the question they answer, so they differ from each other. |
| MOTION | 1 | Hover and focus transitions only, 120 to 150ms. No entrance animations, no scroll choreography, no loops. |

## Identity

The console is about two things: money and status. That is the motif, and it is
specific to this product rather than borrowed from a template.

- Every amount, count and identifier is set in tabular numerals so columns align
  and a changed digit is visible at a glance.
- The unified status set (pending, paid, expired, cancelled, failed, refunded,
  unknown) is the one visual language repeated in every list, detail view and
  filter. A reader learns it once.
- Reserved hue: the accent blue means action and current location, never state.
  Status has its own colors, so blue stays unambiguous.
- One focal point per screen. On a list screen the table is the focus. On a record
  screen the current status and its primary action are the focus; everything else
  defers.

## Palette

Two core colors plus one accent, with a separate semantic set for status. Neutrals
(the ink and grey ramp) are the base, not counted as core colors.

| Token | Value | Role | Why |
| --- | --- | --- | --- |
| canvas | `#F5F6F8` | app background | A cool light grey separates white panels from the page without a border on everything. |
| surface | `#FFFFFF` | panels, tables, forms | Maximum legibility for the content that matters. |
| surface-muted | `#EEF0F4` | table headers, code, inline metadata | A third level exists so headers and code blocks do not need borders to read as separate. |
| border | `#D8DCE3` | dividers, control outlines | One visible border weight; hierarchy comes from layering, not from heavier lines. |
| border-subtle | `#E6E9EF` | table row rules, quiet dividers | Keeps dense tables from turning into a grid of strong lines. |
| ink | `#14161A` | primary text | Near-black reads as a console, not as a soft admin theme. |
| text-secondary | `#4A5261` | labels, secondary values | Clear step down from ink while staying well above AA. |
| text-muted | `#5D6675` | timestamps, helper text | The floor of the ramp; below this it stops being readable at 12px, and it must clear AA on the muted surface too. |
| accent | `#1D4ED8` | primary actions, active nav, links | Blue carries no status meaning (green, red and amber are spoken for), so it stays a pure action signal. Deep enough to pass AA as text on white and as a button fill. |

### Status semantics

Status is a system, not decoration, and color is never the only carrier. Every
status renders a text label, so a reader who cannot separate the hues still reads
the state.

| Status | Text | Background |
| --- | --- | --- |
| paid | `#067647` | `#E7F6EE` |
| pending | `#B54708` | `#FDF3E7` |
| failed | `#B42318` | `#FEECEB` |
| expired | `#475467` | `#EEF1F5` |
| cancelled | `#475467` | `#EEF1F5` |
| refunded | `#175CD3` | `#EAF1FE` |
| unknown | `#475467` | `#EEF1F5` |

### Contrast evidence

Measured WCAG contrast ratios for the pairs above. All meet AA for normal text
(4.5:1). Numbers are computed, not assumed; re-measure after any value changes.

| Pair | Ratio |
| --- | --- |
| ink on white | 18.11:1 |
| ink on canvas | 16.75:1 |
| text-secondary on white | 7.86:1 |
| text-secondary on surface-muted | 6.89:1 |
| text-muted on white | 5.80:1 |
| text-muted on canvas | 5.36:1 |
| text-muted on surface-muted | 5.08:1 |
| accent on white | 6.70:1 |
| white on accent | 6.70:1 |
| paid on its background | 5.10:1 |
| pending on its background | 4.95:1 |
| failed on its background | 5.76:1 |
| expired on its background | 6.79:1 |
| refunded on its background | 5.28:1 |

### Dark theme

Same token names, different values, applied on `html.dark`. Both themes come from
one variable set, so a component names a token and never a colour. Panels separate
by border rather than fill in both modes, so the two share a shape. Blue stays the
accent; in dark it lightens so it still passes as text, and the primary button
flips to dark text on that lighter blue.

| Token | Light | Dark | Role |
| --- | --- | --- | --- |
| canvas | `#F5F6F8` | `#0E1116` | app background |
| surface | `#FFFFFF` | `#161A21` | panels, tables, forms |
| surface-muted | `#EEF0F4` | `#1E242C` | table headers, code, metadata |
| border | `#D8DCE3` | `#39424E` | dividers, control outlines |
| border-subtle | `#E6E9EF` | `#272E38` | row rules, quiet dividers |
| ink | `#14161A` | `#E8ECF2` | primary text |
| text-secondary | `#4A5261` | `#B0BAC8` | labels, secondary values |
| text-muted | `#5D6675` | `#8D97A5` | timestamps, helper text |
| accent | `#1D4ED8` | `#82A8FF` | actions, active nav, links |
| accent-hover | `#1A44BD` | `#9CBCFF` | hover on the accent fill |
| accent-soft | `#EAF1FE` | `#1C2942` | quiet button and active nav fill |
| on-accent | `#FFFFFF` | `#0B1017` | text drawn on the accent fill |
| overlay | `rgba(20,22,26,0.35)` | `rgba(0,0,0,0.6)` | dialog scrim |

Status pairs in dark:

| Status | Text | Background |
| --- | --- | --- |
| paid | `#4ED08A` | `#12261C` |
| pending | `#F0A93B` | `#2B1F0D` |
| failed | `#FF8078` | `#2E1618` |
| expired | `#B0BAC8` | `#242B34` |
| cancelled | `#B0BAC8` | `#242B34` |
| refunded | `#82A8FF` | `#17253F` |
| unknown | `#B0BAC8` | `#242B34` |

#### Dark contrast evidence

Same method as light, same AA target. All pairs pass.

| Pair | Ratio |
| --- | --- |
| ink on surface | 14.71:1 |
| ink on canvas | 15.95:1 |
| ink on surface-muted | 13.18:1 |
| text-secondary on surface | 8.89:1 |
| text-secondary on surface-muted | 7.96:1 |
| text-muted on surface | 5.90:1 |
| text-muted on surface-muted | 5.28:1 |
| text-muted on canvas | 6.40:1 |
| accent on surface (link) | 7.47:1 |
| accent on accent-soft | 6.22:1 |
| on-accent on accent | 8.17:1 |
| paid on its background | 8.12:1 |
| pending on its background | 8.01:1 |
| failed on its background | 6.92:1 |
| expired on its background | 7.28:1 |
| refunded on its background | 6.55:1 |

Divider and control borders are deliberately below 3:1 in both themes (light
border on surface 1.38:1, dark 1.71:1). They are decoration, not the only cue: a
field is identified by its label and its value, and panels by their fill step and
position. This is a stated tradeoff rather than an oversight, and dark borders
measure slightly stronger than light ones.

### Accent rule

The accent appears on primary buttons, the active nav item, focused links and
selected filters. It does not appear on status, on table rows, on card borders or
as a background wash. When everything is blue, nothing is.

## Typography

- Sans: IBM Plex Sans. Mono: IBM Plex Mono.
- Reason: Plex reads as an engineering tool rather than an advertising font, has
  true tabular figures, and holds up at the 12 to 13px sizes a dense table needs.
  It is a real choice with a reason, not the model's default roster (Inter, Geist,
  Space Grotesk). Mono is used only where monospacing carries meaning:
  identifiers, key prefixes, provider references, raw payloads, amounts in a
  column.
- Fallbacks: `IBM Plex Sans, "Segoe UI", system-ui, sans-serif` and
  `IBM Plex Mono, "SFMono-Regular", Menlo, monospace`.

| Use | Size | Weight |
| --- | --- | --- |
| Page title | 20px | 600 |
| Section title | 15px | 600 |
| Body and table cell | 13px | 400 |
| Table header | 12px | 500, letter-spacing 0.02em |
| Micro label and timestamp | 12px | 400 |
| Identifier and amount (mono) | 12.5px | 450, tabular |

No uppercase tracking-as-decoration. Table headers stay sentence case; uppercase
is reserved for the rare fixed abbreviation.

## Spacing and density

4px base scale.

| Element | Value |
| --- | --- |
| Control height | 32px |
| Table row height | 36px (40px on the transactions table) |
| Cell padding | 8px vertical, 12px horizontal |
| Panel padding | 16px |
| Gap inside a group | 8px |
| Gap between groups | 24px |
| Gap between sections | 32px |

Density is the point: rows stay tight so an operator can scan many at once, while
section gaps stay large enough that screens do not blur into one another. That gap
difference is the rhythm, not decoration.

### Mobile shell

The header remains sticky but only 48px tall on mobile so the working area stays
dominant. At the point the horizontal section navigation no longer fits, it is
replaced by a labeled **Menu** control. Menu opens an accessible dialog containing
every console destination, marks the current page, and returns focus to its
trigger when dismissed. Its destination rows, environment choices, and account
trigger use 44px targets on mobile; desktop returns to the 32px dense controls.

## Shape, elevation, motion

- Radius: 4px for controls, 6px for panels and tags. Nothing is pill-shaped; a
  status tag is a small rounded rectangle, not a capsule.
- Hierarchy is border-first and layer-first. Shadow is reserved for overlays that
  genuinely float above the page (dropdown, dialog, toast), never for cards and
  table rows.
- No glassmorphism, no glow, no gradient fills. A single-element rule: if a
  surface needs attention, it earns a border or the accent, not a glow.
- Motion: 120 to 150ms ease on hover, focus and disclosure. Honor
  `prefers-reduced-motion` by cutting transitions to zero. No pulsing dots, no
  floating elements, no animated counters.

## Components

shadcn/ui is the base, with density overrides:

- Table: compact variant, sticky header, zebra off (row rules instead), tabular
  numerals, right-aligned amount columns, one border color.
- Status tag: the text label plus the semantic color; never color alone.
- Forms: 32px controls, labels above the field at 12px, inline field errors,
  required marked by a word not only by an asterisk.
- Charts: only when a question is written into the title (for example "Failed
  payments per hour, last 24h"). Minimal axes, single accent, no 3D, no area
  gradient.
- Icons: one set, functional only. No sparkle, lightning, robot or magic glyphs.
  Icons may be dropped entirely when the label carries the meaning.
- States: every data screen ships an empty state that names the cause and the next
  action, a loading state that says what is loading, and an error state that names
  what failed. "No data available" is not one of these.

## Do not ship

- Stat-card wallpaper, a greeting hero, or a celebratory empty state.
- Invented numbers, deltas without a real comparison window, filler activity feeds.
- Emoji, decorative status dots, colored left stripes, pill badges.
- Gradient or glass surfaces, glow, or shadow on flat elements.
- A table that hides the field the operator actually decides on.

## Dark mode

Both themes ship. On a first visit with no stored choice the console follows the
operating system, and it keeps following the system until the reader picks Light
or Dark from the account menu. The choice is stored per browser and applied by a
small inline script in the document head, so a saved dark choice never flashes
light first.

Because every colour is a token, a theme switch is a variable change and no
component knows which theme is active. Both modes pass the same AA check, with the
measured ratios above. A shipped mode that breaks contrast or fonts is a defect, so
any future token change is re-measured in both themes.

## To confirm

1. Palette values, in particular the accent. Blue is recommended because it stays
   clear of status meaning; a different hue is fine if it keeps that property and
   passes AA.
2. IBM Plex Sans and Mono versus another pairing.
3. The dark-mode accent (`#82A8FF`) and the dark surface ramp, now that both themes
   are built and measured.
