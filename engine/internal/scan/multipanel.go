package scan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotstockscreen"
	"github.com/earlisreal/eTape/engine/internal/session"
	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

var scannerIdentityPart = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

type backgroundRequester struct{ requester }

func (r backgroundRequester) Request(ctx context.Context, protoID uint32, req proto.Message) (opend.Frame, error) {
	return r.requester.Request(opend.WithBackgroundRequest(ctx), protoID, req)
}

type scannerPanelState struct {
	filters     wsmsg.ScannerFilters
	board       map[string]rankItem
	seen        map[string]map[string]bool
	baseline    bool
	discoveryAt time.Time
	status      string
}

type scannerConnection struct {
	workspaceID string
	sourceID    string
}

func scannerPanelID(workspaceID, panelID string) string {
	return wsmsg.ScannerIdentity(workspaceID, panelID)
}

func (p *Poller) SetScannerWorkspace(connID uint64, args wsmsg.SetScannerWorkspaceArgs) error {
	if connID == 0 || !scannerIdentityPart.MatchString(args.WorkspaceID) {
		return fmt.Errorf("invalid scanner workspace identity")
	}
	filtersByPanel := make(map[string]wsmsg.ScannerFilters, len(args.Panels))
	for _, panel := range args.Panels {
		if !scannerIdentityPart.MatchString(panel.PanelID) {
			return fmt.Errorf("invalid scanner panel identity")
		}
		if err := ValidateFilters(panel.Filters); err != nil {
			return err
		}
		if _, exists := filtersByPanel[panel.PanelID]; exists {
			return fmt.Errorf("duplicate scanner panel")
		}
		filtersByPanel[panel.PanelID] = panel.Filters
	}
	if args.SourceEnabled {
		if !scannerIdentityPart.MatchString(args.SourceWorkspaceID) || !scannerIdentityPart.MatchString(args.SourcePanelID) || args.SourceFilters == nil {
			return fmt.Errorf("invalid active Scanner Source")
		}
		if err := ValidateFilters(*args.SourceFilters); err != nil {
			return err
		}
	}
	if args.SourcePanelID != "" || args.SourceWorkspaceID != "" || args.SourceFilters != nil {
		if !scannerIdentityPart.MatchString(args.SourceWorkspaceID) || !scannerIdentityPart.MatchString(args.SourcePanelID) || args.SourceFilters == nil {
			return fmt.Errorf("invalid Scanner Source")
		}
		if err := ValidateFilters(*args.SourceFilters); err != nil {
			return err
		}
	}

	p.scannerLifecycleMu.Lock()
	defer p.scannerLifecycleMu.Unlock()
	p.mu.Lock()
	old := p.scannerWorkspaces[args.WorkspaceID]
	previousConnection := p.scannerConnections[connID]
	p.managedPanels = true
	p.scannerWorkspaces[args.WorkspaceID] = filtersByPanel
	for panelID, filters := range filtersByPanel {
		p.setPanelFiltersLocked(scannerPanelID(args.WorkspaceID, panelID), filters)
	}
	sourceIdentity := ""
	if args.SourceWorkspaceID != "" && args.SourcePanelID != "" && args.SourceFilters != nil {
		sourceIdentity = scannerPanelID(args.SourceWorkspaceID, args.SourcePanelID)
		p.setPanelFiltersLocked(sourceIdentity, *args.SourceFilters)
	}
	sourceID := ""
	if args.SourceEnabled {
		// Monitoring is the owner of source activity; it keeps the selected
		// board alive even after the source workspace's own window closes.
		sourceID = sourceIdentity
	}
	p.scannerConnections[connID] = scannerConnection{workspaceID: args.WorkspaceID, sourceID: sourceID}
	var deleted []string
	var paused []wsmsg.ScannerRankPayload
	for panelID := range old {
		if _, retained := filtersByPanel[panelID]; retained {
			continue
		}
		id := scannerPanelID(args.WorkspaceID, panelID)
		for otherID, connection := range p.scannerConnections {
			if connection.sourceID == id {
				connection.sourceID = ""
				p.scannerConnections[otherID] = connection
			}
		}
		if !p.scannerReferencedLocked(id) {
			delete(p.scannerPanels, id)
			deleted = append(deleted, id)
		}
	}
	if previousConnection.sourceID != "" && previousConnection.sourceID != sourceID {
		paused = append(paused, p.pauseScannerPanelLocked(previousConnection.sourceID)...)
	}
	idle := p.cancelPanelPollIfIdleLocked()
	p.mu.Unlock()
	if idle {
		p.stopScannerWork()
		p.pokeScannerPoll()
	}
	publishPausedScannerPanels(p.pub, paused)
	p.publishDeletedPanels(deleted)
	return nil
}

