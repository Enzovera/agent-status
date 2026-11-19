package status

import (
	"strings"
	"testing"
	"time"
)

func TestParseClaude(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		body       string
		wantState  State
		wantTask   string
		wantDetail string
	}{
		{"session start", `{"session_id":"s1","cwd":"C:\\code\\forma","hook_event_name":"SessionStart","source":"startup"}`, StateIdle, "", ""},
		{"prompt", `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"napraw   testy\nAPI"}`, StateWorking, "napraw testy API", ""},
		{"prompt with title", `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"x","session_title":"Auth fix"}`, StateWorking, "Auth fix", ""},
		{"tool", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run tests"}}`, StateWorking, "", "Bash: Run tests"},
		{"tool without input", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"TodoWrite"}`, StateWorking, "", "TodoWrite"},
		{"question", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Którą bazę?","header":"DB"}]}}`, StateWaiting, "", "pytanie: Którą bazę?"},
		{"plan", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode","tool_input":{"plan":"x"}}`, StateWaiting, "", "plan do akceptacji"},
		{"session offer", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"mcp__ccd_session__start_session","tool_input":{"initiation":"offer","title":"Fix CI"}}`, StateWaiting, "", "propozycja sesji: Fix CI"},
		{"wrap up without ask", `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"mcp__ccd_turn__wrap_up","tool_input":{"recap":"done"}}`, StateWorking, "", "mcp__ccd_turn__wrap_up"},
		{"question permission", `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Którą bazę?"}]}}`, StateWaiting, "", "pytanie: Którą bazę?"},
		{"permission", `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Edit","tool_input":{"file_path":"/a/b.go"}}`, StateWaiting, "", "zgoda: Edit: /a/b.go"},
		{"permission prompt", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs permission"}`, StateWaiting, "", "Claude needs permission"},
		{"idle prompt", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"idle_prompt","message":"waiting"}`, StateIdle, "", ""},
		{"other notification", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"auth_success"}`, "", "", ""},
		{"stop", `{"session_id":"s1","hook_event_name":"Stop","last_assistant_message":"Gotowe."}`, StateIdle, "", "Gotowe."},
		{"stop failure", `{"session_id":"s1","hook_event_name":"StopFailure","error":"rate_limit","error_details":"429 Too Many Requests","last_assistant_message":"API Error: Rate limit reached"}`, StateError, "", "błąd: limit zapytań — 429 Too Many Requests"},
		{"stop failure without details", `{"session_id":"s1","hook_event_name":"StopFailure","error":"overloaded","last_assistant_message":"API Error: Overloaded"}`, StateError, "", "błąd: przeciążenie API — API Error: Overloaded"},
		{"stop failure unnamed type", `{"session_id":"s1","hook_event_name":"StopFailure","error":"model_not_found"}`, StateError, "", "błąd: model_not_found"},
		{"stop failure without error", `{"session_id":"s1","hook_event_name":"StopFailure"}`, StateError, "", "błąd: API"},
		{"end", `{"session_id":"s1","hook_event_name":"SessionEnd","reason":"other"}`, StateEnded, "", ""},
		{"post tool", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"/a.txt","content":"x"},"tool_response":{"filePath":"/a.txt","type":"create"},"tool_use_id":"toolu_1","duration_ms":12}`, "", "", ""},
		{"tool failure", `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash","tool_input":{"command":"npm test"},"tool_use_id":"toolu_1","error":"Exit code 1\nError: Cannot find module 'express'","is_interrupt":false,"duration_ms":4187}`, "", "", "błąd narzędzia: Bash: Exit code 1 Error: Cannot find module 'express'"},
		{"tool interrupted", `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"aborted","is_interrupt":true}`, "", "", "przerwane: Bash"},
		{"permission denied", `{"session_id":"s1","permission_mode":"auto","hook_event_name":"PermissionDenied","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/build","description":"Clean build directory"},"tool_use_id":"toolu_1","reason":"[Irreversible Local Destruction]"}`, "", "", "odmowa: Bash: Clean build directory"},
		{"task created", `{"session_id":"s1","hook_event_name":"TaskCreated","task_id":"task-001","task_subject":"Implement user authentication","task_description":"Add login"}`, "", "", ""},
		{"task completed", `{"session_id":"s1","hook_event_name":"TaskCompleted","task_id":"task-001","task_subject":"Implement user authentication"}`, "", "", "zrobione: Implement user authentication"},
		{"pre compact manual", `{"session_id":"s1","hook_event_name":"PreCompact","trigger":"manual","custom_instructions":null}`, "", "", "kompaktuje kontekst"},
		{"pre compact auto", `{"session_id":"s1","hook_event_name":"PreCompact","trigger":"auto","custom_instructions":null}`, "", "", "kompaktuje kontekst (auto)"},
		{"post compact", `{"session_id":"s1","hook_event_name":"PostCompact","trigger":"manual","compact_summary":"Summary..."}`, "", "", "kontekst skompaktowany"},
		{"model switch", `{"session_id":"s1","hook_event_name":"PostModelSwitch","from_model":"claude-sonnet-5","to_model":"claude-opus-5","requested_model":null,"source":"auto"}`, "", "", ""},
		{"quota resumed", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"quota_auto_resume_fired","message":"Resuming"}`, "", "", "limit: wznowiono automatycznie"},
		{"quota stale", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"quota_auto_resume_stale","message":"x"}`, "", "", "limit minął: naciśnij Enter, by wznowić"},
		{"quota disabled", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"quota_auto_resume_disabled","message":"x"}`, "", "", "limit: bez automatycznego wznowienia"},
		{"unknown", `{"session_id":"s1","hook_event_name":"PostToolBatch"}`, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseClaude("pc1", []byte(tt.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if ev.State != tt.wantState || ev.Task != tt.wantTask || ev.Detail != tt.wantDetail {
				t.Errorf("got state=%q task=%q detail=%q, want %q %q %q", ev.State, ev.Task, ev.Detail, tt.wantState, tt.wantTask, tt.wantDetail)
			}
			if ev.Agent != "claude" || ev.Host != "pc1" || ev.SessionID != "s1" {
				t.Errorf("identity: %+v", ev)
			}
		})
	}
}

func TestParseClaudeNewHooks(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		body string
		want Event
	}{
		{"session start", `{"session_id":"s1","hook_event_name":"SessionStart","source":"resume","model":"claude-opus-5"}`,
			Event{State: StateIdle, Reason: "resume", Model: "claude-opus-5"}},
		{"post tool", `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Write","tool_use_id":"toolu_1","duration_ms":12}`,
			Event{Reason: "Write", DurationMs: 12, Resolves: "|Write"}},
		{"tool failure", `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"Exit code 1","duration_ms":4187.6}`,
			Event{Reason: "Bash", DurationMs: 4187, Failed: true, Resolves: "|Bash", Detail: "błąd narzędzia: Bash: Exit code 1"}},
		{"tool interrupted", `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"aborted","is_interrupt":true,"duration_ms":5}`,
			Event{Reason: "Bash", DurationMs: 5, Failed: true, Resolves: "|Bash", ErrorKind: "interrupt", Detail: "przerwane: Bash"}},
		{"permission denied", `{"session_id":"s1","hook_event_name":"PermissionDenied","tool_name":"Bash","tool_input":{"command":"curl x"},"reason":"[Data Exfiltration]","permission_mode":"auto"}`,
			Event{Reason: "[Data Exfiltration]", Detail: "odmowa: Bash: curl x", Mode: "auto"}},
		{"task created", `{"session_id":"s1","hook_event_name":"TaskCreated","task_id":"task-001","task_subject":"Auth"}`,
			Event{TodoID: "task-001", Todo: "Auth"}},
		{"task completed", `{"session_id":"s1","hook_event_name":"TaskCompleted","task_id":"task-001","task_subject":"Auth"}`,
			Event{TodoID: "task-001", Todo: "Auth", TodoDone: true, Detail: "zrobione: Auth"}},
		{"pre compact", `{"session_id":"s1","hook_event_name":"PreCompact","trigger":"auto","custom_instructions":null}`,
			Event{Reason: "auto", Detail: "kompaktuje kontekst (auto)"}},
		{"post compact", `{"session_id":"s1","hook_event_name":"PostCompact","trigger":"manual","compact_summary":"s"}`,
			Event{Reason: "manual", Detail: "kontekst skompaktowany", Compacted: true}},
		{"model switch", `{"session_id":"s1","hook_event_name":"PostModelSwitch","from_model":"claude-sonnet-5","to_model":"claude-opus-5","requested_model":"opus","source":"command"}`,
			Event{Model: "claude-opus-5", Reason: "command"}},
		{"stop failure", `{"session_id":"s1","hook_event_name":"StopFailure","error":"rate_limit","error_details":"429 Too Many Requests","last_assistant_message":"API Error: Rate limit reached"}`,
			Event{State: StateError, ErrorKind: "rate_limit", Detail: "błąd: limit zapytań — 429 Too Many Requests", Message: "API Error: Rate limit reached"}},
		{"session end", `{"session_id":"s1","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
			Event{State: StateEnded, Reason: "prompt_input_exit"}},
		{"notification", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"quota_auto_resume_fired","message":"m"}`,
			Event{Reason: "quota_auto_resume_fired", Detail: "limit: wznowiono automatycznie", KeepWaiting: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseClaude("pc1", []byte(tt.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if ev.Tool != "" {
				t.Errorf("Tool = %q; only PreToolUse may count a tool call", ev.Tool)
			}
			want := tt.want
			want.Agent, want.Host, want.SessionID, want.At = "claude", "pc1", "s1", at
			want.Kind = ev.Kind
			if ev != want {
				t.Errorf("got  %+v\nwant %+v", ev, want)
			}
		})
	}
}

