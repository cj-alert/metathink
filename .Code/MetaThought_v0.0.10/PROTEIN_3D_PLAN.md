# Rotatable 3D Protein view

User request October 28, 2026: make Protein view resemble a three-dimensional molecule and allow user rotation.

Use vanilla browser JavaScript/SVG with actual XYZ positions, perspective projection, depth sorting and shaded sphere glyphs. This is a visual representation of saved team membership/connections, not scientific or physical geometry. Keep Structure view and original evidence selection. No third-party package, renderer service, new model call or operational state change.

Mouse/touch drag rotates; wheel and visible controls zoom; reset restores the initial view. Distinguish click from drag. Persist orientation/zoom alongside existing instance-specific preferences so normal refresh preserves the view, and defer page refresh during an active drag. Include keyboard controls.

Verification: focused Goja math/state tests on the actual rendered script; confirm rotation changes projected positions and front/back order without changing underlying membership/IDs, controls preserve selections, and refresh preserves orientation. Build and republish the completed h instance through Publish-Dashboard.ps1. Verify HTTP output and the actual browser: visible dimensionality, drag rotation, zoom/reset, selection, refresh and Structure fallback. Capture a new screenshot and record evidence/decisions. Preserve prior screenshots and generated research outputs.
