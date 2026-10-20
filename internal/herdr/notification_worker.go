package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type NotificationDiagnostic struct {
	MachineID       string `json:"machine_id"`
	Key             string `json:"key"`
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	SeenSeq         int64  `json:"seen_seq"`
	ThroughSeq      int64  `json:"through_seq"`
	LastNotifiedSeq int64  `json:"last_notified_seq"`
	AttemptID       string `json:"attempt_id,omitempty"`
	Message         string `json:"message,omitempty"`
}

type notificationHostStatus struct {
	Bindings []NotificationDiagnostic `json:"bindings"`
}

func (s *Store) saveNotificationStatus(p Peer, status []NotificationDiagnostic) error {
	dir, err := s.notificationDir("status")
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, hashBytes([]byte(p.Machine.ID+"\x00"+p.Config.HerdrSocket))+".json"), notificationHostStatus{Bindings: status})
}

func (s *Store) NotificationStatuses() ([]NotificationDiagnostic, error) {
	dir, err := s.notificationDir("status")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := []NotificationDiagnostic{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var host notificationHostStatus
		if err := readJSON(filepath.Join(dir, entry.Name()), &host); err != nil {
			return nil, err
		}
		result = append(result, host.Bindings...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MachineID+result[i].Key < result[j].MachineID+result[j].Key })
	return result, nil
}

func (s *Store) checkNotificationCursor(ctx context.Context, machine, key string, seen int64) error {
	dir, err := s.notificationDir("cursors")
	if err != nil {
		return err
	}
	name := hashBytes([]byte(machine + "\x00" + key))
	return lockFile(ctx, filepath.Join(dir, name+".lock"), func() error {
		path := filepath.Join(dir, name+".json")
		var previous struct {
			SeenSeq int64 `json:"seen_seq"`
		}
		err := readJSON(path, &previous)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if seen < 0 || seen < previous.SeenSeq {
			return fmt.Errorf("notification_cursor_regression")
		}
		if err == nil && seen == previous.SeenSeq {
			return nil
		}
		previous.SeenSeq = seen
		return writeJSON(path, previous)
	})
}

func (b *Broker) RunNotifications(ctx context.Context, p Peer) {
	backoff := time.Second
	for ctx.Err() == nil {
		generation, cancel := context.WithCancel(ctx)
		stream, err := b.openNotificationEvents(generation, p)
		if err == nil {
			err = b.runNotificationGeneration(generation, p, stream, time.Now)
			_ = stream.Close()
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "room message fallback:", p.Machine.ID, err)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

type notificationWaitResult struct {
	key, status string
	err         error
}

type notificationGeneration struct {
	broker      *Broker
	peer        Peer
	ctx         context.Context
	now         func() time.Time
	schedule    *notificationSchedule
	bindings    map[string]NotificationBinding
	pending     map[string]bool
	waits       map[string]context.CancelFunc
	waited      chan notificationWaitResult
	workers     sync.WaitGroup
	diagnostics []NotificationDiagnostic
}

func (b *Broker) runNotificationGeneration(parent context.Context, p Peer, stream io.ReadCloser, now func() time.Time) error {
	ctx, cancel := context.WithCancel(parent)
	g := &notificationGeneration{broker: b, peer: p, ctx: ctx, now: now, schedule: newNotificationSchedule(), bindings: map[string]NotificationBinding{}, pending: map[string]bool{}, waits: map[string]context.CancelFunc{}, waited: make(chan notificationWaitResult, 64)}
	defer g.workers.Wait()
	defer func() { cancel(); _ = stream.Close() }()
	defer func() {
		for i := range g.diagnostics {
			g.diagnostics[i].Status = "unavailable"
			g.diagnostics[i].Message = "observation connection closed; identity and readiness must be verified again"
		}
		_ = b.Store.saveNotificationStatus(p, g.diagnostics)
	}()
	wake := make(chan struct{}, 1)
	finished := make(chan error, 1)
	g.workers.Go(func() {
		signal := func() error {
			select {
			case wake <- struct{}{}:
			default:
			}
			return nil
		}
		err := ReadEvents(ctx, stream, signal, signal)
		cancel()
		finished <- err
	})
	started := false
	tick := time.NewTicker(notificationInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-finished:
				return err
			default:
				return ctx.Err()
			}
		case <-wake:
			started = true
			if err := g.refresh(true); err != nil {
				return err
			}
		case result := <-g.waited:
			if stop := g.waits[result.key]; stop != nil {
				stop()
				delete(g.waits, result.key)
			}
			if _, active := g.bindings[result.key]; !active {
				continue
			}
			if result.err == nil && result.status == "timeout" {
				g.startWait(result.key)
				continue
			}
			g.pending[result.key] = true
			if err := g.refresh(false); err != nil {
				return err
			}
		case <-tick.C:
			if started {
				if err := g.refresh(false); err != nil {
					return err
				}
			}
		}
	}
}

