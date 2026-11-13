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
		{"permission", `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Edit","tool_input":{"file_path":"/a/b.go"}}`, StateWaiting, "", "zgoda: Edit: /a/b.go"},
		{"permission prompt", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs permission"}`, StateWaiting, "", "Claude needs permission"},
		{"idle prompt", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"idle_prompt","message":"waiting"}`, StateIdle, "", ""},
		{"other notification", `{"session_id":"s1","hook_event_name":"Notification","notification_type":"auth_success"}`, "", "", ""},
		{"stop", `{"session_id":"s1","hook_event_name":"Stop","last_assistant_message":"Gotowe."}`, StateIdle, "", "Gotowe."},
		{"stop failure", `{"session_id":"s1","hook_event_name":"StopFailure","message":"rate limit"}`, StateError, "", "rate limit"},
		{"end", `{"session_id":"s1","hook_event_name":"SessionEnd","reason":"other"}`, StateEnded, "", ""},
		{"unknown", `{"session_id":"s1","hook_event_name":"PostCompact"}`, "", "", ""},
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
