// Package pricealert owns saved notification conditions and durable trigger history.
package pricealert

import (
	"crypto/rand"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/earlisreal/eTape/engine/internal/clock"
	"github.com/earlisreal/eTape/engine/internal/session"
)

type Condition string

var symbolPattern = regexp.MustCompile(`^(US\.)?[A-Z][A-Z0-9.-]{0,14}$`)

const (
	AtOrAbove Condition = "at-or-above"
	AtOrBelow Condition = "at-or-below"
)

type State string

const (
	Monitoring      State = "monitoring"
	WaitingData     State = "waiting-data"
	WaitingCapacity State = "waiting-capacity"
	OutsideSession  State = "outside-session"
	Paused          State = "paused"
	Triggered       State = "triggered"
)

type Sessions struct {
	Pre  bool `json:"pre"`
	RTH  bool `json:"rth"`
	Post bool `json:"post"`
}

func (s Sessions) allows(at time.Time) bool {
	switch session.PhaseAt(at) {
	case session.PreMarket:
		return s.Pre
	case session.RTH:
		return s.RTH
	case session.PostMarket:
		return s.Post
	default:
		return false
	}
}

type Alert struct {
	ID          string    `json:"id"`
	Symbol      string    `json:"symbol"`
	SourceGroup string    `json:"sourceGroup,omitempty"`
	Condition   Condition `json:"condition"`
	Price       float64   `json:"price"`
	Label       string    `json:"label"`
	Sessions    Sessions  `json:"sessions"`
	State       State     `json:"state"`
	Version     uint64    `json:"version"`
	CreatedMs   int64     `json:"createdMs"`
	UpdatedMs   int64     `json:"updatedMs"`
}

type Trigger struct {
	ID          string    `json:"id"`
	AlertID     string    `json:"alertId"`
	Symbol      string    `json:"symbol"`
	SourceGroup string    `json:"sourceGroup,omitempty"`
	Condition   Condition `json:"condition"`
	Price       float64   `json:"price"`
	ActualPrice float64   `json:"actualPrice"`
	Label       string    `json:"label"`
	Sessions    Sessions  `json:"sessions"`
	Session     string    `json:"session"`
	TsMs        int64     `json:"tsMs"`
	Reason      string    `json:"reason"`
}

type Draft struct {
	ID          string
	Version     uint64
	Rearm       bool
	Symbol      string
	SourceGroup string
	Condition   Condition
	Price       float64
	Label       string
	Sessions    Sessions
}

type Print struct {
	Symbol   string
	Price    float64
	TsMs     int64
	RecvTsMs int64
	Seq      int64
	Gap      bool
}

type Snapshot struct {
	Alerts   []Alert
	History  []Trigger
	Revision uint64
	Error    string
}

type Notification struct {
	ID        string
	Triggers  []Trigger
	ExpiresMs int64
}

type notificationState struct {
	notification Notification
	popupOwner   uint64
	soundClaimed bool
}

type Change struct {
	Alert         *Alert
	Trigger       *Trigger
	DeleteAlert   string
	DeleteTrigger string
}

type Repository interface {
	LoadPriceAlerts() ([]Alert, []Trigger, error)
	CommitPriceAlerts(Change) error
}

type Availability uint8

const (
	Unavailable Availability = iota
	Available
	CapacityUnavailable
)

type Demand interface {
	Ensure(symbol string)
	Release(symbol string)
	Availability(symbol string) Availability
}

type Options struct {
	Clock          clock.Clock
	Repository     Repository
	Demand         Demand
	OnNotification func(Notification)
	OnSnapshot     func(Snapshot)
}