func (p *Poller) setPanelFiltersLocked(id string, filters wsmsg.ScannerFilters) {
	state := p.scannerPanels[id]
	if state == nil {
		p.scannerPanels[id] = &scannerPanelState{filters: filters, board: map[string]rankItem{}, seen: map[string]map[string]bool{}, baseline: true}
		return
	}
	if !sameFilters(state.filters, filters) {
		state.filters = filters
		state.board = map[string]rankItem{}
		state.baseline = true
		state.seen = map[string]map[string]bool{}
	}
}

func (p *Poller) SetPanelFilters(workspaceID, panelID string, filters wsmsg.ScannerFilters) error {
	if !scannerIdentityPart.MatchString(workspaceID) || !scannerIdentityPart.MatchString(panelID) {
		return fmt.Errorf("invalid scanner panel identity")
	}
	if err := ValidateFilters(filters); err != nil {
		return err
	}
	p.scannerLifecycleMu.Lock()
	defer p.scannerLifecycleMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	id := scannerPanelID(workspaceID, panelID)
	if p.scannerPanels[id] == nil {
		return fmt.Errorf("scanner panel is not registered")
	}
	p.setPanelFiltersLocked(id, filters)
	if p.scannerWorkspaces[workspaceID] == nil {
		p.scannerWorkspaces[workspaceID] = map[string]wsmsg.ScannerFilters{}
	}
	p.scannerWorkspaces[workspaceID][panelID] = filters
	return nil
}

func (p *Poller) ReleaseScannerConnection(connID uint64) {
	p.scannerLifecycleMu.Lock()
	defer p.scannerLifecycleMu.Unlock()
	p.mu.Lock()
	connection, exists := p.scannerConnections[connID]
	delete(p.scannerConnections, connID)
	var paused []wsmsg.ScannerRankPayload
	pausedIDs := map[string]bool{}
	pause := func(id string) {
		if id == "" || pausedIDs[id] {
			return
		}
		updates := p.pauseScannerPanelLocked(id)
		if len(updates) > 0 {
			pausedIDs[id] = true
			paused = append(paused, updates...)
		}
	}
	if exists {
		for panelID := range p.scannerWorkspaces[connection.workspaceID] {
			id := scannerPanelID(connection.workspaceID, panelID)
			pause(id)
		}
		pause(connection.sourceID)
	}
	idle := p.cancelPanelPollIfIdleLocked()
	p.mu.Unlock()
	if idle {
		p.stopScannerWork()
		p.pokeScannerPoll()
	}
	publishPausedScannerPanels(p.pub, paused)
}

// scannerPanelActiveLocked reports whether an open workspace or Monitoring
// source still owns this panel. Saved definitions alone do not keep it active.
func (p *Poller) scannerPanelActiveLocked(id string) bool {
	for _, connection := range p.scannerConnections {
		if connection.sourceID == id {
			return true
		}
		for panelID := range p.scannerWorkspaces[connection.workspaceID] {
			if scannerPanelID(connection.workspaceID, panelID) == id {
				return true
			}
		}
	}
	return false
}

