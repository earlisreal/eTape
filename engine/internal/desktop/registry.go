package desktop

import (
	"github.com/earlisreal/eTape/engine/internal/uistate"
)

type NativeWindow = uistate.NativeWindow
type WorkspaceRegistry = uistate.WindowRegistry

var ErrInvalidWorkspaceID = uistate.ErrInvalidWorkspaceID

// ValidateWorkspaceID keeps the native window name and URL identity stable.
func ValidateWorkspaceID(id string) error { return uistate.ValidateWorkspaceID(id) }

func WindowName(workspaceID string) string { return "workspace:" + workspaceID }

func NewWorkspaceRegistry(onEmpty func()) *WorkspaceRegistry {
	return uistate.NewWindowRegistry(onEmpty)
}