type Manager struct {
	mu             sync.Mutex
	clk            clock.Clock
	repo           Repository
	demand         Demand
	demanded       map[string]bool
	alerts         map[string]Alert
	history        map[string]Trigger
	marks          map[string]Print
	after          map[string]int64
	resuming       map[string]bool
	lastSeq        map[string]int64
	lastDay        map[string]int64
	gapMs          int64
	onNotification func(Notification)
	notifications  map[string]*notificationState
	burstID        string
	burstUntilMs   int64
	revision       uint64
	lastError      string
	pending        map[string]Change
	onSnapshot     func(Snapshot)
	input          chan Print
	pendingGap     atomic.Int64
}

func New(options Options) (*Manager, error) {
	if options.Repository == nil {
		return nil, errors.New("price alerts require durable storage")
	}
	if options.Clock == nil {
		options.Clock = clock.System{}
	}
	alerts, history, err := options.Repository.LoadPriceAlerts()
	if err != nil {
		return nil, err
	}
	m := &Manager{clk: options.Clock, repo: options.Repository, demand: options.Demand,
		alerts: make(map[string]Alert), history: make(map[string]Trigger), marks: make(map[string]Print), demanded: make(map[string]bool),
		after: make(map[string]int64), resuming: make(map[string]bool), lastSeq: make(map[string]int64), lastDay: make(map[string]int64),
		onNotification: options.OnNotification, notifications: make(map[string]*notificationState),
		onSnapshot: options.OnSnapshot, input: make(chan Print, 8192)}
	m.pending = make(map[string]Change)
	for _, alert := range alerts {
		if alert.State != Triggered && alert.State != Paused {
			alert.State = WaitingData
		}
		m.alerts[alert.ID] = alert
		m.after[alert.ID], m.resuming[alert.ID] = m.clk.Now().UnixMilli(), true
	}
	for _, trigger := range history {
		m.history[trigger.ID] = trigger
	}
	m.refreshLocked()
	return m, nil
}

func (m *Manager) Upsert(draft Draft) (Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[draft.ID]; ok {
		return Alert{}, errors.New("a trigger is waiting for storage recovery")
	}
	draft.Symbol = strings.ToUpper(strings.TrimSpace(draft.Symbol))
	if !symbolPattern.MatchString(draft.Symbol) || len(draft.Label) > 200 || draft.Price <= 0 || math.IsNaN(draft.Price) || math.IsInf(draft.Price, 0) {
		return Alert{}, errors.New("a symbol and positive finite price are required")
	}
	if draft.Condition != AtOrAbove && draft.Condition != AtOrBelow {
		return Alert{}, errors.New("invalid price alert condition")
	}
	switch draft.SourceGroup {
	case "", "red", "green", "blue", "yellow":
	default:
		return Alert{}, errors.New("invalid price alert source group")
	}
	if !draft.Sessions.Pre && !draft.Sessions.RTH && !draft.Sessions.Post {
		return Alert{}, errors.New("select at least one session")
	}
	now := m.clk.Now().UnixMilli()
	alert := Alert{ID: "pa-" + rand.Text(), Symbol: draft.Symbol, Condition: draft.Condition, Price: draft.Price,
		SourceGroup: draft.SourceGroup, Label: strings.TrimSpace(draft.Label), Sessions: draft.Sessions, State: WaitingData, Version: 1, CreatedMs: now, UpdatedMs: now}
	if draft.ID != "" {
		previous, ok := m.alerts[draft.ID]
		if !ok {
			return Alert{}, errors.New("price alert no longer exists")
		}
		if previous.Version != draft.Version {
			return Alert{}, errors.New("price alert changed; refresh before saving")
		}
		if previous.Symbol != draft.Symbol {
			return Alert{}, errors.New("an existing alert's symbol cannot change")
		}
		alert.ID, alert.CreatedMs, alert.Version = previous.ID, previous.CreatedMs, previous.Version+1
		alert.SourceGroup = previous.SourceGroup
		if previous.State == Triggered && !draft.Rearm {
			alert.State = Triggered
		}
		if previous.State == Paused {
			alert.State = Paused
		}
		if draft.Rearm && previous.State != Triggered {
			return Alert{}, errors.New("only a triggered price alert can be rearmed")
		}
	}
	if err := m.repo.CommitPriceAlerts(Change{Alert: &alert}); err != nil {
		return Alert{}, err
	}
	m.alerts[alert.ID] = alert
	m.revision++
	m.after[alert.ID] = now
	m.resuming[alert.ID] = draft.Rearm
	m.refreshLocked()
	return alert, nil
}

