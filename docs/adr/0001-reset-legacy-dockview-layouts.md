# Reset legacy Dockview layouts for v8

Status: Accepted

Dockview 8.1 makes the eTape Panel Header a native custom tab, changing the persisted layout contract. Instead of attempting a brittle v7 migration, eTape clears each legacy workspace once—including its panel list and Link Group focus state—then marks it as layout version 8; regenerated built-in presets remain available. Imports of a legacy layout are rejected with `Invalid layout`, while hotkeys remain independent and importable.
