package status

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	ToolResponse         json.RawMessage `json:"tool_response"`
	NotificationType     string          `json:"notification_type"`
	Message              string          `json:"message"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	PermissionMode       string          `json:"permission_mode"`
	Model                json.RawMessage `json:"model"`

	Source       string          `json:"source"`  // SessionStart, PostModelSwitch
	Reason       string          `json:"reason"`  // SessionEnd, PermissionDenied
	Trigger      string          `json:"trigger"` // PreCompact, PostCompact
	AgentID      string          `json:"agent_id"`
	AgentType    string          `json:"agent_type"`
	TaskID       string          `json:"task_id"`
	TaskSubject  string          `json:"task_subject"`
	Error        json.RawMessage `json:"error"` // a string in StopFailure and PostToolUseFailure
	ErrorDetails string          `json:"error_details"`
	IsInterrupt  bool            `json:"is_interrupt"`
	DurationMs   float64         `json:"duration_ms"`
	ToModel      string          `json:"to_model"`
}

// rawText reads a field documented as a string but tolerates any other shape,
// so one unexpected value never makes the whole hook unreadable.
func rawText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if t := strings.TrimSpace(string(raw)); t != "null" {
		return t
	}
	return ""
}

// stopError names StopFailure error types; other types are shown as sent.
var stopError = map[string]string{
	"rate_limit":            "limit zapytań",
	"overloaded":            "przeciążenie API",
	"authentication_failed": "błąd logowania",
	"billing_error":         "płatności",
	"server_error":          "błąd serwera API",
	"max_output_tokens":     "za długa odpowiedź",
}

// quotaResume describes the end of a wait for a claude.ai usage limit.
var quotaResume = map[string]string{
	"quota_auto_resume_fired":    "limit: wznowiono automatycznie",
	"quota_auto_resume_stale":    "limit minął: naciśnij Enter, by wznowić",
	"quota_auto_resume_disabled": "limit: bez automatycznego wznowienia",
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
		AgentID:   p.AgentID,
		Cwd:       p.Cwd,
		Kind:      p.HookEventName,
		Task:      clip(p.SessionTitle),
		At:        at,
		Model:     clip(modelName(p.Model)),
		Mode:      clip(p.PermissionMode),
	}
	switch p.HookEventName {
	case "SessionStart":
		// Claude Code runs only command and mcp_tool hooks on SessionStart, so
		// an http hook never delivers it; parsed for anything that forwards it.
		ev.State = StateIdle
		ev.Reason = p.Source
	case "UserPromptSubmit":
		ev.State = StateWorking
		if injected(p.Prompt) {
			// A subagent's report or a notification handed to the session as a
			// "prompt": work goes on, but it is not yours and not the title.
			break
		}
		if ev.Task == "" {
			ev.Task = clip(p.Prompt)
		}
		ev.Prompt = clipLong(p.Prompt)
	case "PreToolUse":
		ev.State = StateWorking
		ev.Detail = toolDetail(p.ToolName, p.ToolInput)
		ev.Tool = clip(p.ToolName)
		if p.AgentID == "" && isAgentTool(p.ToolName) {
			ev.LaunchDesc, ev.LaunchType = launchOf(p.ToolInput)
		}
		if q, ask := userQuestion(p.ToolName, p.ToolInput); q != "" {
			if ask {
				// Asked in the closing box: the turn goes on until Stop.
				ev.Ask = q
			} else {
				ev.State = StateWaiting
				ev.Detail = q
				ev.WaitFor = waitKey(p.AgentID, p.ToolName)
			}
		}
	// PostToolUse and PostToolUseFailure put the tool name in Reason, not in
	// Tool: Tool counts a call, and PreToolUse already counted this one.
	case "PostToolUse":
		// Fires after every successful tool call: nothing to show, only the
		// duration for the archive.
		ev.Reason = clip(p.ToolName)
		ev.DurationMs = int64(p.DurationMs)
		ev.Resolves = waitKey(p.AgentID, p.ToolName)
		if p.AgentID == "" && isAgentTool(p.ToolName) {
			// The Agent tool returns the new subagent's id (at once for a background
			// one): that ties its description to it exactly.
			ev.LaunchDesc, ev.LaunchType = launchOf(p.ToolInput)
			ev.LaunchedID = launchedID(p.ToolResponse)
		}
	case "PostToolUseFailure":
		ev.Reason = clip(p.ToolName)
		ev.DurationMs = int64(p.DurationMs)
		ev.Failed = true
		ev.Resolves = waitKey(p.AgentID, p.ToolName)
		if p.IsInterrupt {
			ev.ErrorKind = "interrupt"
			ev.Detail = clip("przerwane: " + p.ToolName)
		} else {
			ev.Detail = clip("błąd narzędzia: " + p.ToolName + ": " + rawText(p.Error))
		}
	case "PermissionDenied":
		// Auto mode refused a tool call; Claude carries on without it.
		ev.Reason = clip(p.Reason)
		ev.Detail = clip("odmowa: " + toolDetail(p.ToolName, p.ToolInput))
	case "PermissionRequest":
		ev.State = StateWaiting
		ev.WaitFor = waitKey(p.AgentID, p.ToolName)
		// Claude Code asks for "permission" to show a question too; that is
		// the question, not a permission.
		if q, _ := userQuestion(p.ToolName, p.ToolInput); q != "" {
			ev.Detail = q
			break
		}
		// What you approve is the command, not its description; the statistics
		// group permission requests by it too.
		ev.Detail = "zgoda: " + permissionDetail(p.ToolName, p.ToolInput)
		ev.Permission = true
	case "Notification":
		ev.Reason = p.NotificationType
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
		case "quota_auto_resume_fired", "quota_auto_resume_stale", "quota_auto_resume_disabled":
			// The wait for a claude.ai usage limit ended. Only text: when the
			// task really continues, UserPromptSubmit follows.
			ev.Detail = quotaResume[p.NotificationType]
			ev.KeepWaiting = true
		}
	case "SubagentStart":
		ev.Subagent = clip(p.AgentType)
		ev.SubagentID = p.AgentID
		ev.SubagentStarted = true
		ev.Detail = clip(strings.TrimSpace("podagent: " + p.AgentType))
	case "SubagentStop":
		// Also fires for Claude Code's internal agents (prompt suggestions,
		// /btw) that never sent SubagentStart; stopping an unknown id is a no-op.
		ev.Subagent = clip(p.AgentType)
		ev.SubagentID = p.AgentID
		ev.SubagentStopped = true
	case "TaskCreated":
		ev.TodoID = p.TaskID
		ev.Todo = clip(p.TaskSubject)
	case "TaskCompleted":
		ev.TodoID = p.TaskID
		ev.Todo = clip(p.TaskSubject)
		ev.TodoDone = true
		ev.Detail = clip("zrobione: " + p.TaskSubject)
	case "PreCompact":
		ev.Reason = p.Trigger
		ev.Detail = "kompaktuje kontekst"
		if p.Trigger == "auto" {
			ev.Detail += " (auto)"
		}
	case "PostCompact":
		ev.Reason = p.Trigger
		ev.Detail = "kontekst skompaktowany"
		ev.Compacted = true
	case "PostModelSwitch":
		ev.Model = clip(p.ToModel)
		ev.Reason = p.Source
	case "Stop":
		ev.State = StateIdle
		ev.Detail = clip(p.LastAssistantMessage)
		ev.Message = clipLong(p.LastAssistantMessage)
		ev.EndsTurn = true
	case "StopFailure":
		// error is the type (rate_limit…), error_details the API's own words and
		// last_assistant_message the error line shown in the conversation.
		ev.State = StateError
		ev.ErrorKind = clip(rawText(p.Error))
		ev.Detail = "błąd: " + firstNonEmpty(stopError[ev.ErrorKind], ev.ErrorKind, "API")
		if d := firstNonEmpty(p.ErrorDetails, p.LastAssistantMessage); d != "" {
			ev.Detail += " — " + d
		}
		ev.Detail = clip(ev.Detail)
		ev.Message = clipLong(firstNonEmpty(p.LastAssistantMessage, p.ErrorDetails))
	case "SessionEnd":
		ev.State = StateEnded
		ev.Reason = p.Reason
	}
	if p.AgentID != "" {
		ev = asSubagentSession(ev, p)
	}
	return ev, nil
}

// asSubagentSession moves an event fired inside a subagent to the subagent's own
// session (id = agent_id, parent = the main session), the way OpenCode reports its
// subagents. The main session then shows only its main thread: a subagent's tool
// calls, permission prompts and questions no longer change its state or caption.
func asSubagentSession(ev Event, p claudePayload) Event {
	ev.ParentID, ev.SessionID = ev.SessionID, p.AgentID
	ev.Subagent = clip(p.AgentType)
	// The parent lists its children itself (Store.trackChild), from these sessions.
	ev.SubagentID, ev.SubagentStarted, ev.SubagentSeen, ev.SubagentStopped, ev.SubagentDoing = "", false, false, false, ""
	ev.Task = "" // the session title belongs to the main session
	ev.Ask = ""  // the closing-box question is the main thread's
	ev.EndsTurn = false
	switch p.HookEventName {
	case "SubagentStart":
		ev.State = StateWorking
		ev.Detail = "start"
	case "SubagentStop":
		ev.State = StateEnded
		ev.Message = clipLong(p.LastAssistantMessage)
		ev.Detail = clip(firstNonEmpty(p.LastAssistantMessage, "skończył"))
	}
	return ev
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

// injectedTags open text Claude Code hands to a session as a user prompt although
// nobody typed it: subagent reports, background-task and other notifications.
var injectedTags = []string{"<agent-message", "<task-notification", "<system-reminder", "<side-sessions-event",
	"<ci-monitor-event", "<teammate-message", "<local-command"}

func injected(prompt string) bool {
	p := strings.TrimSpace(prompt)
	for _, t := range injectedTags {
		if strings.HasPrefix(p, t) {
			return true
		}
	}
	return false
}

// isAgentTool: the tool that starts a subagent ("Task" in older Claude Code).
func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

// launchOf reads what a subagent was started for and as which agent.
func launchOf(raw json.RawMessage) (desc, typ string) {
	var in struct {
		Description  string `json:"description"`
		SubagentType string `json:"subagent_type"`
	}
	_ = json.Unmarshal(raw, &in)
	return clip(in.Description), clip(in.SubagentType)
}

var agentIDText = regexp.MustCompile(`agent[_ ]?[iI][dD]\W{1,3}([A-Za-z0-9_-]{6,})`)

// launchedID finds the started subagent's id in the Agent tool's result: a JSON
// field (agentId / agent_id) or, in the text Claude reads, "agentId: …".
func launchedID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var o struct {
		AgentID  string `json:"agentId"`
		AgentID2 string `json:"agent_id"`
	}
	if json.Unmarshal(raw, &o) == nil && firstNonEmpty(o.AgentID, o.AgentID2) != "" {
		return firstNonEmpty(o.AgentID, o.AgentID2)
	}
	if m := agentIDText.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// waitKey names the tool call a session waits on: which agent (empty for the
// main thread, else the subagent's id) and which tool. The tool's completion
// with the same key ends the wait, while a background subagent's tools don't.
func waitKey(agentID, tool string) string {
	if tool == "" {
		return ""
	}
	return agentID + "|" + tool
}

// permissionDetail is toolDetail with a shell command shown as the command
// itself rather than its description.
func permissionDetail(name string, raw json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(raw, &in)
	if c := strings.TrimSpace(in.Command); c != "" {
		return clip(name + ": " + c)
	}
	return toolDetail(name, raw)
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
	// ParentID is set on session.created of a child session, which the
	// subagent tool creates; the plugin repeats it on the child's later events.
	ParentID string `json:"parentID"`
	Agent    string `json:"agent"` // session.created: the agent the session runs as
	Location struct {
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
		ParentID:  d.ParentID,
	}
	if ev.ParentID == ev.SessionID {
		ev.ParentID = ""
	}

	switch p.Type {
	case "session.created":
		ev.State = StateIdle
		ev.Task = clip(d.Title)
		if ev.ParentID != "" {
			// The subagent tool's child session: the title is the task's
			// short description, agent the subagent type.
			ev.Subagent = clip(d.Agent)
			ev.Detail = clip(strings.TrimSpace("podagent " + d.Agent))
		}
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