func (m *Manager) Delete(id string, version uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[id]; ok {
		return errors.New("a trigger is waiting for storage recovery")
	}
	alert, ok := m.alerts[id]
	if !ok {
		return nil
	}
	if alert.Version != version {
		return errors.New("price alert changed; refresh before deleting")
	}
	if err := m.repo.CommitPriceAlerts(Change{DeleteAlert: id}); err != nil {
		return err
	}
	delete(m.alerts, id)
	delete(m.after, id)
	delete(m.resuming, id)
	m.revision++
	m.reconcileDemandLocked()
	return nil
}

func (m *Manager) DeleteTrigger(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.repo.CommitPriceAlerts(Change{DeleteTrigger: id}); err != nil {
		return err
	}
	delete(m.history, id)
	m.revision++
	return nil
}

func (m *Manager) SetPaused(id string, version uint64, paused bool) (Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pending[id]; ok {
		return Alert{}, errors.New("a trigger is waiting for storage recovery")
	}
	alert, ok := m.alerts[id]
	if !ok {
		return Alert{}, errors.New("price alert no longer exists")
	}
	if alert.Version != version {
		return Alert{}, errors.New("price alert changed; refresh before saving")
	}
	if alert.State == Triggered {
		return Alert{}, errors.New("use Rearm for a triggered alert")
	}
	alert.State = WaitingData
	if paused {
		alert.State = Paused
	}
	alert.UpdatedMs, alert.Version = m.clk.Now().UnixMilli(), alert.Version+1
	if err := m.repo.CommitPriceAlerts(Change{Alert: &alert}); err != nil {
		return Alert{}, err
	}
	m.alerts[id] = alert
	m.revision++
	m.after[id], m.resuming[id] = alert.UpdatedMs, !paused
	m.reconcileDemandLocked()
	return alert, nil
}

func (m *Manager) SetDemand(demand Demand) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.demand != nil {
		for symbol := range m.demanded {
			m.demand.Release(symbol)
		}
	}
	clear(m.demanded)
	clear(m.marks)
	for id := range m.alerts {
		m.after[id], m.resuming[id] = m.clk.Now().UnixMilli(), true
	}
	m.demand = demand
	m.revision++
	m.refreshLocked()
}

func (m *Manager) reconcileDemandLocked() {
	if m.demand == nil {
		return
	}
	wanted := make(map[string]bool)
	for _, alert := range m.alerts {
		if alert.State != Paused && alert.State != Triggered && alert.Sessions.allows(m.clk.Now()) {
			wanted[alert.Symbol] = true
		}
	}
	for symbol := range m.demanded {
		if !wanted[symbol] {
			m.demand.Release(symbol)
			delete(m.demanded, symbol)
		}
	}
	for symbol := range wanted {
		if !m.demanded[symbol] {
			m.demand.Ensure(symbol)
			m.demanded[symbol] = true
		}
	}
}

func (m *Manager) Refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshLocked()
}

