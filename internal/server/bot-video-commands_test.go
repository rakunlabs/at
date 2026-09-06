package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/rakunlabs/at/internal/service"
)

type videoCommandBotStore struct {
	service.BotConfigStorer
	cfg       service.BotConfig
	err       error
	reads     int
	failAfter int
}

func (m *videoCommandBotStore) GetBotConfig(context.Context, string) (*service.BotConfig, error) {
	m.reads++
	if m.err != nil && (m.failAfter == 0 || m.reads > m.failAfter) {
		return nil, m.err
	}
	cfg := m.cfg
	return &cfg, nil
}

func (m *videoCommandBotStore) CreateBotConfig(_ context.Context, cfg service.BotConfig) (*service.BotConfig, error) {
	m.cfg = cfg
	return &cfg, nil
}

func (m *videoCommandBotStore) UpdateBotConfig(_ context.Context, _ string, cfg service.BotConfig) (*service.BotConfig, error) {
	m.cfg = cfg
	return &cfg, nil
}

func TestBotVideoCommandConfigRoundTrip(t *testing.T) {
	store := &videoCommandBotStore{}
	s := &Server{botConfigStore: store}
	args := map[string]any{
		"platform": "telegram", "token": "test-token", "enabled": false,
		"custom_commands": []any{map[string]any{
			"command": "/video", "video_template_id": "template-1", "organization_id": "org-1",
		}},
	}
	out, err := s.execBotCreate(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "update", "unrelated update"} {
		if operation != "create" {
			update := map[string]any{"id": "bot-1", "name": "renamed"}
			if operation == "update" {
				update["custom_commands"] = args["custom_commands"]
			}
			out, err = s.execBotUpdate(context.Background(), update)
			if err != nil {
				t.Fatal(err)
			}
		}
		var cfg service.BotConfig
		if err := json.Unmarshal([]byte(out), &cfg); err != nil {
			t.Fatal(err)
		}
		if len(cfg.CustomCommands) != 1 || cfg.CustomCommands[0].VideoTemplateID != "template-1" || cfg.CustomCommands[0].Command != "video" {
			t.Fatalf("%s lost binding: %+v", operation, cfg.CustomCommands)
		}
	}
}

