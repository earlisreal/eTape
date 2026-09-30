//go:build tray && !windows

package main

import "github.com/earlisreal/eTape/engine/internal/exec"

func confirmForceExitHeldOrders(exec.HeldShutdownSummary) bool { return false }
