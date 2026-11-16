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
		if q, ask := userQuestion(p.ToolName, p.ToolInput); q != "" {
			if ask {
				// Asked in the closing box: the turn goes on until Stop.
				ev.Ask = q
			} else {
				ev.State = StateWaiting
				ev.Detail = q
			}
		}
	case "PermissionRequest":
		ev.State = StateWaiting
		// Claude Code asks for "permission" to show a question too; that is
		// the question, not a permission.
		if q, _ := userQuestion(p.ToolName, p.ToolInput); q != "" {
			ev.Detail = q
			break
		}
		ev.Detail = "zgoda: " + toolDetail(p.ToolName, p.ToolInput)
		ev.Permission = true
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "agent_needs_input", "elicitation_dialog", "elicitation_url_dialog":
			ev.State = StateWaiting
			ev.Detail = clip(p.Message)
			// The generic message ("Claude needs your permission…") must not
			// replace what PermissionRequest or a question already said.
			ev.KeepWaiting = true
		case "idle_prompt":
			ev.State = StateIdle
			ev.KeepWaiting = true
		}
	case "Stop":
		ev.State = StateIdle
		ev.Detail = clip(p.LastAssistantMessage)
		ev.Message = clipLong(p.LastAssistantMessage)
		ev.EndsTurn = true
	case "StopFailure":
		ev.State = StateError
		ev.Detail = clip(p.Message)
		ev.Message = clipLong(p.Message)
	case "SessionEnd":
		ev.State = StateEnded
	}
	return ev, nil
}

// userQuestion recognizes Claude Code tools that stop and wait for the user.
// It returns what the user is asked; deferred is true when the question is
// only shown once the turn ends (the desktop app's closing box), so the
// session keeps working until Stop.
func userQuestion(tool string, raw json.RawMessage) (q string, deferred bool) {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
		Ask        string `json:"ask"`
		Initiation string `json:"initiation"`
		Title      string `json:"title"`
	}
	_ = json.Unmarshal(raw, &in)
	switch {
	case tool == "AskUserQuestion":
		q := "pytanie"
		if len(in.Questions) > 0 && in.Questions[0].Question != "" {
			q += ": " + in.Questions[0].Question
		}
		return clip(q), false
	case tool == "ExitPlanMode":
		return "plan do akceptacji", false
	case strings.HasSuffix(tool, "__wrap_up") && strings.TrimSpace(in.Ask) != "":
		return clip("decyzja: " + in.Ask), true
	case strings.HasSuffix(tool, "__start_session") && in.Initiation == "offer":
		return clip(strings.TrimSpace("propozycja sesji: " + in.Title)), false
	}
	return "", false
}

func toolDetail(name string, raw json.RawMessage) string {
	// Claude Code uses snake_case keys, OpenCode camelCase.
	var in struct {
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
		FilePathCC  string `json:"filePath"`
		Path        string `json:"path"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Command     string `json:"command"`
		Query       string `json:"query"`
	}
	_ = json.Unmarshal(raw, &in)
	for _, s := range []string{in.Description, in.FilePath, in.FilePathCC, in.Path, in.Pattern, in.URL, in.Command, in.Query} {
		if s != "" {
			return clip(name + ": " + s)
		}
	}
	return name
}

type openCodePayload struct {
	Host      string          `json:"host"`
	Directory string          `json:"directory"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
}

type openCodeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e openCodeError) text() string { return firstNonEmpty(e.Message, e.Type) }

type openCodeModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant"`
}

func (m *openCodeModel) name() string {
	if m == nil || m.ID == "" {
		return ""
	}
	s := m.ID
	if m.ProviderID != "" {
		s = m.ProviderID + "/" + s
	}
	if m.Variant != "" {
		s += " (" + m.Variant + ")"
	}
	return s
}

// openCodeStatus is session.status's {type, message}. Other OpenCode events
// (workspace.status, shell.exited) send "status" as a bare string; accept that
// as the type so one of them never makes the whole event unreadable.
type openCodeStatus struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (s *openCodeStatus) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = openCodeStatus{Type: str}
		return nil
	}
	type plain openCodeStatus
	return json.Unmarshal(b, (*plain)(s))
}

