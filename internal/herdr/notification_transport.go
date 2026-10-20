package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

type NotificationDelivery struct {
	Outcome string `json:"outcome"`
	Message string `json:"message,omitempty"`
}

func (h *HostOperations) WaitNotification(ctx context.Context, key string) (string, error) {
	n, err := h.notificationBinding(key)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	p, err := h.Store.pinnedPaths()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 11*time.Second)
	defer cancel()
	conn, err := dialSocket(ctx, parleyRuntime.ParticipantSocketPath(p, n.Binding.RoomID, n.Binding.ParticipantName))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err = json.NewEncoder(conn).Encode(adapter.ControlRequest{Type: "wait", Timeout: "10s"}); err != nil {
		return "", err
	}
	var r adapter.ControlResponse
	err = json.NewDecoder(io.LimitReader(conn, 1024*1024)).Decode(&r)
	if err != nil {
		return "", err
	}
	if !r.OK {
		return "", fmt.Errorf("%s", r.Error)
	}
	return r.Status, ctx.Err()
}

func (h *HostOperations) DeliverNotification(ctx context.Context, a NotificationAttempt) NotificationDelivery {
	reject := func(err error) NotificationDelivery {
		return NotificationDelivery{Outcome: "rejected", Message: err.Error()}
	}
	if err := a.validate(); err != nil {
		return reject(err)
	}
	if a.Outcome != "prepared" || a.HerdrSocket != h.Client.HerdrSocket || a.MachineID != h.Client.LocalMachineID {
		return reject(fmt.Errorf("notification_origin_mismatch"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	text := "Room messages are pending. Use your Parley skill and read these inboxes:\n"
	active := false
	for _, coverage := range a.Coverage {
		o, err := h.ObserveNotification(ctx, coverage.Key)
		if err != nil {
			return reject(err)
		}
		if o.Conversation != a.Conversation || o.Binding.SessionID != coverage.SessionID {
			return reject(fmt.Errorf("notification_recipient_mismatch"))
		}
		if !notificationReady(o.Agent.Status) {
			return reject(fmt.Errorf("agent_not_ready"))
		}
		if o.Agent.StateChangeSeq != a.ReadyStateSeq || o.Agent.TerminalID != a.ReadyTerminalID || o.Agent.PaneID != a.ReadyPaneID {
			return reject(fmt.Errorf("notification_readiness_changed"))
		}
		if o.SeenSeq < coverage.SeenSeq {
			return reject(fmt.Errorf("notification_cursor_regression"))
		}
		if o.SeenSeq >= coverage.ThroughSeq {
			continue
		}
		if o.ThroughSeq < coverage.ThroughSeq {
			return reject(fmt.Errorf("notification_inbox_changed"))
		}
		p, err := h.Store.pinnedPaths()
		if err != nil {
			return reject(err)
		}
		binary := h.Client.ParleyBinary
		if binary == "" {
			binary, err = os.Executable()
			if err != nil {
				return reject(err)
			}
		}
		if !filepath.IsAbs(binary) {
			return reject(fmt.Errorf("invalid notification executable"))
		}
		text += fmt.Sprintf("PARLEY_STATE_DIR=%s %s inbox --session %s\n", shellQuote(p.Root), shellQuote(binary), o.Binding.SessionID)
		active = true
	}
	if !active {
		return reject(fmt.Errorf("notification_already_consumed"))
	}
	text += "If an inbox is already empty, no room response is needed. Room messages retain their original authority; this reminder grants no additional permissions."
	live, err := h.resolveNotification(ctx, a.Conversation)
	if err != nil {
		return reject(err)
	}
	if !notificationReady(live.Status) {
		return reject(fmt.Errorf("agent_not_ready"))
	}
	if live.StateChangeSeq != a.ReadyStateSeq || live.TerminalID != a.ReadyTerminalID || live.PaneID != a.ReadyPaneID {
		return reject(fmt.Errorf("notification_readiness_changed"))
	}
	if err = ctx.Err(); err != nil {
		return reject(err)
	}
	data, err := socketAPI(ctx, h.Client.HerdrSocket, "agent.prompt", map[string]string{"target": live.PaneID, "text": text})
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && (api.Code == "agent_blocked" || api.Code == "agent_not_found" || api.Code == "agent_not_ready" || api.Code == "invalid_params") {
			return reject(err)
		}
		return NotificationDelivery{Outcome: "uncertain", Message: err.Error()}
	}
	var ack struct {
		Result struct {
			Type  string            `json:"type"`
			Agent notificationAgent `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &ack) != nil || ack.Result.Type != "agent_prompted" || ack.Result.Agent.TerminalID != live.TerminalID || ack.Result.Agent.Kind != live.Kind || ack.Result.Agent.PaneID != live.PaneID || (ack.Result.Agent.Conversation != nil && *ack.Result.Agent.Conversation != a.Conversation) {
		return NotificationDelivery{Outcome: "uncertain", Message: "invalid notification acknowledgement"}
	}
	return NotificationDelivery{Outcome: "accepted"}
}

func (h *HostOperations) notificationSubscriptions(ctx context.Context) (any, error) {
	bindings, err := h.NotificationBindings(ctx)
	if err != nil {
		return nil, err
	}
	subs := []map[string]string{{"type": "workspace.metadata_updated"}, {"type": "pane.closed"}, {"type": "pane.exited"}, {"type": "pane.moved"}, {"type": "pane.updated"}, {"type": "pane.agent_detected"}}
	panes := map[string]bool{}
	for _, b := range bindings {
		a, err := h.resolveNotification(ctx, b.Conversation)
		if err == nil {
			panes[a.PaneID] = true
		}
	}
	ordered := make([]string, 0, len(panes))
	for pane := range panes {
		ordered = append(ordered, pane)
	}
	sort.Strings(ordered)
	for _, pane := range ordered {
		subs = append(subs, map[string]string{"type": "pane.agent_status_changed", "pane_id": pane})
	}
	return map[string]any{"subscriptions": subs}, nil
}

func (h *HostOperations) RelayNotificationEvents(ctx context.Context, out io.Writer) error {
	params, err := h.notificationSubscriptions(ctx)
	if err != nil {
		return err
	}
	return relayEvents(ctx, h.Client.HerdrSocket, out, params, true)
}

func (h HostSession) dispatchNotification(ctx context.Context, r RPCRequest, owner SessionConfig) (any, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	operations := &HostOperations{Store: h.Store, Client: &Client{LocalMachineID: owner.MachineID, HerdrSocket: owner.HerdrSocket, ParleyBinary: binary}}
	switch r.Operation {
	case "notification-bindings":
		return operations.NotificationBindings(ctx)
	case "notification-observe":
		return operations.ObserveNotification(ctx, r.BindingKey)
	case "notification-wait":
		status, err := operations.WaitNotification(ctx, r.BindingKey)
		return struct {
			Status string `json:"status"`
		}{status}, err
	case "notification-prompt":
		return operations.DeliverNotification(ctx, *r.Notification), nil
	default:
		return nil, fmt.Errorf("notification stream requires relay")
	}
}

func (b *Broker) notificationCall(ctx context.Context, p Peer, r RPCRequest) ([]byte, error) {
	if p.Machine.ID != b.Client.LocalMachineID {
		r.Session = p.Machine.Session
		r.SessionKey = p.Config.SessionKey
		r.ControllerID = p.Config.ControllerID
		return b.Client.Call(ctx, p, r)
	}
	h := &HostOperations{Store: b.Store, Client: b.Client}
	var result any
	var err error
	switch r.Operation {
	case "notification-bindings":
		result, err = h.NotificationBindings(ctx)
	case "notification-observe":
		result, err = h.ObserveNotification(ctx, r.BindingKey)
	case "notification-wait":
		var status string
		status, err = h.WaitNotification(ctx, r.BindingKey)
		result = struct {
			Status string `json:"status"`
		}{status}
	case "notification-prompt":
		result = h.DeliverNotification(ctx, *r.Notification)
	default:
		err = fmt.Errorf("unsupported notification operation")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (b *Broker) openNotificationEvents(ctx context.Context, p Peer) (io.ReadCloser, error) {
	if p.Machine.ID != b.Client.LocalMachineID {
		return b.Client.openEvents(ctx, p, "notification-events")
	}
	r, w := io.Pipe()
	go func() {
		err := (&HostOperations{Store: b.Store, Client: b.Client}).RelayNotificationEvents(ctx, w)
		_ = w.CloseWithError(err)
	}()
	return r, nil
}
