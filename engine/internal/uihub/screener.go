package uihub

import "github.com/earlisreal/eTape/engine/internal/screener"

func (h *Hub) SetScreener(service *screener.Service) { h.cmd.screener.Store(service) }