func (p *Poller) RemoveScannerWorkspace(workspaceID string) {
	p.scannerLifecycleMu.Lock()
	defer p.scannerLifecycleMu.Unlock()
	p.mu.Lock()
	var deleted []string
	var retiredSources []string
	for panelID := range p.scannerWorkspaces[workspaceID] {
		deleted = append(deleted, scannerPanelID(workspaceID, panelID))
	}
	delete(p.scannerWorkspaces, workspaceID)
	for connID, connection := range p.scannerConnections {
		if connection.workspaceID == workspaceID {
			if connection.sourceID != "" && !strings.HasPrefix(connection.sourceID, workspaceID+"/") {
				retiredSources = append(retiredSources, connection.sourceID)
			}
			delete(p.scannerConnections, connID)
			continue
		}
		if strings.HasPrefix(connection.sourceID, workspaceID+"/") {
			connection.sourceID = ""
			p.scannerConnections[connID] = connection
		}
	}
	var paused []wsmsg.ScannerRankPayload
	for _, id := range retiredSources {
		if !strings.HasPrefix(id, workspaceID+"/") {
			paused = append(paused, p.pauseScannerPanelLocked(id)...)
		}
	}
	for id := range p.scannerPanels {
		if strings.HasPrefix(id, workspaceID+"/") {
			delete(p.scannerPanels, id)
			deleted = append(deleted, id)
		}
	}
	idle := p.cancelPanelPollIfIdleLocked()
	p.mu.Unlock()
	if idle {
		p.stopScannerWork()
		p.pokeScannerPoll()
	}
	publishPausedScannerPanels(p.pub, paused)
	p.publishDeletedPanels(deleted)
}

func (p *Poller) pauseScannerPanelLocked(id string) []wsmsg.ScannerRankPayload {
	if id == "" || p.scannerPanelActiveLocked(id) {
		return nil
	}
	state := p.scannerPanels[id]
	if state == nil {
		return nil
	}
	state.board = map[string]rankItem{}
	state.seen = map[string]map[string]bool{}
	state.baseline = true
	state.discoveryAt = time.Time{}
	state.status = "paused"
	updates := make([]wsmsg.ScannerRankPayload, 0, 4)
	for _, sessionName := range []string{"premarket", "rth", "afterhours", "overnight"} {
		updates = append(updates, wsmsg.ScannerRankPayload{
			ScannerID: id, Session: sessionName, Status: "paused",
			Rows: []wsmsg.ScannerRow{}, Baseline: true,
		})
	}
	return updates
}

func publishPausedScannerPanels(pub Publisher, payloads []wsmsg.ScannerRankPayload) {
	if pub == nil {
		return
	}
	for _, payload := range payloads {
		pub.Publish(wsmsg.TopicScannerRank, payload.ScannerID+"/"+payload.Session, payload)
	}
}

func (p *Poller) cancelPanelPollIfIdleLocked() bool {
	for _, connection := range p.scannerConnections {
		if connection.sourceID != "" || len(p.scannerWorkspaces[connection.workspaceID]) > 0 {
			return false
		}
	}
	if p.panelPollCancel != nil {
		p.panelPollCancel()
	}
	return true
}

func (p *Poller) pokeScannerPoll() {
	select {
	case p.poke <- struct{}{}:
	default:
	}
}

func (p *Poller) scannerReferencedLocked(id string) bool {
	for workspaceID, panels := range p.scannerWorkspaces {
		for panelID := range panels {
			if scannerPanelID(workspaceID, panelID) == id {
				return true
			}
		}
	}
	for _, connection := range p.scannerConnections {
		if connection.sourceID == id {
			return true
		}
	}
	return false
}

func (p *Poller) publishDeletedPanels(ids []string) {
	if p.pub == nil {
		return
	}
	for _, id := range ids {
		p.pub.Publish(wsmsg.TopicScannerRank, id, wsmsg.ScannerRankPayload{
			ScannerID: id, Status: "deleted", Rows: []wsmsg.ScannerRow{},
		})
	}
}

