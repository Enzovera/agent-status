package status

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	maxText = 160
	maxLong = 2000
)

type claudePayload struct {
	SessionID            string          `json:"session_id"`
	Cwd                  string          `json:"cwd"`
	HookEventName        string          `json:"hook_event_name"`
	Prompt               string          `json:"prompt"`
	SessionTitle         string          `json:"session_title"`
	ToolName             string          `json:"tool_name"`
	ToolInput            json.RawMessage `json:"tool_input"`
	NotificationType     string          `json:"notification_type"`
	Message              string          `json:"message"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	PermissionMode       string          `json:"permission_mode"`
	Model                json.RawMessage `json:"model"`
}

// modelName accepts the model as a plain string or as an object with an id.
func modelName(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	_ = json.Unmarshal(raw, &o)
	return firstNonEmpty(o.DisplayName, o.ID)
}

// ParseClaude maps a Claude Code hook payload (sent by an "http" hook) to an Event.
func ParseClaude(host string, body []byte, at time.Time) (Event, error) {
	var p claudePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("claude payload: %w", err)
	}
	ev := Event{
		Agent:     "claude",
		Host:      host,
		SessionID: p.SessionID,
		Cwd:       p.Cwd,
		Kind:      p.HookEventName,
		Task:      clip(p.SessionTitle),
		At:        at,
		Model:     clip(modelName(p.Model)),
		Mode:      clip(p.PermissionMode),
	}
	switch p.HookEventName {
	case "SessionStart":
		ev.State = StateIdle
	case "UserPromptSubmit":
		ev.State = StateWorking
		if ev.Task == "" {
			ev.Task = clip(p.Prompt)
		}
		ev.Prompt = clipLong(p.Prompt)
	case "PreToolUse":
		ev.State = StateWorking
		ev.Detail = toolDetail(p.ToolName, p.ToolInput)
		ev.Tool = clip(p.ToolName)
	case "PermissionRequest":
		ev.State = StateWaiting
		ev.Detail = "zgoda: " + toolDetail(p.ToolName, p.ToolInput)
		ev.Permission = true
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "agent_needs_input", "elicitation_dialog", "elicitation_url_dialog":
			ev.State = StateWaiting
			ev.Detail = clip(p.Message)
		case "idle_prompt":
			ev.State = StateIdle
		}
	case "Stop":
		ev.State = StateIdle
		ev.Detail = clip(p.LastAssistantMessage)
		ev.Message = clipLong(p.LastAssistantMessage)
	case "StopFailure":
		ev.State = StateError
		ev.Detail = clip(p.Message)
		ev.Message = clipLong(p.Message)
	case "SessionEnd":
		ev.State = StateEnded
	}
	return ev, nil
}

func toolDetail(name string, raw json.RawMessage) string {
	var in struct {
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Command     string `json:"command"`
	}
	_ = json.Unmarshal(raw, &in)
	for _, s := range []string{in.Description, in.FilePath, in.Pattern, in.URL, in.Command} {
		if s != "" {
			return clip(name + ": " + s)
		}
	}
	return name
}

type openCodePayload struct {
	Host       string          `json:"host"`
	Directory  string          `json:"directory"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type openCodeProps struct {
	SessionID string `json:"sessionID"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
	Info struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
	} `json:"info"`
	Title      string `json:"title"`
	Permission string `json:"permission"`
	Error      struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// ParseOpenCode maps an event forwarded by the OpenCode plugin to an Event.
// fallbackHost is used when the plugin did not report its host name.
func ParseOpenCode(fallbackHost string, body []byte, at time.Time) (Event, error) {
	var p openCodePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("opencode payload: %w", err)
	}
	var props openCodeProps
	if len(p.Properties) > 0 {
		if err := json.Unmarshal(p.Properties, &props); err != nil {
			return Event{}, fmt.Errorf("opencode properties: %w", err)
		}
	}
	host := p.Host
	if host == "" {
		host = fallbackHost
	}
	ev := Event{
		Agent:     "opencode",
		Host:      host,
		SessionID: props.SessionID,
		Cwd:       p.Directory,
		Kind:      p.Type,
		At:        at,
	}
	if ev.SessionID == "" {
		ev.SessionID = props.Info.ID
	}
	if props.Info.Directory != "" {
		ev.Cwd = props.Info.Directory
	}
	ev.Task = clip(props.Info.Title)

	switch {
	case p.Type == "session.created":
		ev.State = StateIdle
	case p.Type == "session.status":
		switch props.Status.Type {
		case "busy":
			ev.State = StateWorking
		case "retry":
			ev.State = StateWorking
			ev.Detail = "ponawia"
		case "idle":
			ev.State = StateIdle
		}
	case p.Type == "session.idle":
		ev.State = StateIdle
	case p.Type == "session.error":
		ev.State = StateError
		ev.Detail = clip(firstNonEmpty(props.Error.Data.Message, props.Error.Name))
		ev.Message = clipLong(firstNonEmpty(props.Error.Data.Message, props.Error.Name))
	case p.Type == "session.deleted":
		ev.State = StateEnded
	case p.Type == "permission.replied":
		ev.State = StateWorking
	case strings.HasPrefix(p.Type, "permission."):
		ev.State = StateWaiting
		ev.Detail = clip("zgoda: " + firstNonEmpty(props.Title, props.Permission))
		ev.Permission = true
	}
	return ev, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxText {
		return s
	}
	return string(r[:maxText-1]) + "…"
}

// clipLong keeps line breaks (the details view shows them) but caps the length.
func clipLong(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxLong {
		return s
	}
	return string(r[:maxLong-1]) + "…"
}