func TestParseClaudeOddShapes(t *testing.T) {
	// An error that is not a string must not make the hook unreadable.
	ev, err := ParseClaude("pc1", []byte(`{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"mcp__x__y","error":{"code":1}}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ev.Detail != `błąd narzędzia: mcp__x__y: {"code":1}` {
		t.Errorf("detail = %q", ev.Detail)
	}
}

func TestParseOpenCode(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		body       string
		wantState  State
		wantTask   string
		wantDetail string
	}{
		{"created", `{"host":"mac","directory":"/src","type":"session.created","data":{"sessionID":"o1","location":{"directory":"/src/app"},"slug":"x","title":"New session","model":{"id":"opus","providerID":"anthropic"}}}`, StateIdle, "New session", ""},
		{"renamed", `{"type":"session.renamed","data":{"sessionID":"o1","title":"Refactor auth"}}`, "", "Refactor auth", ""},
		{"busy", `{"type":"session.status","data":{"sessionID":"o1","status":{"type":"busy"}}}`, StateWorking, "", ""},
		{"retry", `{"type":"session.status","data":{"sessionID":"o1","status":{"type":"retry","attempt":2,"message":"overloaded","next":1}}}`, StateWorking, "", "ponawia overloaded"},
		{"idle status", `{"type":"session.status","data":{"sessionID":"o1","status":{"type":"idle"}}}`, StateIdle, "", ""},
		{"busy as string", `{"type":"session.status","data":{"sessionID":"o1","status":"busy"}}`, StateWorking, "", ""},
		{"idle as string", `{"type":"session.status","data":{"sessionID":"o1","status":"idle"}}`, StateIdle, "", ""},
		{"other string status", `{"type":"workspace.status","data":{"sessionID":"o1","status":"connected"}}`, "", "", ""},
		{"idle", `{"type":"session.idle","data":{"sessionID":"o1"}}`, StateIdle, "", ""},
		{"execution started", `{"type":"session.execution.started","data":{"sessionID":"o1"}}`, StateWorking, "", ""},
		{"execution succeeded", `{"type":"session.execution.succeeded","data":{"sessionID":"o1"}}`, StateIdle, "", ""},
		{"execution failed", `{"type":"session.execution.failed","data":{"sessionID":"o1","error":{"type":"api","message":"rate limit"}}}`, StateError, "", "rate limit"},
		{"interrupted", `{"type":"session.execution.interrupted","data":{"sessionID":"o1","reason":"user"}}`, StateIdle, "", "przerwane przez Ciebie"},
		{"prompt", `{"type":"session.inbox.enqueued","data":{"sessionID":"o1","inboxID":"m1","item":{"type":"user","payload":{"text":"dodaj testy"}}}}`, "", "", ""},
		{"tool", `{"type":"session.tool.called","data":{"sessionID":"o1","id":"c1","name":"read","input":{"filePath":"/src/app/main.go"},"executed":false}}`, StateWorking, "", "read: /src/app/main.go"},
		{"tool failed", `{"type":"session.tool.failed","data":{"sessionID":"o1","id":"c1","error":{"type":"x","message":"not found"},"executed":true}}`, "", "", "błąd narzędzia: not found"},
		{"text", `{"type":"session.text.ended","data":{"sessionID":"o1","ordinal":0,"text":"Gotowe.\nTesty przechodzą."}}`, "", "", "Gotowe. Testy przechodzą."},
		{"permission asked", `{"type":"permission.asked","data":{"id":"p1","sessionID":"o1","action":"bash","resources":["rm -rf build"]}}`, StateWaiting, "", "zgoda: bash: rm -rf build"},
		{"permission with message", `{"type":"permission.asked","data":{"id":"p1","sessionID":"o1","action":"edit","resources":[],"message":"Edit main.go"}}`, StateWaiting, "", "zgoda: Edit main.go"},
		{"permission replied", `{"type":"permission.replied","data":{"sessionID":"o1","requestID":"p1","reply":"once"}}`, StateWorking, "", ""},
		{"question", `{"type":"form.created","data":{"form":{"id":"f1","sessionID":"o1","title":"Która baza?"}}}`, StateWaiting, "", "pytanie: Która baza?"},
		{"question answered", `{"type":"form.replied","data":{"id":"f1","sessionID":"o1","answer":{}}}`, StateWorking, "", ""},
		{"deleted", `{"type":"session.deleted","data":{"sessionID":"o1"}}`, StateEnded, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseOpenCode("10.0.0.5", []byte(tt.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if ev.SessionID != "o1" || ev.State != tt.wantState || ev.Task != tt.wantTask || ev.Detail != tt.wantDetail {
				t.Errorf("got id=%q state=%q task=%q detail=%q", ev.SessionID, ev.State, ev.Task, ev.Detail)
			}
		})
	}
}

func TestParseOpenCodeExtras(t *testing.T) {
	at := time.Now()
	parse := func(body string) Event {
		t.Helper()
		ev, err := ParseOpenCode("h", []byte(body), at)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	ev := parse(`{"directory":"/fallback","type":"session.created","data":{"sessionID":"o1","location":{"directory":"/src/app"},"model":{"id":"claude-opus-5-5","providerID":"anthropic","variant":"high"}}}`)
	if ev.Cwd != "/src/app" || ev.Model != "anthropic/claude-opus-5-5 (high)" {
		t.Errorf("created: cwd=%q model=%q", ev.Cwd, ev.Model)
	}
	ev = parse(`{"directory":"/fallback","type":"session.idle","data":{"sessionID":"o1"}}`)
	if ev.Cwd != "/fallback" || ev.Model != "" {
		t.Errorf("idle: cwd=%q model=%q", ev.Cwd, ev.Model)
	}
	ev = parse(`{"type":"session.model.selected","data":{"sessionID":"o1","model":{"id":"gpt-6","providerID":"openai"}}}`)
	if ev.Model != "openai/gpt-6" || ev.State != "" {
		t.Errorf("model selected: %+v", ev)
	}
	ev = parse(`{"type":"session.inbox.enqueued","data":{"sessionID":"o1","item":{"type":"user","payload":{"text":"dodaj\ntesty"}}}}`)
	if ev.Prompt != "dodaj\ntesty" {
		t.Errorf("prompt = %q", ev.Prompt)
	}
	ev = parse(`{"type":"session.tool.called","data":{"sessionID":"o1","name":"bash","input":{"command":"go test","description":"Run tests"}}}`)
	if ev.Tool != "bash" || ev.Detail != "bash: Run tests" {
		t.Errorf("tool: %+v", ev)
	}
	ev = parse(`{"type":"session.text.ended","data":{"sessionID":"o1","text":"Gotowe.\nTesty przechodzą."}}`)
	if ev.Message != "Gotowe.\nTesty przechodzą." {
		t.Errorf("message = %q", ev.Message)
	}
	ev = parse(`{"type":"permission.asked","data":{"id":"p1","sessionID":"o1","action":"bash","resources":[]}}`)
	if !ev.Permission || ev.Detail != "zgoda: bash" {
		t.Errorf("permission: %+v", ev)
	}
	ev = parse(`{"type":"server.connected","data":{}}`)
	if ev.SessionID != "" {
		t.Errorf("event without session should have no id: %+v", ev)
	}
}

func TestParseOpenCodeHost(t *testing.T) {
	ev, _ := ParseOpenCode("10.0.0.5", []byte(`{"type":"session.idle","data":{"sessionID":"o1"}}`), time.Now())
	if ev.Host != "10.0.0.5" {
		t.Errorf("fallback host = %q", ev.Host)
	}
	ev, _ = ParseOpenCode("10.0.0.5", []byte(`{"host":"mac","type":"session.idle","data":{"sessionID":"o1"}}`), time.Now())
	if ev.Host != "mac" {
		t.Errorf("reported host = %q", ev.Host)
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("ą", 300)
	got := []rune(clip(long))
	if len(got) != maxText || got[len(got)-1] != '…' {
		t.Errorf("clip length %d", len(got))
	}
}

func TestParseOpenCodeChild(t *testing.T) {
	at := time.Now()
	ev, err := ParseOpenCode("h", []byte(`{"type":"session.created","data":{"sessionID":"c","parentID":"p","title":"Find handlers","agent":"explore","slug":"x","version":"2.3.0"}}`), at)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ParentID != "p" || ev.Subagent != "explore" || ev.Task != "Find handlers" || ev.State != StateIdle || ev.Detail != "podagent explore" {
		t.Errorf("child created = %+v", ev)
	}
	ev, _ = ParseOpenCode("h", []byte(`{"type":"session.created","data":{"sessionID":"r","title":"New session","agent":"build"}}`), at)
	if ev.ParentID != "" || ev.Subagent != "" || ev.Detail != "" {
		t.Errorf("root created = %+v", ev)
	}
	ev, _ = ParseOpenCode("h", []byte(`{"type":"permission.asked","data":{"sessionID":"c","parentID":"p","action":"bash","resources":["ls"]}}`), at)
	if ev.ParentID != "p" || ev.State != StateWaiting || ev.Subagent != "" {
		t.Errorf("child permission = %+v", ev)
	}
	ev, _ = ParseOpenCode("h", []byte(`{"type":"session.idle","data":{"sessionID":"c","parentID":"c"}}`), at)
	if ev.ParentID != "" {
		t.Errorf("self parent kept: %+v", ev)
	}
}

// Everything fired inside a Claude Code subagent belongs to the subagent's own
// session (id = agent_id, parent = the main session), like OpenCode's subagents.
func TestParseClaudeSubagentSession(t *testing.T) {
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, body string
		state      State
		detail     string
	}{
		{"start", `{"session_id":"s1","hook_event_name":"SubagentStart","agent_id":"ag1","agent_type":"Explore"}`, StateWorking, "start"},
		{"tool", `{"session_id":"s1","hook_event_name":"PreToolUse","agent_id":"ag1","agent_type":"Explore","tool_name":"Grep","tool_input":{"pattern":"x"}}`, StateWorking, "Grep: x"},
		{"permission", `{"session_id":"s1","hook_event_name":"PermissionRequest","agent_id":"ag1","agent_type":"Explore","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`, StateWaiting, "zgoda: Bash: rm -rf build"},
		{"stop", `{"session_id":"s1","hook_event_name":"SubagentStop","agent_id":"ag1","agent_type":"Explore","last_assistant_message":"Found 3 uses."}`, StateEnded, "Found 3 uses."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseClaude("pc1", []byte(tt.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if ev.SessionID != "ag1" || ev.ParentID != "s1" || ev.Subagent != "Explore" || ev.State != tt.state || ev.Detail != tt.detail {
				t.Errorf("got %+v", ev)
			}
			if ev.SubagentStarted || ev.SubagentSeen || ev.SubagentStopped || ev.SubagentID != "" {
				t.Errorf("parent-side flags set: %+v", ev)
			}
		})
	}
}
