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
		wantID     string
		wantState  State
		wantTask   string
		wantDetail string
	}{
		{"created", `{"host":"mac","directory":"/src/app","type":"session.created","properties":{"info":{"id":"o1","title":"New session","directory":"/src/app"}}}`, "o1", StateIdle, "New session", ""},
		{"updated title", `{"type":"session.updated","properties":{"info":{"id":"o1","title":"Refactor auth"}}}`, "o1", "", "Refactor auth", ""},
		{"busy", `{"type":"session.status","properties":{"sessionID":"o1","status":{"type":"busy"}}}`, "o1", StateWorking, "", ""},
		{"retry", `{"type":"session.status","properties":{"sessionID":"o1","status":{"type":"retry"}}}`, "o1", StateWorking, "", "ponawia"},
		{"idle status", `{"type":"session.status","properties":{"sessionID":"o1","status":{"type":"idle"}}}`, "o1", StateIdle, "", ""},
		{"idle", `{"type":"session.idle","properties":{"sessionID":"o1"}}`, "o1", StateIdle, "", ""},
		{"error", `{"type":"session.error","properties":{"sessionID":"o1","error":{"name":"APIError","data":{"message":"overloaded"}}}}`, "o1", StateError, "", "overloaded"},
		{"permission asked", `{"type":"permission.asked","properties":{"sessionID":"o1","permission":"bash"}}`, "o1", StateWaiting, "", "zgoda: bash"},
		{"permission replied", `{"type":"permission.replied","properties":{"sessionID":"o1"}}`, "o1", StateWorking, "", ""},
		{"deleted", `{"type":"session.deleted","properties":{"info":{"id":"o1"}}}`, "o1", StateEnded, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseOpenCode("10.0.0.5", []byte(tt.body), at)
			if err != nil {
				t.Fatal(err)
			}
			if ev.SessionID != tt.wantID || ev.State != tt.wantState || ev.Task != tt.wantTask || ev.Detail != tt.wantDetail {
				t.Errorf("got id=%q state=%q task=%q detail=%q", ev.SessionID, ev.State, ev.Task, ev.Detail)
			}
		})
	}
}

func TestParseOpenCodeHost(t *testing.T) {
	ev, _ := ParseOpenCode("10.0.0.5", []byte(`{"type":"session.idle","properties":{"sessionID":"o1"}}`), time.Now())
	if ev.Host != "10.0.0.5" {
		t.Errorf("fallback host = %q", ev.Host)
	}
	ev, _ = ParseOpenCode("10.0.0.5", []byte(`{"host":"mac","type":"session.idle","properties":{"sessionID":"o1"}}`), time.Now())
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