// openCodeData is the union of the "data" fields of the OpenCode 2.x events
// the plugin forwards.
type openCodeData struct {
	SessionID string `json:"sessionID"`
	Location  struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Title  string         `json:"title"`
	Model  *openCodeModel `json:"model"`
	Status openCodeStatus `json:"status"`
	Item   struct {
		Type    string `json:"type"`
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	} `json:"item"`
	Error   openCodeError   `json:"error"`
	Reason  string          `json:"reason"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Text    string          `json:"text"`
	Action  string          `json:"action"`
	Res     []string        `json:"resources"`
	Message string          `json:"message"`
	Reply   string          `json:"reply"`
	Form    struct {
		SessionID string `json:"sessionID"`
		Title     string `json:"title"`
	} `json:"form"`
}

var interruptReason = map[string]string{
	"user":       "przerwane przez Ciebie",
	"shutdown":   "przerwane: zamknięcie OpenCode",
	"superseded": "przerwane: nowe polecenie",
	"inactivity": "przerwane: brak aktywności",
}

// ParseOpenCode maps an event forwarded by the OpenCode plugin to an Event.
// fallbackHost is used when the plugin did not report its host name.
func ParseOpenCode(fallbackHost string, body []byte, at time.Time) (Event, error) {
	var p openCodePayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("opencode payload: %w", err)
	}
	var d openCodeData
	if len(p.Data) > 0 {
		if err := json.Unmarshal(p.Data, &d); err != nil {
			return Event{}, fmt.Errorf("opencode data: %w", err)
		}
	}
	host := p.Host
	if host == "" {
		host = fallbackHost
	}
	ev := Event{
		Agent:     "opencode",
		Host:      host,
		SessionID: firstNonEmpty(d.SessionID, d.Form.SessionID),
		Cwd:       firstNonEmpty(d.Location.Directory, p.Directory),
		Kind:      p.Type,
		At:        at,
		Model:     clip(d.Model.name()),
	}

	switch p.Type {
	case "session.created":
		ev.State = StateIdle
		ev.Task = clip(d.Title)
	case "session.renamed":
		ev.Task = clip(d.Title)
	case "session.deleted":
		ev.State = StateEnded
	case "session.status":
		switch d.Status.Type {
		case "busy":
			ev.State = StateWorking
		case "retry":
			ev.State = StateWorking
			ev.Detail = clip(strings.TrimSpace("ponawia " + d.Status.Message))
		case "idle":
			ev.State = StateIdle
		}
	case "session.idle", "session.execution.succeeded":
		ev.State = StateIdle
	case "session.execution.started":
		ev.State = StateWorking
	case "session.execution.failed":
		ev.State = StateError
		ev.Detail = clip(d.Error.text())
		ev.Message = clipLong(d.Error.text())
	case "session.execution.interrupted":
		ev.State = StateIdle
		ev.Detail = firstNonEmpty(interruptReason[d.Reason], "przerwane")
	case "session.inbox.enqueued":
		if d.Item.Type == "user" {
			ev.Prompt = clipLong(d.Item.Payload.Text)
		}
	case "session.tool.called":
		ev.State = StateWorking
		ev.Tool = clip(d.Name)
		ev.Detail = toolDetail(firstNonEmpty(d.Name, "narzędzie"), d.Input)
	case "session.tool.failed":
		ev.Detail = clip("błąd narzędzia: " + d.Error.text())
	case "session.text.ended":
		ev.Message = clipLong(d.Text)
		ev.Detail = clip(d.Text)
	case "permission.asked":
		ev.State = StateWaiting
		ev.Permission = true
		what := d.Action
		if len(d.Res) > 0 {
			what += ": " + strings.Join(d.Res, ", ")
		}
		ev.Detail = clip("zgoda: " + firstNonEmpty(d.Message, what))
	case "permission.replied":
		ev.State = StateWorking
	case "form.created":
		ev.State = StateWaiting
		ev.Detail = clip("pytanie: " + d.Form.Title)
	case "form.replied", "form.cancelled":
		ev.State = StateWorking
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