func (m *Manager) refreshLocked() {
	// Preserve the original touch through a transient disk failure. Recovery
	// updates history silently rather than replaying a missed notification.
	for id, change := range m.pending {
		if err := m.repo.CommitPriceAlerts(change); err != nil {
			continue
		}
		m.alerts[id] = *change.Alert
		m.history[change.Trigger.ID] = *change.Trigger
		delete(m.pending, id)
		m.revision++
	}
	if len(m.pending) == 0 && m.lastError != "" {
		m.lastError = ""
		m.revision++
	}
	m.reconcileDemandLocked()
	now := m.clk.Now()
	for id, alert := range m.alerts {
		if alert.State == Paused || alert.State == Triggered {
			continue
		}
		previous := alert.State
		switch {
		case !alert.Sessions.allows(now):
			alert.State = OutsideSession
		case m.demand == nil || m.demand.Availability(alert.Symbol) == Unavailable:
			alert.State = WaitingData
		case m.demand.Availability(alert.Symbol) == CapacityUnavailable:
			alert.State = WaitingCapacity
		default:
			if previous == OutsideSession {
				m.after[id], m.resuming[id] = now.UnixMilli(), true
			}
			alert.State = WaitingData
			if mark, ok := m.marks[alert.Symbol]; ok && mark.RecvTsMs > m.after[id] {
				alert.State = Monitoring
			}
		}
		if previous == Monitoring && alert.State != Monitoring {
			m.after[id], m.resuming[id] = now.UnixMilli(), true
		}
		m.alerts[id] = alert
		if alert.State != previous {
			m.revision++
		}
	}
}

func (m *Manager) Ingest(print Print) error {
	m.mu.Lock()
	var fired []Trigger
	var storageErr error
	defer func() {
		var notification *Notification
		if len(fired) > 0 {
			value := m.notificationLocked(fired)
			notification = &value
		}
		if len(fired) > 0 {
			m.reconcileDemandLocked()
		}
		m.mu.Unlock()
		if notification != nil && m.onNotification != nil {
			m.onNotification(*notification)
		}
	}()
	now := m.clk.Now().UnixMilli()
	if print.Gap {
		at := print.RecvTsMs
		if at <= 0 {
			at = now
		}
		if at > m.gapMs {
			m.gapMs = at
		}
		clear(m.marks)
		clear(m.lastSeq)
		clear(m.lastDay)
		for id, alert := range m.alerts {
			if alert.State == Paused || alert.State == Triggered {
				continue
			}
			alert.State = WaitingData
			m.alerts[id] = alert
			m.revision++
			m.after[id], m.resuming[id] = max(m.after[id], m.gapMs), true
		}
		return nil
	}
	if print.Symbol == "" || print.Price <= 0 || math.IsNaN(print.Price) || math.IsInf(print.Price, 0) ||
		print.Seq <= 0 || print.TsMs <= 0 || print.RecvTsMs <= m.gapMs || print.RecvTsMs > now || now-print.RecvTsMs > 2_000 {
		return nil
	}
	day := session.DayMs(print.TsMs)
	if m.lastDay[print.Symbol] != day {
		m.lastSeq[print.Symbol], m.lastDay[print.Symbol] = 0, day
	}
	if print.Seq <= m.lastSeq[print.Symbol] {
		return nil
	}
	m.lastSeq[print.Symbol] = print.Seq
	if m.demand != nil && m.demand.Availability(print.Symbol) == Available {
		m.marks[print.Symbol] = print
	} else {
		delete(m.marks, print.Symbol)
	}
	for id, alert := range m.alerts {
		if _, pending := m.pending[id]; pending {
			continue
		}
		if alert.Symbol != print.Symbol || alert.State == Triggered || alert.State == Paused {
			continue
		}
		if !alert.Sessions.allows(m.clk.Now()) || !alert.Sessions.allows(time.UnixMilli(print.TsMs)) {
			if alert.State != OutsideSession {
				alert.State = OutsideSession
				m.alerts[id] = alert
				m.revision++
			}
			m.resuming[id] = true
			continue
		}
		if m.demand == nil || m.demand.Availability(alert.Symbol) != Available {
			continue
		}
		if print.RecvTsMs <= m.after[id] {
			continue
		}
		if alert.State == OutsideSession {
			m.resuming[id] = true
		}
		if alert.State != Monitoring {
			m.revision++
		}
		alert.State = Monitoring
		m.alerts[id] = alert
		met := alert.Condition == AtOrAbove && print.Price >= alert.Price || alert.Condition == AtOrBelow && print.Price <= alert.Price
		if !met {
			m.resuming[id] = false
			continue
		}
		alert.State, alert.UpdatedMs = Triggered, m.clk.Now().UnixMilli()
		alert.Version++
		trigger := Trigger{ID: "pt-" + rand.Text(), AlertID: id, Symbol: alert.Symbol, Condition: alert.Condition,
			SourceGroup: alert.SourceGroup, Price: alert.Price, ActualPrice: print.Price, Label: alert.Label, Sessions: alert.Sessions, Session: session.PhaseAt(time.UnixMilli(print.TsMs)).String(), TsMs: print.TsMs, Reason: "Target reached"}
		if m.resuming[id] {
			trigger.Reason = "Target met on resume"
		}
		if err := m.repo.CommitPriceAlerts(Change{Alert: &alert, Trigger: &trigger}); err != nil {
			m.pending[id] = Change{Alert: &alert, Trigger: &trigger}
			m.lastError = "Price alert storage failed; trigger retained for retry: " + err.Error()
			m.revision++
			storageErr = err
			continue
		}
		m.alerts[id], m.history[trigger.ID] = alert, trigger
		m.revision++
		fired = append(fired, trigger)
	}
	return storageErr
}