func TestValidateBotVideoCommands(t *testing.T) {
	for _, tt := range []struct {
		name, platform, command, template, org string
		valid                                  bool
	}{
		{"bound", "telegram", "video", "template-1", "org-1", true},
		{"slash", "telegram", "/video", "template-1", "org-1", true},
		{"legacy", "discord", "Legacy Command!", "", "", true},
		{"wrong platform", "discord", "video", "template-1", "org-1", false},
		{"empty command", "telegram", "", "template-1", "org-1", false},
		{"invalid command", "telegram", "Video!", "template-1", "org-1", false},
		{"invalid template", "telegram", "video", "../template", "org-1", false},
		{"no org", "telegram", "video", "template-1", "", false},
		{"invalid org", "telegram", "video", "template-1", " org-1 ", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := service.BotConfig{Platform: tt.platform, CustomCommands: []service.BotCustomCommand{{Command: tt.command, VideoTemplateID: tt.template, OrganizationID: tt.org, AgentID: "not-a-fallback"}}}
			if err := validateBotVideoCommands(cfg); (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestBotVideoCommandREST(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, org := range []string{"org-1", ""} {
			t.Run(method+"/"+org, func(t *testing.T) {
				store := &videoCommandBotStore{}
				s := &Server{botConfigStore: store}
				cfg := service.BotConfig{Platform: "telegram", Token: "test", CustomCommands: []service.BotCustomCommand{{Command: "video", VideoTemplateID: "template-1", OrganizationID: org}}}
				body, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(method, "/api/v1/bots/bot-1", strings.NewReader(string(body)))
				req.SetPathValue("id", "bot-1")
				w := httptest.NewRecorder()
				want := http.StatusOK
				if method == http.MethodPost {
					s.CreateBotConfigAPI(w, req)
					want = http.StatusCreated
				} else {
					s.UpdateBotConfigAPI(w, req)
				}
				if org == "" {
					want = http.StatusBadRequest
				}
				if w.Code != want {
					t.Fatalf("status=%d, want %d: %s", w.Code, want, w.Body.String())
				}
				if org != "" && !strings.Contains(w.Body.String(), `"video_template_id":"template-1"`) {
					t.Fatalf("binding missing: %s", w.Body.String())
				}
			})
		}
	}
}

func TestTelegramVideoCommandGuards(t *testing.T) {
	for _, tt := range []struct {
		name, mode, org, text, want string
		users                       []string
		lookupFailure               bool
	}{
		{"empty args without default agent", "allowlist", "org-1", "/video", "Usage: /video <topic>", []string{"42"}, false},
		{"approved pending", "pending", "org-1", "/video", "Usage:", []string{"42"}, false},
		{"public even if listed", "public", "org-1", "/video topic", "explicitly approved", []string{"42"}, false},
		{"unlisted", "allowlist", "org-1", "/video topic", "explicitly approved", []string{"99"}, false},
		{"empty allowlist", "allowlist", "org-1", "/video topic", "explicitly approved", nil, false},
		{"pending unapproved", "pending", "org-1", "/video topic", "explicitly approved", nil, false},
		{"no org does not fall back", "allowlist", "", "/video topic", "requires an organization_id", []string{"42"}, false},
		{"fresh lookup fails closed", "allowlist", "org-1", "/video topic", "explicitly approved", []string{"42"}, true},
		{"template route before org route", "allowlist", "org-1", "/video " + strings.Repeat("x", 2001), "topic must be nonempty", []string{"42"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var messages []string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test"}}`))
					return
				}
				_ = r.ParseForm()
				messages = append(messages, strings.ReplaceAll(r.Form.Get("text"), "\\", ""))
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
			}))
			defer api.Close()
			bot, err := tgbotapi.NewBotAPIWithClient("test", api.URL+"/bot%s/%s", api.Client())
			if err != nil {
				t.Fatal(err)
			}
			store := &videoCommandBotStore{cfg: service.BotConfig{
				Platform: "telegram", AccessMode: tt.mode, AllowedUsers: tt.users,
				CustomCommands: []service.BotCustomCommand{{Command: "video", VideoTemplateID: "template-1", OrganizationID: tt.org, AgentID: "legacy-agent", Brief: "legacy override", TitlePrefix: "legacy title"}},
			}}
			if tt.lookupFailure {
				store.err, store.failAfter = errors.New("database unavailable"), 1
			}
			s := &Server{botConfigStore: store}
			msg := &tgbotapi.Message{MessageID: 7, From: &tgbotapi.User{ID: 42}, Chat: &tgbotapi.Chat{ID: 123}, Text: tt.text, Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}}}
			// With no session/task stores, any fallthrough into generic routing
			// would fail rather than produce the expected guard response.
			s.handleTelegramMessage(context.Background(), bot, msg, "", &telegramContext{botID: "bot-1"})
			if len(messages) != 1 || !strings.Contains(messages[0], tt.want) {
				t.Fatalf("messages=%q, want %q", messages, tt.want)
			}
		})
	}
}

func TestCheckBotAccessRestrictedAndFailure(t *testing.T) {
	for _, tt := range []struct {
		name, mode       string
		users            []string
		err              error
		allowed, pending bool
	}{
		{"generic public", "public", nil, nil, true, false},
		{"approved allowlist", "allowlist", []string{"42"}, nil, true, false},
		{"empty allowlist", "allowlist", nil, nil, false, false},
		{"approved pending", "pending", []string{"42"}, nil, true, false},
		{"new pending", "pending", nil, nil, false, true},
		{"lookup failure", "public", nil, errors.New("unavailable"), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &videoCommandBotStore{cfg: service.BotConfig{AccessMode: tt.mode, AllowedUsers: tt.users}, err: tt.err}
			s := &Server{botConfigStore: store}
			allowed, pending := s.checkBotAccess(context.Background(), "bot-1", "42", "public", false, []string{"42"})
			if allowed != tt.allowed || pending != tt.pending {
				t.Fatalf("got (%v, %v), want (%v, %v)", allowed, pending, tt.allowed, tt.pending)
			}
		})
	}
}
