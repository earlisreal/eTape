package alpaca

import (
	"github.com/earlisreal/eTape/engine/internal/exec"
	"github.com/earlisreal/eTape/engine/internal/session"
)

// FractionalStockOrderReason checks the exact Alpaca route before dispatch.
func (a *Adapter) FractionalStockOrderReason(req exec.OrderRequest) string {
	if req.Side != exec.SideBuy && req.Side != exec.SideSell {
		return "stock shorts and covers require whole shares"
	}
	if req.TIF != exec.TIFDay || req.Session == exec.SessionOvernight {
		return "fractional stocks require DAY during regular, pre-market or after-hours sessions"
	}
	status, _ := a.AssetStatus(req.Symbol)
	if req.Side == exec.SideBuy && (status.Fractionable == nil || !*status.Fractionable || status.Tradable == nil || !*status.Tradable) {
		return "stock is not eligible for a fractional long at this venue"
	}
	phase := session.PhaseAt(a.clk.Now())
	if phase != session.RTH && (phase != session.PreMarket && phase != session.PostMarket || req.Session == exec.SessionRTH) {
		return "fractional order is unavailable in the current session"
	}
	extended := req.Session == exec.SessionExtended || phase != session.RTH
	if extended {
		if req.Type != exec.TypeLimit || status.FractionalEHEnabled == nil || !*status.FractionalEHEnabled {
			return "fractional extended-hours orders require an eligible Alpaca DAY limit"
		}
	}
	return ""
}
