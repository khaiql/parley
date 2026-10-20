package herdr

import (
	"time"
)

const notificationGrace = 30 * time.Second
const notificationInterval = 5 * time.Second

type notificationTiming struct {
	observation NotificationObservation
	readySince  time.Time
	unreadSince time.Time
	retryID     string
}
type notificationSchedule struct {
	bindings map[string]*notificationTiming
}

func newNotificationSchedule() *notificationSchedule {
	return &notificationSchedule{bindings: map[string]*notificationTiming{}}
}
func notificationReady(status string) bool { return status == "idle" || status == "done" }

func (s *notificationSchedule) Observe(now time.Time, o NotificationObservation) {
	p := s.bindings[o.Key]
	if p == nil {
		p = &notificationTiming{}
		s.bindings[o.Key] = p
	}
	old := p.observation
	if o.SeenSeq < old.SeenSeq {
		delete(s.bindings, o.Key)
		return
	}
	if !notificationReady(o.Agent.Status) {
		p.readySince = time.Time{}
	} else if p.readySince.IsZero() || o.Agent.TerminalID != old.Agent.TerminalID || o.Agent.PaneID != old.Agent.PaneID || (o.Agent.StateChangeSeq != old.Agent.StateChangeSeq && o.Agent.Status == old.Agent.Status) {
		p.readySince = now
	}
	if o.ThroughSeq <= o.SeenSeq {
		p.unreadSince = time.Time{}
	} else if p.unreadSince.IsZero() || o.SeenSeq > old.SeenSeq {
		p.unreadSince = now
	}
	p.observation = o
}

func (s *notificationSchedule) Due(now time.Time, attempts []NotificationAttempt) []NotificationObservation {
	var result []NotificationObservation
	for key, p := range s.bindings {
		prior, err := notificationOutstanding(key, p.observation.SeenSeq, attempts)
		if err != nil {
			continue
		}
		if prior != nil {
			if !prior.RetryRequested {
				continue
			}
			if p.retryID != prior.ID {
				p.retryID = prior.ID
				if notificationReady(p.observation.Agent.Status) {
					p.readySince = now
				}
				p.unreadSince = now
			}
		}
		if !notificationReady(p.observation.Agent.Status) || p.readySince.IsZero() || p.unreadSince.IsZero() {
			continue
		}
		start := p.readySince
		if p.unreadSince.After(start) {
			start = p.unreadSince
		}
		if now.Sub(start) >= notificationGrace {
			result = append(result, p.observation)
		}
	}
	return result
}