func (p *Poller) activeScannerPanels() []activeScannerPanel {
	p.mu.RLock()
	ids := map[string]bool{}
	for _, connection := range p.scannerConnections {
		for panelID := range p.scannerWorkspaces[connection.workspaceID] {
			ids[scannerPanelID(connection.workspaceID, panelID)] = true
		}
		if connection.sourceID != "" {
			ids[connection.sourceID] = true
		}
	}
	out := make([]activeScannerPanel, 0, len(ids))
	for id := range ids {
		if state := p.scannerPanels[id]; state != nil {
			out = append(out, activeScannerPanel{id: id, filters: state.filters})
		}
	}
	p.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

type activeScannerPanel struct {
	id      string
	filters wsmsg.ScannerFilters
}

type scannerRankCache struct {
	items       []rankItem
	at          time.Time
	lastAttempt time.Time
	lastErr     error
}

const scannerRankCadence = 2 * time.Second
const scannerVolumeCadence = 5 * time.Second

func (p *Poller) pollPanelsOnce(ctx context.Context, now time.Time, panels []activeScannerPanel) {
	p.mu.Lock()
	panels = p.activePanelSubsetLocked(panels)
	if len(panels) == 0 {
		p.mu.Unlock()
		return
	}
	workCtx, cancel := context.WithCancel(ctx)
	p.panelPollID++
	pollID := p.panelPollID
	if p.panelPollCancel != nil {
		p.panelPollCancel()
	}
	p.panelPollCancel = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		if p.panelPollID == pollID {
			p.panelPollCancel = nil
		}
		p.mu.Unlock()
	}()
	ctx = workCtx
	phase, poolDay := session.PhaseAt(now), session.PoolDay(now)
	sess := sessionKey(phase)
	oldDay := p.seenDay
	p.resetIfNewDay(now)
	dayChanged := oldDay != p.seenDay
	metricDay := int64(0)
	if metricDate, ok := scannerMetricDate(now); ok {
		metricDay = metricDate.UnixMilli()
	}

	p.mu.Lock()
	if phase == session.PostMarket && p.phaseSet && p.lastPhase != session.PostMarket {
		for _, panel := range panels {
			if state := p.scannerPanels[panel.id]; state != nil {
				state.board = map[string]rankItem{}
				state.seen = map[string]map[string]bool{}
				state.baseline = true
			}
		}
		p.premarketBootstrapped = false
	}
	if dayChanged {
		for _, panel := range panels {
			if state := p.scannerPanels[panel.id]; state != nil {
				state.seen = map[string]map[string]bool{}
				state.baseline = true
			}
		}
	}
	p.lastPhase, p.phaseSet = phase, true
	bootstrapped := p.premarketBootstrapped
	all := map[string]rankItem{}
	for _, panel := range panels {
		if state := p.scannerPanels[panel.id]; state != nil {
			for sym, item := range state.board {
				all[sym] = currentDailyItem(currentSessionItem(item, phase, poolDay), metricDay)
			}
		}
	}
	p.mu.Unlock()

	modes := map[string]bool{}
	for _, panel := range panels {
		modes[panel.filters.Mode] = true
	}
	if modes["most_active"] && phase != session.RTH {
		modes["gainers"], modes["losers"] = true, true
	}
	source := map[string][]rankItem{}
	sourceAt := map[string]time.Time{}
	sourceErr := map[string]error{}
	for _, mode := range []string{"gainers", "losers"} {
		if !modes[mode] {
			continue
		}
		items, at, err := p.cachedPanelRank(ctx, scannerRankKey(poolDay, sess+"/"+mode), now, scannerRankCadence, func() ([]rankItem, error) {
			return p.fetchRank(ctx, phase, mode)
		})
		if err != nil {
			sourceErr[mode] = err
		}
		source[mode], sourceAt[mode] = items, at
		if phase == session.RTH && !bootstrapped {
			pre, preAt, preErr := p.cachedPanelRank(ctx, scannerRankKey(poolDay, "bootstrap/"+mode), now, scannerRankCadence, func() ([]rankItem, error) {
				return p.fetchRank(ctx, session.PreMarket, mode)
			})
			if preErr == nil && len(pre) > 0 {
				source[mode] = append(append([]rankItem{}, source[mode]...), pre...)
				if sourceAt[mode].IsZero() || preAt.Before(sourceAt[mode]) {
					sourceAt[mode] = preAt
				}
			} else if preErr != nil {
				sourceErr[mode] = preErr
			}
		}
	}
	if modes["most_active"] && phase == session.RTH || modes["session_volume"] && phase == session.RTH {
		items, at, err := p.cachedPanelRank(ctx, scannerRankKey(poolDay, "rth/session_volume"), now, scannerVolumeCadence, func() ([]rankItem, error) {
			return p.fetchMostActiveRTH(ctx)
		})
		if err != nil {
			sourceErr["most_active"], sourceErr["session_volume"] = err, err
		}
		if modes["session_volume"] {
			source["session_volume"], sourceAt["session_volume"] = items, at
		}
		if modes["most_active"] {
			source["most_active"], sourceAt["most_active"] = items, at
		}
	}
	if modes["session_volume"] && phase != session.RTH {
		items, at, err := p.cachedPanelRank(ctx, scannerRankKey(poolDay, "session_volume/"+sess), now, scannerVolumeCadence, func() ([]rankItem, error) {
			return p.fetchSessionVolume(ctx, phase)
		})
		if err != nil {
			sourceErr["session_volume"] = err
		}
		source["session_volume"], sourceAt["session_volume"] = items, at
	}
	if modes["most_active"] && phase != session.RTH {
		combined := dedupeRankItems(append(append([]rankItem{}, source["gainers"]...), source["losers"]...))
		sort.Slice(combined, func(i, j int) bool { return combined[i].Volume > combined[j].Volume })
		source["most_active"] = combined
		sourceAt["most_active"] = minTime(sourceAt["gainers"], sourceAt["losers"])
		if sourceErr["gainers"] != nil {
			sourceErr["most_active"] = sourceErr["gainers"]
		} else if sourceErr["losers"] != nil {
			sourceErr["most_active"] = sourceErr["losers"]
		}
	}
	if phase == session.PreMarket && (!sourceAt["gainers"].IsZero() || !sourceAt["losers"].IsZero()) {
		p.mu.Lock()
		p.premarketBootstrapped = true
		p.mu.Unlock()
	}

	for _, items := range source {
		for _, item := range items {
			if item.sessionVolume != nil {
				item.sessionVolumePhase, item.sessionVolumePoolDay = phase, poolDay
			}
			all[item.Symbol] = mergeRankItem(all[item.Symbol], item)
		}
	}
	// Static exchange lookup and snapshots are shared by all panel sources and
	// sticky boards; never do one fetch per panel.
	union := make([]rankItem, 0, len(all))
	for _, item := range all {
		union = append(union, item)
	}
	p.resolveExch(ctx, union)
	union = dropOTC(union, p.otc)
	keep := map[string]rankItem{}
	for _, item := range union {
		keep[item.Symbol] = item
	}
	all = keep
	p.refreshPanelSnapshots(ctx, phase, all, poolDay)
	p.applyRelativeVolumes(now, all)

	var poolRows []wsmsg.ScannerRow
	poolSeen := map[string]bool{}
	type panelPublish struct {
		key     string
		payload wsmsg.ScannerRankPayload
		hits    []string
	}
	var publishes []panelPublish
	p.scannerLifecycleMu.Lock()
	p.mu.Lock()
	panels = p.activePanelSubsetLocked(panels)
	if len(panels) == 0 || ctx.Err() != nil {
		p.mu.Unlock()
		p.clearPool()
		p.scannerLifecycleMu.Unlock()
		return
	}
	for _, panel := range panels {
		state := p.scannerPanels[panel.id]
		if state == nil || !sameFilters(panel.filters, state.filters) {
			continue
		}
		for sym := range state.board {
			if updated, ok := all[sym]; ok {
				state.board[sym] = currentSessionItem(updated, phase, poolDay)
			}
		}
		for _, item := range source[panel.filters.Mode] {
			item, ok := all[item.Symbol]
			if !ok {
				continue
			}
			item = currentSessionItem(item, phase, poolDay)
			if len(rankRowsFiltered([]rankItem{item}, p.floats, panel.filters)) > 0 {
				state.board[item.Symbol] = item
			}
		}
		rows := make([]wsmsg.ScannerRow, 0, len(state.board))
		for sym, item := range state.board {
			item = currentSessionItem(item, phase, poolDay)
			state.board[sym] = item
			rows = append(rows, rankRowsFiltered([]rankItem{item}, p.floats, wsmsg.ScannerFilters{
				Mode: "session_volume", FloatUnit: panel.filters.FloatUnit, VolumeUnit: panel.filters.VolumeUnit,
				SessionVolumeUnit: panel.filters.SessionVolumeUnit,
			})...)
		}
		sortPanelRows(rows, panel.filters.Mode)
		if at := sourceAt[panel.filters.Mode]; !at.IsZero() {
			state.discoveryAt = at
		}
		state.status = "ready"
		if sourceErr[panel.filters.Mode] != nil {
			state.status = "delayed"
		}
		baseline := state.baseline
		state.baseline = false
		hits := panelNewHits(state, sess, rows)
		key := panel.id + "/" + sess
		publishes = append(publishes, panelPublish{key: key, hits: hits, payload: wsmsg.ScannerRankPayload{
			ScannerID: panel.id, Session: sess,
			RefreshedAt: formatScannerTime(panelSnapshotTime(rows, state.board, p.lastSnapshotAt)),
			DiscoveryAt: formatScannerTime(state.discoveryAt), Status: state.status,
			Rows: rows, Filters: panel.filters, Baseline: baseline,
		}})
		for _, row := range rows {
			if !poolSeen[row.Symbol] {
				poolSeen[row.Symbol] = true
				poolRows = append(poolRows, row)
			}
		}
	}
	p.mu.Unlock()
	if ctx.Err() != nil {
		p.scannerLifecycleMu.Unlock()
		return
	}
	p.updatePool(now, poolRows)
	p.enqueueRelativeVolumeForPool(now)
	if p.pub != nil {
		for _, publication := range publishes {
			p.overlayShortInterest(publication.payload.Rows, now)
			p.pub.Publish(wsmsg.TopicScannerRank, publication.key, publication.payload)
			for _, symbol := range publication.hits {
				p.pub.Publish(wsmsg.TopicScannerHit, publication.key, wsmsg.ScanHitPayload{
					ScannerID: publication.payload.ScannerID, Session: publication.payload.Session,
					Symbol: symbol, At: formatScannerTime(p.clk.Now()),
				})
			}
		}
	}
	p.scannerLifecycleMu.Unlock()
}