func (g *notificationGeneration) startWait(key string) {
	if _, active := g.waits[key]; active || g.ctx.Err() != nil {
		return
	}
	waiting, stop := context.WithCancel(g.ctx)
	g.waits[key] = stop
	g.workers.Go(func() {
		data, err := g.broker.notificationCall(waiting, g.peer, RPCRequest{Operation: "notification-wait", BindingKey: key})
		var response struct {
			Status string `json:"status"`
		}
		if err == nil {
			err = json.Unmarshal(data, &response)
		}
		select {
		case g.waited <- notificationWaitResult{key, response.Status, err}:
		case <-g.ctx.Done():
		}
	})
}

func (g *notificationGeneration) refresh(all bool) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if all {
		data, err := g.broker.notificationCall(g.ctx, g.peer, RPCRequest{Operation: "notification-bindings"})
		if err != nil {
			return err
		}
		var list []NotificationBinding
		if err = json.Unmarshal(data, &list); err != nil {
			return err
		}
		current := map[string]NotificationBinding{}
		for _, n := range list {
			if !validDigest(n.Key) || n.Conversation.validate() != nil || n.Key != HostBindingKey(n.Binding.RoomID, n.Binding.Target) || n.Binding.Target.HerdrSocket != g.peer.Config.HerdrSocket && g.peer.Machine.ID != g.broker.Client.LocalMachineID {
				return fmt.Errorf("invalid notification binding response")
			}
			current[n.Key] = n
		}
		for key := range g.bindings {
			if _, exists := current[key]; !exists {
				if stop := g.waits[key]; stop != nil {
					stop()
					delete(g.waits, key)
				}
				delete(g.schedule.bindings, key)
				delete(g.pending, key)
			}
		}
		g.bindings = current
	}
	attempts, err := g.broker.Store.NotificationAttempts()
	if err != nil {
		return err
	}
	attempts = notificationHostAttempts(attempts, g.peer.Machine.ID, g.peer.Config.HerdrSocket)
	g.diagnostics = nil
	keys := make([]string, 0, len(g.bindings))
	for key := range g.bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		n := g.bindings[key]
		d := NotificationDiagnostic{MachineID: g.peer.Machine.ID, Key: key, SessionID: n.Binding.SessionID, Status: "listening"}
		if all || g.pending[key] {
			data, observeErr := g.broker.notificationCall(g.ctx, g.peer, RPCRequest{Operation: "notification-observe", BindingKey: key})
			var o NotificationObservation
			if observeErr == nil {
				observeErr = json.Unmarshal(data, &o)
			}
			if observeErr == nil && (o.Key != key || o.Binding.SessionID != n.Binding.SessionID || o.Conversation != n.Conversation || o.Agent.Conversation == nil || *o.Agent.Conversation != n.Conversation) {
				observeErr = fmt.Errorf("notification_identity_mismatch")
			}
			if observeErr == nil {
				observeErr = g.broker.Store.checkNotificationCursor(g.ctx, g.peer.Machine.ID, key, o.SeenSeq)
			}
			if observeErr != nil {
				delete(g.schedule.bindings, key)
				g.pending[key] = true
				d.Status = "suspended"
				d.Message = observeErr.Error()
			} else {
				g.schedule.Observe(g.now(), o)
				g.pending[key] = o.ThroughSeq > o.SeenSeq
				if g.pending[key] {
					d.Status = "waiting"
				} else {
					g.startWait(key)
				}
			}
		}
		if observation := g.schedule.bindings[key]; observation != nil {
			d.SeenSeq = observation.observation.SeenSeq
			d.ThroughSeq = observation.observation.ThroughSeq
			prior, err := notificationOutstanding(key, n.Binding.SessionID, d.SeenSeq, attempts)
			if err != nil {
				return err
			}
			if prior != nil {
				d.AttemptID = prior.ID
				d.Status = prior.Outcome
				g.pending[key] = true
			}
		}
		for _, a := range attempts {
			if a.Outcome == "accepted" && a.MachineID == g.peer.Machine.ID && a.HerdrSocket == g.peer.Config.HerdrSocket {
				for _, c := range a.Coverage {
					if c.Key == key && c.ThroughSeq > d.LastNotifiedSeq {
						d.LastNotifiedSeq = c.ThroughSeq
					}
				}
			}
		}
		g.diagnostics = append(g.diagnostics, d)
	}
	if err = g.broker.Store.saveNotificationStatus(g.peer, g.diagnostics); err != nil {
		return err
	}
	return g.submitDue(attempts)
}