func (m *Manager) notificationLocked(triggers []Trigger) Notification {
	now := m.clk.Now().UnixMilli()
	for id, state := range m.notifications {
		if now >= state.notification.ExpiresMs {
			delete(m.notifications, id)
		}
	}
	state := m.notifications[m.burstID]
	if state == nil || now >= m.burstUntilMs {
		m.burstID, m.burstUntilMs = "pn-"+rand.Text(), now+650
		state = &notificationState{notification: Notification{ID: m.burstID, ExpiresMs: now + 5_000}}
		m.notifications[m.burstID] = state
	}
	state.notification.Triggers = append(state.notification.Triggers, triggers...)
	notification := state.notification
	notification.Triggers = append([]Trigger(nil), notification.Triggers...)
	return notification
}

// ClaimNotification grants one popup owner and at most one sound play per live burst.
func (m *Manager) ClaimNotification(id, kind string, connectionID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.notifications[id]
	if state == nil || connectionID == 0 || m.clk.Now().UnixMilli() >= state.notification.ExpiresMs {
		return false
	}
	switch kind {
	case "popup":
		if state.popupOwner == 0 {
			state.popupOwner = connectionID
		}
		return state.popupOwner == connectionID
	case "sound":
		if state.soundClaimed {
			return false
		}
		state.soundClaimed = true
		return true
	default:
		return false
	}
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshLocked()
	return m.snapshotLocked()
}

// Preview exposes only a recent eligible live print; an absent price is zero.
func (m *Manager) Preview(symbol string) Print {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.marks[symbol]
	if m.demand == nil || m.demand.Availability(symbol) != Available || p.RecvTsMs <= m.gapMs || m.clk.Now().UnixMilli()-p.RecvTsMs > 2_000 {
		return Print{}
	}
	return p
}

func (m *Manager) snapshotLocked() Snapshot {
	out := Snapshot{Alerts: make([]Alert, 0, len(m.alerts)), History: make([]Trigger, 0, len(m.history)), Revision: m.revision, Error: m.lastError}
	for _, alert := range m.alerts {
		out.Alerts = append(out.Alerts, alert)
	}
	for _, trigger := range m.history {
		out.History = append(out.History, trigger)
	}
	sort.Slice(out.Alerts, func(i, j int) bool { return out.Alerts[i].ID < out.Alerts[j].ID })
	sort.Slice(out.History, func(i, j int) bool {
		if out.History[i].TsMs == out.History[j].TsMs {
			return out.History[i].ID > out.History[j].ID
		}
		return out.History[i].TsMs > out.History[j].TsMs
	})
	return out
}