func (p *Poller) activePanelSubsetLocked(panels []activeScannerPanel) []activeScannerPanel {
	active := panels[:0]
	for _, panel := range panels {
		if !p.scannerPanelActiveLocked(panel.id) {
			continue
		}
		state := p.scannerPanels[panel.id]
		if state == nil {
			continue
		}
		active = append(active, activeScannerPanel{id: panel.id, filters: state.filters})
	}
	return active
}

func (p *Poller) cachedPanelRank(ctx context.Context, key string, now time.Time, cadence time.Duration, fetch func() ([]rankItem, error)) ([]rankItem, time.Time, error) {
	if p.panelRankCache == nil {
		p.panelRankCache = map[string]scannerRankCache{}
	}
	entry := p.panelRankCache[key]
	if !entry.lastAttempt.IsZero() && now.Sub(entry.lastAttempt) < cadence {
		return entry.items, entry.at, entry.lastErr
	}
	entry.lastAttempt = now
	items, err := fetch()
	if errors.Is(err, context.Canceled) {
		entry.lastAttempt = time.Time{}
		entry.lastErr = nil
		p.panelRankCache[key] = entry
		return entry.items, entry.at, err
	}
	if err == nil {
		entry.items, entry.at, entry.lastErr = items, p.clk.Now(), nil
	} else {
		entry.lastErr = err
	}
	p.panelRankCache[key] = entry
	return entry.items, entry.at, err
}

