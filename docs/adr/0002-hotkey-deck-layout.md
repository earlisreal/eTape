# Keep Hotkey Deck Layout independent of Action Template order

Status: Accepted

The old `templates` array determined both the settings-card order and the flat Hotkey Deck order. Persist a normalized, ordered set of template-ID rows plus one global, default-off hotkey-label preference in `OrderConfig`; treat legacy `template.deck` membership as migration input and a derived compatibility projection. This lets drag-and-drop change only the deck, preserves existing configurations and exports, and leaves action execution unchanged.
