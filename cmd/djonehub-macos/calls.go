package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type callRecord struct {
	ID        string     `json:"id"`
	Index     int        `json:"index"`
	Direction string     `json:"direction"`
	State     string     `json:"state"`
	Number    string     `json:"number,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Missed    bool       `json:"missed"`
}

type parsedCall struct {
	Index     int
	Direction string
	State     string
	Number    string
}

var clccPattern = regexp.MustCompile(`\+CLCC:\s*(\d+),(\d+),(\d+),(\d+),(\d+)(?:,"([^"]*)",(\d+))?`)

func parseCLCC(response string) []parsedCall {
	matches := clccPattern.FindAllStringSubmatch(response, -1)
	out := make([]parsedCall, 0, len(matches))
	for _, match := range matches {
		// CLCC mode 0 is voice. Mode 1 is a data session and must not surface
		// as a phone call in the macOS UI.
		if match[4] != "0" {
			continue
		}
		index, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		out = append(out, parsedCall{
			Index:     index,
			Direction: mapCallDirection(match[2]),
			State:     mapCallState(match[3]),
			Number:    strings.TrimSpace(match[6]),
		})
	}
	return out
}

func mapCallDirection(raw string) string {
	if raw == "1" {
		return "incoming"
	}
	return "outgoing"
}

func mapCallState(raw string) string {
	switch raw {
	case "0":
		return "active"
	case "1":
		return "held"
	case "2":
		return "dialing"
	case "3":
		return "alerting"
	case "4":
		return "incoming"
	case "5":
		return "waiting"
	default:
		return "unknown"
	}
}

func (a *app) startCallPoller(ctx context.Context) {
	interval := a.callPollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := a.pollCallOnce(); err != nil {
				log.Printf("call poll failed: %v", err)
			}
			timer.Reset(interval)
		}
	}
}

func (a *app) pollCallOnce() error {
	if a.demo {
		return nil
	}
	if a.modem == nil && a.currentUSBDevice() == nil {
		a.setCallPollStatus(errDJIUSBNotConnected)
		return nil
	}

	a.callMu.Lock()
	configured := a.callConfigured
	a.callMu.Unlock()
	if !configured {
		if _, err := a.runATCommand("AT+CLIP=1", 3*time.Second); err != nil {
			a.setCallPollStatus(err)
			return err
		}
		a.callMu.Lock()
		a.callConfigured = true
		a.callMu.Unlock()
	}

	response, err := a.runATCommand("AT+CLCC", 3*time.Second)
	if err != nil {
		a.setCallPollStatus(err)
		return err
	}
	a.applyCallPoll(parseCLCC(response), time.Now())
	a.setCallPollStatus(nil)
	return nil
}

func (a *app) applyCallPoll(calls []parsedCall, now time.Time) {
	var selected *parsedCall
	for i := range calls {
		candidate := &calls[i]
		if selected == nil || callStatePriority(candidate.State) > callStatePriority(selected.State) {
			selected = candidate
		}
	}

	a.callMu.Lock()
	var notify *callRecord
	if selected == nil {
		if a.activeCall != nil {
			ended := now
			a.activeCall.EndedAt = &ended
			a.activeCall.UpdatedAt = now
			a.activeCall.Missed = a.activeCall.Direction == "incoming" &&
				(a.activeCall.State == "incoming" || a.activeCall.State == "waiting")
			a.callHistory = append([]callRecord{*a.activeCall}, a.callHistory...)
			if len(a.callHistory) > 100 {
				a.callHistory = a.callHistory[:100]
			}
			a.activeCall = nil
		}
		a.callMu.Unlock()
		return
	}

	if a.activeCall == nil || a.activeCall.Index != selected.Index || a.activeCall.Direction != selected.Direction {
		record := &callRecord{
			ID:        fmt.Sprintf("%d-%d", now.UnixMilli(), selected.Index),
			Index:     selected.Index,
			Direction: selected.Direction,
			State:     selected.State,
			Number:    selected.Number,
			StartedAt: now,
			UpdatedAt: now,
		}
		a.activeCall = record
		if selected.Direction == "incoming" && (selected.State == "incoming" || selected.State == "waiting") {
			copy := *record
			notify = &copy
		}
	} else {
		wasRinging := a.activeCall.State == "incoming" || a.activeCall.State == "waiting"
		a.activeCall.State = selected.State
		a.activeCall.UpdatedAt = now
		if selected.Number != "" {
			a.activeCall.Number = selected.Number
		}
		if selected.Direction == "incoming" && !wasRinging &&
			(selected.State == "incoming" || selected.State == "waiting") {
			copy := *a.activeCall
			notify = &copy
		}
	}
	a.callMu.Unlock()

	if notify != nil {
		if a.callNotifier != nil {
			a.callNotifier(*notify)
		}
	}
}

func callStatePriority(state string) int {
	switch state {
	case "incoming", "waiting":
		return 5
	case "active":
		return 4
	case "alerting":
		return 3
	case "dialing":
		return 2
	case "held":
		return 1
	default:
		return 0
	}
}

func (a *app) setCallPollStatus(err error) {
	a.callMu.Lock()
	defer a.callMu.Unlock()
	a.callLastPoll = time.Now()
	if err != nil {
		a.callLastPollError = err.Error()
		return
	}
	a.callLastPollError = ""
}

func (a *app) callStatus(w http.ResponseWriter, _ *http.Request) {
	a.callMu.RLock()
	defer a.callMu.RUnlock()
	var active *callRecord
	if a.activeCall != nil {
		copy := *a.activeCall
		active = &copy
	}
	history := append([]callRecord(nil), a.callHistory...)
	writeJSON(w, http.StatusOK, map[string]any{
		"active":          active,
		"history":         history,
		"polling":         !a.demo,
		"poll_interval_s": int(a.callPollInterval.Seconds()),
		"last_poll":       a.callLastPoll,
		"last_poll_error": a.callLastPollError,
	})
}

func (a *app) rejectCall(w http.ResponseWriter, _ *http.Request) {
	if a.demo {
		a.applyCallPoll(nil, time.Now())
		writeJSON(w, http.StatusOK, map[string]bool{"rejected": true})
		return
	}
	response, err := a.runATCommand("AT+CHUP", 5*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rejected": true,
		"response": response,
	})
}