func (p *Poller) refreshPanelSnapshots(ctx context.Context, phase session.Phase, items map[string]rankItem, poolDay int64) {
	if len(items) == 0 || !p.lastSnapshotAttempt.IsZero() && p.clk.Now().Sub(p.lastSnapshotAttempt) < scannerRankCadence {
		return
	}
	syms := make([]string, 0, len(items))
	for sym := range items {
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	start := p.snapshotCursor % len(syms)
	end := start + snapshotChunkSize
	if end > len(syms) {
		end = len(syms)
	}
	batch := append([]string{}, syms[start:end]...)
	if len(batch) < snapshotChunkSize && end == len(syms) && start > 0 {
		batch = append(batch, syms[:minInt(start, snapshotChunkSize-len(batch))]...)
	}
	if len(batch) > snapshotChunkSize {
		batch = batch[:snapshotChunkSize]
	}
	p.snapshotCursor = (start + len(batch)) % len(syms)
	p.lastSnapshotAttempt = p.clk.Now()
	requests := 0
	p.snapshotBatch(ctx, phase, batch, &requests, items, poolDay)
}

func scannerRankKey(poolDay int64, source string) string {
	return fmt.Sprintf("%d/%s", poolDay, source)
}

func (p *Poller) fetchSessionVolume(ctx context.Context, phase session.Phase) ([]rankItem, error) {
	field := int32(2404)
	switch phase {
	case session.PostMarket:
		field = 2409
	case session.Overnight:
		field = 2418
	}
	desc, market, us := int32(2), int32(2), int64(2)
	codeField := int32(1101)
	query := &qotstockscreen.Request{C2S: &qotstockscreen.C2S{
		FilterList:   []*qotstockscreen.ScreenQuery{{SimpleFieldQuery: &qotstockscreen.QuerySimpleField{SimpleField: &market, ScreenValueList: []int64{us}}}},
		RetrieveList: []*qotstockscreen.RetrieveQuery{{BasicProperty: &qotstockscreen.PropertyBasic{Name: &codeField}}, {SimpleProperty: &qotstockscreen.PropertySimple{Name: &field}}},
		Sort:         &qotstockscreen.Sort{Direction: &desc, SimpleProperty: &qotstockscreen.PropertySimple{Name: &field}},
		PageFrom:     proto.Int32(0), PageCount: proto.Int32(200),
	}}
	frame, err := p.r.Request(ctx, opend.ProtoQotGetStockScreen, query)
	if err != nil {
		return nil, err
	}
	var response qotstockscreen.Response
	if err := proto.Unmarshal(frame.Body, &response); err != nil {
		return nil, err
	}
	if response.GetRetType() != 0 {
		return nil, fmt.Errorf("session volume screen retType=%d: %s", response.GetRetType(), response.GetRetMsg())
	}
	var out []rankItem
	for _, row := range response.GetS2C().GetDataList() {
		var code string
		var volume int64
		var hasVolume bool
		for _, result := range row.GetResults() {
			if basic := result.GetBasicPropertyResult(); basic != nil && basic.GetProperty().GetName() == codeField {
				code = basic.GetSval()
			}
			if simple := result.GetSimplePropertyResult(); simple != nil && simple.GetProperty().GetName() == field {
				if simple.Ival != nil {
					volume, hasVolume = simple.GetIval(), true
				} else if simple.Dval != nil && finiteNonNegative(simple.GetDval()) && simple.GetDval() <= math.MaxInt64 {
					volume, hasVolume = int64(simple.GetDval()), true
				}
			}
		}
		if code == "" || !hasVolume || volume < 0 {
			continue
		}
		out = append(out, rankItem{Symbol: "US." + code, Volume: volume, sessionVolume: &volume, sessionVolumePhase: phase, sessionVolumePoolDay: session.PoolDay(p.clk.Now()), dailyRequired: true})
	}
	return out, nil
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func mergeRankItem(old, next rankItem) rankItem {
	if next.Symbol == "" {
		return old
	}
	if old.Symbol == "" {
		return next
	}
	old.ChangePct, old.Last = next.ChangePct, next.Last
	if next.snapshotAt.After(old.snapshotAt) {
		old.snapshotAt = next.snapshotAt
	}
	if next.sessionVolume != nil {
		old.sessionVolume, old.sessionVolumePhase, old.sessionVolumePoolDay = next.sessionVolume, next.sessionVolumePhase, next.sessionVolumePoolDay
	}
	if next.Turnover != nil {
		old.Turnover, old.turnoverPhase, old.turnoverPoolDay = next.Turnover, next.turnoverPhase, next.turnoverPoolDay
	}
	if next.Volume != 0 {
		old.Volume = next.Volume
	}
	old.dailyRequired = old.dailyRequired || next.dailyRequired
	return old
}

// panelSnapshotTime reports the oldest successful quote snapshot among the
// panel's visible rows. A shared batch for another panel must not make these
// rows appear fresher. Empty results use the latest batch time because they
// have no visible quote values whose age could be overstated.
func panelSnapshotTime(rows []wsmsg.ScannerRow, board map[string]rankItem, emptyFallback time.Time) time.Time {
	if len(rows) == 0 {
		return emptyFallback
	}
	var oldest time.Time
	for _, row := range rows {
		item, ok := board[row.Symbol]
		if !ok || item.snapshotAt.IsZero() {
			return time.Time{}
		}
		if oldest.IsZero() || item.snapshotAt.Before(oldest) {
			oldest = item.snapshotAt
		}
	}
	return oldest
}

func dedupeRankItems(items []rankItem) []rankItem {
	bySymbol := make(map[string]rankItem, len(items))
	for _, item := range items {
		bySymbol[item.Symbol] = mergeRankItem(bySymbol[item.Symbol], item)
	}
	out := make([]rankItem, 0, len(bySymbol))
	for _, item := range bySymbol {
		out = append(out, item)
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func formatScannerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func sortPanelRows(rows []wsmsg.ScannerRow, mode string) {
	sort.Slice(rows, func(i, j int) bool {
		if mode == "session_volume" {
			if rows[i].SessionVolume == nil || rows[j].SessionVolume == nil {
				return rows[i].SessionVolume != nil
			}
			return *rows[i].SessionVolume > *rows[j].SessionVolume
		}
		if mode == "most_active" {
			if rows[i].Volume == nil || rows[j].Volume == nil {
				return rows[i].Volume != nil
			}
			return *rows[i].Volume > *rows[j].Volume
		}
		if rows[i].ChangePct == nil || rows[j].ChangePct == nil {
			return rows[i].Symbol < rows[j].Symbol
		}
		if mode == "losers" {
			return *rows[i].ChangePct < *rows[j].ChangePct
		}
		return *rows[i].ChangePct > *rows[j].ChangePct
	})
}

func panelNewHits(state *scannerPanelState, sess string, rows []wsmsg.ScannerRow) []string {
	seen := state.seen[sess]
	baseline := len(seen) == 0
	if seen == nil {
		seen = map[string]bool{}
		state.seen[sess] = seen
	}
	var hits []string
	for _, row := range rows {
		if !seen[row.Symbol] {
			seen[row.Symbol] = true
			if !baseline {
				hits = append(hits, row.Symbol)
			}
		}
	}
	return hits
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
