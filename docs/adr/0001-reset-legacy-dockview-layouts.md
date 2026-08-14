# Reset legacy Dockview layouts for v8

Status: Accepted

Dockview 8.1 changed the eTape workspace layout contract. Instead of attempting a brittle v7 migration, eTape clears each legacy workspace once—including its panel list and Link Group focus state—then marks it as layout version 8; regenerated built-in presets remain available. Imports of a legacy layout are rejected with `Invalid layout`, while hotkeys remain independent and importable.

Within a v8 workspace, a one-panel Panel Group presents its full-width Panel Header as the drag handle. A multi-panel Panel Group presents native Tabs above the active panel's separate Panel Header; its Tabs are the drag handles. This is a renderer-only change, so existing v8 layouts remain valid without another reset or version bump.
