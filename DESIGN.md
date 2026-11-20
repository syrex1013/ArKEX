# Design System Specification: The Forensic Intelligence Framework

## 1. Overview & Creative North Star
### Creative North Star: "The Silent Sentinel"
In the world of forensic OSINT, clarity is the ultimate weapon, but precision is the ultimate authority. This design system moves away from the "noisy hacker" tropes of the 90s and instead embraces a high-end, editorial approach to data. We call this **The Silent Sentinel**: a visual philosophy that treats every pixel as a piece of evidence. 

The system rejects the "template" look by utilizing intentional asymmetry, deep atmospheric layering, and high-contrast typographic shifts. We do not use borders to define space; we use light, shadow, and tonal depth. The goal is an interface that feels like a redacted document coming to life—authoritative, secretive, and surgical.

---

## 2. Colors & Surface Philosophy
The palette is rooted in total darkness, utilizing a hierarchy of greys and blacks to create a "void" where only the most important data shines.

### The Palette
- **Primary (Intelligence Blue):** `#98cbff` (Fixed: `#cfe5ff`) — Used for verified data points and active analysis paths.
- **Secondary (Terminal Green):** `#f5fff3` (Container: `#27ff97`) — Used for successful connections, "low risk" status, and system health.
- **Tertiary (Evidence Amber):** `#ffb950` — Used for watchlists and person-of-interest markers.
- **Error (Risk Red):** `#ffb4ab` — Reserved strictly for critical system failures or high-threat alerts.

### The "No-Line" Rule
Standard 1px borders are strictly prohibited for sectioning. They create visual clutter that distracts from the forensic data. Instead:
- **Background:** Use `surface` (`#131313`) for the base.
- **Sectioning:** Define areas using `surface_container_low` (`#1c1b1b`) against the main `surface`. 
- **Nesting:** Use `surface_container_highest` (`#353534`) for the most granular interactive elements.

### The "Glass & Gradient" Rule
To achieve a premium, cutting-edge feel:
- **Floating Panels:** Use semi-transparent `surface_container` colors with a 12px to 20px `backdrop-blur`.
- **Atmospheric Glow:** Apply subtle radial gradients using `primary_container` at 5% opacity in the background of data-heavy screens to provide "visual soul" without sacrificing legibility.

---

## 3. Typography
The system uses a dual-font strategy to balance editorial authority with technical precision.

- **Display & Headlines (Space Grotesk):** This font brings a modern, geometric technicality to the UI. Use `display-lg` (3.5rem) for high-impact dashboard summaries and `headline-md` (1.75rem) for section titles.
- **Interface & Data (Inter):** Chosen for its exceptional legibility at small sizes. 
    - **Body-md:** Standard for all descriptive text.
    - **Label-sm:** Used for metadata. Always set in uppercase with +5% letter spacing to enhance the "classified" feel.
- **The Data Contrast:** Use the primary color (`#98cbff`) for dynamic data values and `on_surface_variant` (`#bec7d4`) for static labels to create an immediate visual hierarchy between "the question" and "the answer."

---

## 4. Elevation & Depth
In a forensic environment, depth represents the "drill-down" nature of investigations.

### The Layering Principle
Depth is achieved through **Tonal Layering** rather than drop shadows.
1. **Base Level:** `surface` (#131313) - The canvas.
2. **Structural Level:** `surface_container_low` - For navigation sidebars or header regions.
3. **Action Level:** `surface_container_high` - For cards or data modules.

### Ambient Shadows
If a modal or pop-over requires a shadow to lift it off the interface, use a "Tinted Shadow":
- **Blur:** 40px - 60px.
- **Opacity:** 6% of `#000000`.
- **Effect:** The shadow should feel like a natural falloff of light, not a hard edge.

### The "Ghost Border" Fallback
Where containment is essential for accessibility, use the **Ghost Border**:
- **Token:** `outline_variant` (#3f4852)
- **Opacity:** 15%
- **Weight:** 1px

---

## 5. Components
### Buttons
- **Primary:** High-contrast `primary` background with `on_primary` text. No border. 0.25rem (sm) radius.
- **Secondary:** `surface_container_highest` background with a `primary` "Ghost Border" at 20% opacity.
- **States:** Hover states should utilize a subtle glow effect (`box-shadow: 0 0 15px primary_container`).

### Cards & Data Modules
- **Rule:** Never use dividers.
- **Separation:** Use `spacing-scale` (32px) to separate content blocks. 
- **Detailing:** Add a 2px vertical accent line of `primary` or `secondary` to the left-hand side of a card to indicate the "Status" of the intelligence within.

### Forensic Input Fields
- **Style:** Underline-only or solid `surface_container_lowest` backgrounds. 
- **Focus:** When active, the background should shift to `surface_container_high` with a subtle `primary` glow at the base.

### Intelligence Chips
- **Selection:** Use `secondary_container` for "Positive Match" chips and `error_container` for "High Risk" chips.
- **Radius:** Always use `full` (9999px) for chips to contrast against the sharp, architectural angles of the rest of the UI.

---

## 6. Do’s and Don’ts

### Do:
- **Use White Space as a Tool:** Allow data to breathe. OSINT is dense; the UI should not be.
- **Leverage Monospacing:** Use monospaced numbers for coordinates, timestamps, and IP addresses to ensure vertical alignment.
- **Embrace Asymmetry:** Align high-level stats to the left and investigative tools to the right to create a non-linear, professional workflow.

### Don’t:
- **Don't use 100% white:** Use `on_surface` (#e5e2e1) to prevent eye strain during long investigative sessions.
- **Don't use standard shadows:** Avoid the "floating box" look. If it's on the screen, it should feel like it's part of the machine.
- **Don't use Rounded Corners > 8px:** Keep the `roundedness-scale` mostly at `sm` (2px) or `md` (6px) to maintain an authoritative, technical edge. Reserved `xl` only for floating glass modals.