func (g *notificationGeneration) submitDue(attempts []NotificationAttempt) error {
	due := g.schedule.Due(g.now(), attempts)
	sort.Slice(due, func(i, j int) bool { return due[i].Key < due[j].Key })
	for len(due) > 0 {
		first := due[0]
		due = due[1:]
		prior, err := notificationOutstanding(first.Key, first.Binding.SessionID, first.SeenSeq, attempts)
		if err != nil {
			return err
		}
		replaces := ""
		if prior != nil && prior.RetryRequested {
			replaces = prior.ID
		}
		group := []NotificationObservation{first}
		rest := []NotificationObservation{}
		for _, o := range due {
			oPrior, err := notificationOutstanding(o.Key, o.Binding.SessionID, o.SeenSeq, attempts)
			if err != nil {
				return err
			}
			oReplaces := ""
			if oPrior != nil && oPrior.RetryRequested {
				oReplaces = oPrior.ID
			}
			if o.Conversation == first.Conversation && oReplaces == replaces && len(group) < 16 {
				group = append(group, o)
			} else {
				rest = append(rest, o)
			}
		}
		due = rest
		if err := g.ctx.Err(); err != nil {
			return err
		}
		a, err := g.broker.Store.PrepareNotification(g.ctx, g.peer.Machine.ID, g.peer.Config.HerdrSocket, group, replaces)
		if err != nil {
			return err
		}
		if err = g.ctx.Err(); err != nil {
			_ = g.broker.Store.CompleteNotification(a.ID, "rejected", "connection invalidated before submission")
			return err
		}
		data, sendErr := g.broker.notificationCall(g.ctx, g.peer, RPCRequest{Operation: "notification-prompt", Notification: &a})
		delivery := NotificationDelivery{Outcome: "uncertain"}
		if sendErr == nil {
			sendErr = json.Unmarshal(data, &delivery)
		}
		if sendErr != nil {
			delivery = NotificationDelivery{Outcome: "uncertain", Message: sendErr.Error()}
		}
		if delivery.Outcome != "accepted" && delivery.Outcome != "rejected" && delivery.Outcome != "uncertain" {
			delivery = NotificationDelivery{Outcome: "uncertain", Message: "invalid helper notification acknowledgement"}
		}
		if err = g.broker.Store.CompleteNotification(a.ID, delivery.Outcome, delivery.Message); err != nil {
			return err
		}
		for _, o := range group {
			if delivery.Outcome == "rejected" {
				delete(g.schedule.bindings, o.Key)
			}
			g.pending[o.Key] = true
		}
		attempts, err = g.broker.Store.NotificationAttempts()
		if err != nil {
			return err
		}
		attempts = notificationHostAttempts(attempts, g.peer.Machine.ID, g.peer.Config.HerdrSocket)
	}
	return nil
}
