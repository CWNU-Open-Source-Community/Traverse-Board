# Frontend visual design

Use for building or refining a web interface, its visual hierarchy, responsive layout, interactions, or SVG/CSS motion. Start from the user's reference and the existing product. In Plan, explain the proposed design and checks; in Deliver, implement within the offered workspace and tool authority.

## Choose a direction

Inspect the page, components, tokens, assets, and actual content before changing them. Identify the primary user action and what must remain visible. For an existing product, preserve its visual language unless a redesign is requested. For a new surface, choose a coherent direction appropriate to the audience: type, color, spacing, density, imagery, and motion should support the same purpose. A reference communicates relationships and hierarchy, not permission to copy unrelated features.

Give primary content and actions visual priority. Use a small type scale, readable line lengths, consistent alignment, and deliberate spacing. Keep supporting status, queues, and secondary actions compact; disclose details when useful. Avoid filling space with repeated cards, decorative gradients, oversized panels, or placeholder metrics. Use real content and meaningful empty states. Choose responsive constraints from the content; test long labels and narrow widths instead of relying on a single desktop screenshot.

## Implement the experience

Reuse existing components and tokens where they fit. Keep loading, empty, success, error, disabled, and focus states consistent. Controls need a working action, an accessible name, keyboard access, and visible feedback. Preserve user input through errors. Use semantic HTML, adequate contrast, and usable targets without relying on color or hover alone. Add dependencies or assets only when they materially help the requested design.

Use motion to explain change. Animate the intended component with an appropriate origin and coordinate space; an animated parent does not prove its children behave correctly. For SVG, understand whether it is inline or an external image before choosing CSS selectors and transforms. Keep interaction responsive during animation, honor reduced motion, and implement pause/resume when the experience needs it. Do not add animation to a static request by default.

## Inspect the result

When a browser is available, open the actual page from the changed source. Inspect rendered screenshots at the relevant wide and narrow widths, then adjust hierarchy, density, clipping, and alignment. Exercise the primary action and relevant keyboard, error, and persistence paths. For requested motion, observe changes over time and verify each moving part and playback control; a still image is insufficient. Check console errors and missing assets.

Match evidence to the claim: a build or DOM assertion does not establish visual quality; a screenshot does not establish working controls. Inspect screenshots only if the current model/tools can view them. If browser or image inspection is unavailable, use the available checks and state the exact visual or interaction gap. Fix observed defects and repeat the affected check. Report what was actually rendered and exercised, with artifact paths and remaining limitations. Never describe a proposed check or captured-but-unseen image as a visual pass.

This skill supplies workflow guidance only. It does not grant execution, browser, network, or publication permissions.
