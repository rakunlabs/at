package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// downloadTelegramFile downloads a file from Telegram to /tmp and returns the local path.
func (s *Server) downloadTelegramFile(bot *tgbotapi.BotAPI, fileID, fileName string) (string, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("get file info: %w", err)
	}

	fileURL := file.Link(bot.Token)

	// Create temp dir
	dir := fmt.Sprintf("/tmp/tg-files/%d", time.Now().UnixNano())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}

	// Sanitize filename
	if fileName == "" {
		fileName = filepath.Base(file.FilePath)
	}
	localPath := filepath.Join(dir, fileName)

	// Download
	resp, err := http.Get(fileURL) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	out, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("save file: %w", err)
	}

	slog.Info("telegram bot: downloaded file", "path", localPath, "size", resp.ContentLength)
	return localPath, nil
}

// transcribeAudio transcribes an audio file using the configured method.
// method: "openai" (cloud API), "local" (openai-whisper), "faster-whisper", "none"
// whisperModel: local model size ("tiny", "base", "small", "medium", "large-v3")
func (s *Server) transcribeAudio(ctx context.Context, audioPath string) string {
	return s.transcribeAudioWithConfig(ctx, audioPath, "openai", "base")
}

func (s *Server) transcribeAudioWithConfig(ctx context.Context, audioPath, method, whisperModel string) string {
	if method == "none" || method == "" {
		method = "openai"
	}
	if whisperModel == "" {
		whisperModel = "base"
	}

	switch method {
	case "local":
		return s.transcribeLocal(ctx, audioPath, whisperModel, "whisper")
	case "faster-whisper":
		return s.transcribeLocal(ctx, audioPath, whisperModel, "faster-whisper")
	default:
		return s.transcribeOpenAI(ctx, audioPath)
	}
}

// transcribeOpenAI uses OpenAI's cloud Whisper API.
func (s *Server) transcribeOpenAI(ctx context.Context, audioPath string) string {
	apiKey := ""
	if s.variableStore != nil {
		v, err := s.variableStore.GetVariableByKey(ctx, "openai_api_key")
		if err == nil && v != nil {
			apiKey = v.Value
		}
	}
	if apiKey == "" {
		slog.Warn("transcribeAudio: no openai_api_key variable configured")
		return ""
	}

	f, err := os.Open(audioPath)
	if err != nil {
		slog.Warn("transcribeAudio: failed to open file", "path", audioPath, "error", err)
		return ""
	}
	defer f.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("model", "whisper-1")
	writer.WriteField("response_format", "text")

	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return ""
	}
	if _, err := io.Copy(part, f); err != nil {
		return ""
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/audio/transcriptions", &body)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("transcribeAudio: API request failed", "error", err)
		return ""
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	if resp.StatusCode != 200 {
		slog.Warn("transcribeAudio: API error", "status", resp.StatusCode, "body", string(respBody[:min(200, len(respBody))]))
		return ""
	}

	return strings.TrimSpace(string(respBody))
}

// transcribeLocal uses locally installed whisper or faster-whisper via uvx.
func (s *Server) transcribeLocal(_ context.Context, audioPath, model, pkg string) string {
	var script string
	if pkg == "faster-whisper" {
		script = fmt.Sprintf(`
from faster_whisper import WhisperModel
model = WhisperModel("%s", device="cpu", compute_type="int8")
segments, info = model.transcribe("%s")
text = " ".join([s.text for s in segments])
print(text.strip())
`, model, audioPath)
	} else {
		script = fmt.Sprintf(`
import whisper
model = whisper.load_model("%s")
result = model.transcribe("%s")
print(result["text"].strip())
`, model, audioPath)
	}

	// Use uvx to run with the package dependency — no install step needed
	uvxPkg := "openai-whisper"
	if pkg == "faster-whisper" {
		uvxPkg = "faster-whisper"
	}

	cmd := exec.CommandContext(context.Background(), "uvx", "--from", uvxPkg, "python3", "-c", script)
	cmd.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1", "UV_SYSTEM_PYTHON=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Fallback: try direct python3 (in case uvx is not available or package is already installed)
		slog.Debug("transcribeLocal: uvx failed, trying direct python3", "error", err)
		cmd2 := exec.CommandContext(context.Background(), "python3", "-c", script)
		cmd2.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1")
		var stdout2, stderr2 bytes.Buffer
		cmd2.Stdout = &stdout2
		cmd2.Stderr = &stderr2
		if err2 := cmd2.Run(); err2 != nil {
			slog.Warn("transcribeLocal: both uvx and direct python3 failed", "pkg", pkg, "model", model, "uvx_err", err, "py_err", err2)
			return ""
		}
		return strings.TrimSpace(stdout2.String())
	}

	return strings.TrimSpace(stdout.String())
}

// sendTelegramText sends a UTF-8 sanitized text message to a Telegram chat.
func sendTelegramText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	text = sanitizeUTF8(text)
	if text == "" {
		text = "(empty)"
	}

	// Convert to MarkdownV2
	md := toTelegramMarkdownV2(text)

	// Telegram limit is 4096 chars
	for len(md) > 0 {
		chunk := md
		if len(chunk) > 4096 {
			cutAt := 4096
			if idx := strings.LastIndex(md[:4096], "\n"); idx > 2000 {
				cutAt = idx + 1
			}
			chunk = md[:cutAt]
			md = md[cutAt:]
		} else {
			md = ""
		}
		reply := tgbotapi.NewMessage(chatID, chunk)
		reply.ParseMode = "MarkdownV2"
		reply.DisableWebPagePreview = true
		if _, err := bot.Send(reply); err != nil {
			// MarkdownV2 failed — fall back to plain text
			slog.Warn("telegram: MarkdownV2 failed, sending plain", "error", err)
			plain := sanitizeUTF8(chunk)
			// Strip MarkdownV2 escapes for plain fallback
			plain = strings.ReplaceAll(plain, "\\", "")
			reply2 := tgbotapi.NewMessage(chatID, plain)
			if _, err2 := bot.Send(reply2); err2 != nil {
				slog.Error("telegram: plain send also failed", "error", err2)
			}
		}
	}
}

// telegramMDV2EscapeChars are characters that must be escaped in MarkdownV2 outside of code blocks.
const telegramMDV2EscapeChars = `_[]()~>#+-=|{}.!`

// escapeMDV2 escapes special characters for Telegram MarkdownV2.
func escapeMDV2(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for _, r := range s {
		if strings.ContainsRune(telegramMDV2EscapeChars, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// toTelegramMarkdownV2 converts standard markdown to Telegram MarkdownV2 format.
func toTelegramMarkdownV2(text string) string {
	// First handle code blocks — content inside must NOT be escaped
	var codeBlocks []string
	placeholder := "\x00CODEBLOCK%d\x00"

	// Extract ```...``` blocks
	for strings.Contains(text, "```") {
		start := strings.Index(text, "```")
		afterStart := text[start+3:]
		// Skip language identifier on same line
		nlIdx := strings.Index(afterStart, "\n")
		var codeContent string
		var endOffset int
		if nlIdx >= 0 {
			afterLang := afterStart[nlIdx+1:]
			endIdx := strings.Index(afterLang, "```")
			if endIdx == -1 {
				break
			}
			codeContent = afterLang[:endIdx]
			endOffset = start + 3 + nlIdx + 1 + endIdx + 3
		} else {
			endIdx := strings.Index(afterStart, "```")
			if endIdx == -1 {
				break
			}
			codeContent = afterStart[:endIdx]
			endOffset = start + 3 + endIdx + 3
		}
		ph := fmt.Sprintf(placeholder, len(codeBlocks))
		codeBlocks = append(codeBlocks, "```\n"+codeContent+"```")
		text = text[:start] + ph + text[endOffset:]
	}

	// Extract inline `code`
	var inlineCode []string
	inlinePH := "\x00INLINE%d\x00"
	for strings.Contains(text, "`") {
		start := strings.Index(text, "`")
		rest := text[start+1:]
		end := strings.Index(rest, "`")
		if end == -1 {
			break
		}
		ph := fmt.Sprintf(inlinePH, len(inlineCode))
		inlineCode = append(inlineCode, "`"+rest[:end]+"`")
		text = text[:start] + ph + text[start+1+end+1:]
	}

	// Now process line by line
	lines := strings.Split(text, "\n")
	var result []string

	for _, line := range lines {
		// Extract bold **text** before escaping
		var bolds []string
		boldPH := "\x00BOLD%d\x00"
		for strings.Contains(line, "**") {
			start := strings.Index(line, "**")
			rest := line[start+2:]
			end := strings.Index(rest, "**")
			if end == -1 {
				break
			}
			ph := fmt.Sprintf(boldPH, len(bolds))
			bolds = append(bolds, rest[:end])
			line = line[:start] + ph + rest[end+2:]
		}

		// Extract italic *text*
		var italics []string
		italicPH := "\x00ITALIC%d\x00"
		for {
			start := -1
			for i := 0; i < len(line); i++ {
				if line[i] == '*' {
					if start == -1 {
						start = i
					} else {
						ph := fmt.Sprintf(italicPH, len(italics))
						italics = append(italics, line[start+1:i])
						line = line[:start] + ph + line[i+1:]
						start = -1
						break
					}
				}
			}
			if start != -1 {
				break
			}
			if start == -1 {
				break
			}
		}

		// Headers → bold
		isHeader := false
		if strings.HasPrefix(line, "### ") {
			line = strings.TrimPrefix(line, "### ")
			isHeader = true
		} else if strings.HasPrefix(line, "## ") {
			line = strings.TrimPrefix(line, "## ")
			isHeader = true
		} else if strings.HasPrefix(line, "# ") {
			line = strings.TrimPrefix(line, "# ")
			isHeader = true
		}

		// Escape the remaining text
		line = escapeMDV2(line)

		// Restore bold
		for i, b := range bolds {
			ph := escapeMDV2(fmt.Sprintf(boldPH, i))
			line = strings.Replace(line, ph, "*"+escapeMDV2(b)+"*", 1)
		}

		// Restore italic
		for i, it := range italics {
			ph := escapeMDV2(fmt.Sprintf(italicPH, i))
			line = strings.Replace(line, ph, "_"+escapeMDV2(it)+"_", 1)
		}

		if isHeader {
			line = "*" + line + "*"
		}

		result = append(result, line)
	}

	text = strings.Join(result, "\n")

	// Restore code blocks (not escaped)
	for i, cb := range codeBlocks {
		ph := escapeMDV2(fmt.Sprintf(placeholder, i))
		text = strings.Replace(text, ph, cb, 1)
	}

	// Restore inline code (not escaped)
	for i, ic := range inlineCode {
		ph := escapeMDV2(fmt.Sprintf(inlinePH, i))
		text = strings.Replace(text, ph, ic, 1)
	}

	return text
}

// sanitizeUTF8 ensures a string is valid UTF-8 by replacing invalid bytes.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	// Replace invalid UTF-8 sequences
	v := make([]rune, 0, len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			_, size := utf8.DecodeRuneInString(s[i:])
			if size == 1 {
				continue // skip invalid byte
			}
		}
		v = append(v, r)
	}
	return string(v)
}

// isFinishedTaskStatus returns true when the task has reached a terminal state.
// Used to decide whether the active task is a candidate for revision-spawning.
func isFinishedTaskStatus(status string) bool {
	switch status {
	case service.TaskStatusDone,
		service.TaskStatusCompleted,
		service.TaskStatusCancelled,
		service.TaskStatusBlocked:
		return true
	}
	return false
}

// isRunningTaskStatus returns true when the task is still in motion.
func isRunningTaskStatus(status string) bool {
	switch status {
	case service.TaskStatusInProgress,
		service.TaskStatusInReview,
		service.TaskStatusReview,
		service.TaskStatusOpen,
		service.TaskStatusTodo,
		service.TaskStatusBacklog:
		return true
	}
	return false
}

// extractMediaFiles scans task result text for video and image file paths.
func extractMediaFiles(result string) (videos []string, images []string) {
	videoExts := map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".avi": true}
	imageExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

	// Try parsing as JSON first — look for "video_file", "file", "image" keys
	var data map[string]any
	if json.Unmarshal([]byte(result), &data) == nil {
		for _, key := range []string{"video_file", "file", "output", "video"} {
			if v, ok := data[key].(string); ok && v != "" {
				ext := strings.ToLower(filepath.Ext(v))
				if videoExts[ext] {
					videos = append(videos, v)
				} else if imageExts[ext] {
					images = append(images, v)
				}
			}
		}
		for _, key := range []string{"image", "image_file", "thumbnail"} {
			if v, ok := data[key].(string); ok && v != "" {
				ext := strings.ToLower(filepath.Ext(v))
				if imageExts[ext] {
					images = append(images, v)
				}
			}
		}
	}

	// Also scan for file paths using regex (catches paths in nested JSON or plain text)
	pathPattern := regexp.MustCompile(`(/[a-zA-Z0-9._\-/]+\.(mp4|mov|webm|avi|png|jpg|jpeg|gif|webp))`)
	matches := pathPattern.FindAllStringSubmatch(result, -1)
	seen := make(map[string]bool)
	for _, v := range videos {
		seen[v] = true
	}
	for _, v := range images {
		seen[v] = true
	}
	for _, m := range matches {
		path := m[1]
		if seen[path] {
			continue
		}
		seen[path] = true
		ext := strings.ToLower(filepath.Ext(path))
		if videoExts[ext] {
			videos = append(videos, path)
		} else if imageExts[ext] {
			images = append(images, path)
		}
	}

	return
}

// newCommandFlagRe matches inline /new flags such as:
//
//	max=50    --max=50    -max=50
//	max_iter=50           --max-iterations=50
//
// Captured groups: [1]=key, [2]=value. Anchored to a word boundary so things
// like "max-altitude=50" inside a topic don't get eaten.
var newCommandFlagRe = regexp.MustCompile(`(?i)(?:^|\s)-{0,2}(max(?:[-_]?iter(?:ations)?)?)=([0-9]+)(?:\s|$)`)

// parseNewCommandArgs splits a /new command argument string into the topic
// and any inline option flags. Recognised flags (case-insensitive):
//
//	max=N | --max=N | -max=N | max_iter=N | max-iterations=N
//
// All matched flag tokens are stripped from the topic so the LLM never sees
// them. Unknown tokens are left untouched in the topic.
func parseNewCommandArgs(args string) (topic string, opts BotTaskOptions) {
	stripped := newCommandFlagRe.ReplaceAllStringFunc(args, func(match string) string {
		sub := newCommandFlagRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		key := strings.ToLower(sub[1])
		val := sub[2]
		switch {
		case strings.HasPrefix(key, "max"):
			// max, max_iter, max-iter, max_iterations, max-iterations all
			// map to MaxIterations.
			if n := atoiSafe(val); n > 0 {
				opts.MaxIterations = n
			}
		}
		// Replace with a single space so surrounding text doesn't glue together.
		return " "
	})
	topic = strings.TrimSpace(strings.Join(strings.Fields(stripped), " "))
	return topic, opts
}

// atoiSafe returns 0 for non-numeric input. Bounded to a sane upper limit
// (10000) so a typo like max=99999999 doesn't blow up the agentic loop budget.
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
		if n > 10000 {
			return 10000
		}
	}
	return n
}

// telegramContext holds per-bot context passed to message handlers.
type telegramContext struct {
	botID           string
	defaultAgentID  string
	chatAgents      map[string]string
	allowedAgentIDs []string
	activeTask      sync.Map // chatID (string) -> task identifier (string)
}

// startTelegramBot starts a Telegram bot that routes messages to the agentic loop.
func (s *Server) startTelegramBot(ctx context.Context, botID string, cfg *config.TelegramBotConfig) {
	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		slog.Error("telegram bot: failed to create bot", "error", err)
		return
	}

	slog.Info("telegram bot started", "user", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := bot.GetUpdatesChan(u)

	tgCtx := &telegramContext{
		botID:           botID,
		defaultAgentID:  cfg.DefaultAgentID,
		chatAgents:      cfg.ChatAgents,
		allowedAgentIDs: cfg.AllowedAgentIDs,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				slog.Info("telegram bot: shutting down")
				bot.StopReceivingUpdates()
				return
			case update := <-updates:
				if update.Message == nil {
					continue
				}

				// Determine agent ID for this chat.
				chatIDStr := fmt.Sprintf("%d", update.Message.Chat.ID)
				agentID := tgCtx.defaultAgentID
				if id, ok := tgCtx.chatAgents[chatIDStr]; ok {
					agentID = id
				}
				if agentID == "" {
					continue
				}

				// Access control check.
				userIDStr := fmt.Sprintf("%d", update.Message.From.ID)
				allowed, wasPending := s.checkBotAccess(ctx, botID, userIDStr, cfg.AccessMode, cfg.PendingApproval, cfg.AllowedUsers)
				if !allowed {
					if wasPending {
						reply := tgbotapi.NewMessage(update.Message.Chat.ID, "Your access is pending approval.")
						bot.Send(reply) //nolint:errcheck
					}
					continue
				}

				go func(msg *tgbotapi.Message) {
					s.handleTelegramMessage(ctx, bot, msg, agentID, tgCtx)
				}(update.Message)
			}
		}
	}()
}

func (s *Server) handleTelegramMessage(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message, agentID string, tgCtx *telegramContext) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)

	sessionID, sessionAgentID, err := s.findOrCreateBotSession(ctx, "telegram", tgCtx.botID, userIDStr, chatIDStr, agentID)
	if err != nil {
		slog.Error("telegram bot: session lookup failed", "error", err)
		return
	}
	// Use the session's actual agent (respects /switch), not the bot's default.
	agentID = sessionAgentID

	// Send typing indicator periodically.
	typingCtx, typingCancel := context.WithCancel(ctx)
	defer typingCancel()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		action := tgbotapi.NewChatAction(msg.Chat.ID, tgbotapi.ChatTyping)
		bot.Send(action) //nolint:errcheck
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				bot.Send(action) //nolint:errcheck
			}
		}
	}()

	content := msg.Text
	if content == "" {
		content = msg.Caption
	}

	// Handle file attachments — download and include path in content
	var attachments []string

	if msg.Document != nil {
		filePath, err := s.downloadTelegramFile(bot, msg.Document.FileID, msg.Document.FileName)
		if err == nil {
			attachments = append(attachments, fmt.Sprintf("[Attached file: %s (%s)]", filePath, msg.Document.FileName))
		} else {
			slog.Warn("telegram bot: failed to download document", "error", err)
		}
	}

	if msg.Photo != nil && len(msg.Photo) > 0 {
		// Get the largest photo
		photo := msg.Photo[len(msg.Photo)-1]
		filePath, err := s.downloadTelegramFile(bot, photo.FileID, "photo.jpg")
		if err == nil {
			attachments = append(attachments, fmt.Sprintf("[Attached image: %s]", filePath))
		} else {
			slog.Warn("telegram bot: failed to download photo", "error", err)
		}
	}

	if msg.Voice != nil {
		filePath, err := s.downloadTelegramFile(bot, msg.Voice.FileID, "voice.ogg")
		if err == nil {
			// Auto-transcribe voice message using configured method
			sttMethod := "openai"
			sttModel := "base"
			if tgCtx.botID != "" && s.botConfigStore != nil {
				if botCfg, err := s.botConfigStore.GetBotConfig(ctx, tgCtx.botID); err == nil && botCfg != nil {
					if botCfg.SpeechToText != "" {
						sttMethod = botCfg.SpeechToText
					}
					if botCfg.WhisperModel != "" {
						sttModel = botCfg.WhisperModel
					}
				}
			}
			transcription := s.transcribeAudioWithConfig(ctx, filePath, sttMethod, sttModel)
			if transcription != "" {
				// Use transcription as the message content
				if content == "" {
					content = transcription
				} else {
					content += "\n" + transcription
				}
				slog.Info("telegram bot: voice transcribed", "duration", msg.Voice.Duration, "text_len", len(transcription))
			} else {
				// Fallback: attach the file path
				attachments = append(attachments, fmt.Sprintf("[Voice message: %s, duration: %ds — transcription failed]", filePath, msg.Voice.Duration))
			}
		}
	}

	if msg.Audio != nil {
		fileName := msg.Audio.FileName
		if fileName == "" {
			fileName = "audio.mp3"
		}
		filePath, err := s.downloadTelegramFile(bot, msg.Audio.FileID, fileName)
		if err == nil {
			attachments = append(attachments, fmt.Sprintf("[Attached audio: %s (%s)]", filePath, fileName))
		}
	}

	if msg.Video != nil {
		fileName := msg.Video.FileName
		if fileName == "" {
			fileName = "video.mp4"
		}
		filePath, err := s.downloadTelegramFile(bot, msg.Video.FileID, fileName)
		if err == nil {
			attachments = append(attachments, fmt.Sprintf("[Attached video: %s (%s)]", filePath, fileName))
		}
	}

	// Append attachment info to content
	if len(attachments) > 0 {
		if content == "" {
			content = "User sent file(s):"
		}
		content += "\n" + strings.Join(attachments, "\n")
	}

	if content == "" {
		return
	}

	// Handle bot commands.
	if msg.IsCommand() {
		slog.Info("telegram bot: command received", "command", msg.Command(), "text", content[:min(len(content), 50)])
		switch msg.Command() {
		case "reset":
			if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
				slog.Error("telegram bot: reset failed", "session_id", sessionID, "error", err)
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Failed to reset session.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Session cleared. Starting fresh!")
			bot.Send(reply) //nolint:errcheck
			return
		case "new":
			// /new <topic> — create an org task and run it in the background.
			//
			// Optional inline flags (anywhere in the args, parsed and stripped):
			//   max=N | --max=N | -max=N | max_iter=N | --max-iterations=N
			// e.g. "/new max=50 build a video about quantum computing"
			rawArgs := strings.TrimSpace(msg.CommandArguments())
			if rawArgs == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID,
					"Usage: /new [max=N] <topic or task>\n"+
						"Example: /new top 5 deadliest animals\n"+
						"Example: /new max=50 build a 30s short about quantum entanglement")
				bot.Send(reply) //nolint:errcheck
				return
			}
			topic, botOpts := parseNewCommandArgs(rawArgs)
			if topic == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /new [max=N] <topic or task>")
				bot.Send(reply) //nolint:errcheck
				return
			}

			// Callback to notify user when task finishes
			chatID := msg.Chat.ID
			onDone := func(ident, status, result string) {
				switch status {
				case "done", "completed":
					sendTelegramText(bot, chatID, fmt.Sprintf("Task %s completed!\nUse /result %s to get the output.", sanitizeUTF8(ident), sanitizeUTF8(ident)))
				case "blocked":
					// Iteration-limit pause is recoverable via /resume. Other
					// blocked reasons (review, manual block) just need the user
					// to inspect the partial output.
					if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
						sendTelegramText(bot, chatID, fmt.Sprintf(
							"Task %s paused at the iteration limit. Partial progress saved.\n\n/resume %s to continue from where it left off, or /result %s to see partial output.",
							sanitizeUTF8(ident), sanitizeUTF8(ident), sanitizeUTF8(ident)))
					} else {
						errMsg := sanitizeUTF8(result)
						if len(errMsg) > 500 {
							errMsg = errMsg[:500] + "..."
						}
						sendTelegramText(bot, chatID, fmt.Sprintf(
							"Task %s blocked.\n\n%s\n\n/resume %s to retry, or /new %s to start over.",
							sanitizeUTF8(ident), errMsg, sanitizeUTF8(ident), sanitizeUTF8(topic)))
					}
				case "failed", "cancelled":
					errMsg := sanitizeUTF8(result)
					if len(errMsg) > 500 {
						errMsg = errMsg[:500] + "..."
					}
					sendTelegramText(bot, chatID, fmt.Sprintf("Task %s failed.\n\n%s\n\nTry again:\n/new %s", sanitizeUTF8(ident), errMsg, sanitizeUTF8(topic)))
				}
			}

			slog.Info("telegram bot: creating task",
				"topic", topic, "agent_id", agentID, "max_iterations", botOpts.MaxIterations)
			taskID, identifier, createErr := s.createBotTaskWithOptions(ctx, agentID, topic, botOpts, onDone)
			if createErr != nil {
				slog.Error("telegram bot: create task failed", "error", createErr)
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Failed to create task: %v", createErr))
				return
			}

			slog.Info("telegram bot: task created", "task_id", taskID, "identifier", identifier)

			// Auto-pick this task as active
			tgCtx.activeTask.Store(chatIDStr, identifier)

			ack := fmt.Sprintf("Task %s created and running in background.\nSet as active task.\n\nTopic: %s\n",
				sanitizeUTF8(identifier), sanitizeUTF8(topic))
			if botOpts.MaxIterations > 0 {
				ack += fmt.Sprintf("Max iterations: %d (per-task override)\n", botOpts.MaxIterations)
			}
			ack += "\nI'll notify you when it's done.\n/status to check"
			sendTelegramText(bot, msg.Chat.ID, ack)
			return
		case "status":
			// /status [identifier] — check task status. If no ID given, show the latest task.
			identifier := strings.TrimSpace(msg.CommandArguments())

			var task *service.Task
			var statusErr error

			if identifier == "" {
				// Use active task if set, otherwise latest
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier = activeID.(string)
				}
			}

			if identifier == "" {
				task, statusErr = s.findLatestTask(ctx, agentID)
				if task == nil && statusErr == nil {
					reply := tgbotapi.NewMessage(msg.Chat.ID, "No tasks found. Create one with /new <topic>")
					bot.Send(reply) //nolint:errcheck
					return
				}
			} else {
				task, statusErr = s.findTaskByIdentifier(ctx, agentID, identifier)
			}
			if statusErr != nil || task == nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Task %s not found.", identifier))
				bot.Send(reply) //nolint:errcheck
				return
			}

			// Send status message
			var statusEmoji string
			switch task.Status {
			case "done", "completed":
				statusEmoji = "✅"
			case "in_progress":
				statusEmoji = "⏳"
			case "cancelled", "blocked":
				statusEmoji = "❌"
			default:
				statusEmoji = "📋"
			}

			taskRef := sanitizeUTF8(task.Identifier)
			if taskRef == "" {
				taskRef = task.ID
			}
			statusMsg := fmt.Sprintf("%s %s\nStatus: %s", statusEmoji, taskRef, task.Status)
			if task.Title != "" {
				statusMsg += fmt.Sprintf("\nTitle: %s", sanitizeUTF8(task.Title))
			}
			if task.Status == "done" || task.Status == "completed" {
				statusMsg += fmt.Sprintf("\n\nUse /result %s to get the output", taskRef)
			}
			sendTelegramText(bot, msg.Chat.ID, statusMsg)
			return
		case "result":
			// /result [identifier] — get the full result + send video/images
			identifier := strings.TrimSpace(msg.CommandArguments())

			var task *service.Task
			var resultErr error

			if identifier == "" {
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier = activeID.(string)
				}
			}
			if identifier == "" {
				task, resultErr = s.findLatestTask(ctx, agentID)
			} else {
				task, resultErr = s.findTaskByIdentifier(ctx, agentID, identifier)
			}
			if resultErr != nil || task == nil {
				sendTelegramText(bot, msg.Chat.ID, "Task not found.")
				return
			}

			if task.Result == "" {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s has no result yet. Status: %s", sanitizeUTF8(task.Identifier), task.Status))
				return
			}

			// Try to send video/image files
			videoFiles, imageFiles := extractMediaFiles(task.Result)

			for _, vf := range videoFiles {
				if _, err := os.Stat(vf); err == nil {
					// Send as document to preserve original quality and aspect ratio
					doc := tgbotapi.NewDocument(msg.Chat.ID, tgbotapi.FilePath(vf))
					doc.Caption = sanitizeUTF8(task.Identifier)
					if _, err := bot.Send(doc); err != nil {
						slog.Warn("telegram bot: send video failed", "file", vf, "error", err)
						sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Video at: %s (too large for Telegram)", vf))
					}
				}
			}

			for _, imgf := range imageFiles {
				if _, err := os.Stat(imgf); err == nil {
					photo := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FilePath(imgf))
					photo.Caption = sanitizeUTF8(task.Identifier)
					bot.Send(photo) //nolint:errcheck
				}
			}

			// Send text result if no media or always
			if len(videoFiles) == 0 && len(imageFiles) == 0 {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Result:\n%s", sanitizeUTF8(task.Result)))
			}
			return
		case "tasks":
			// /tasks — list recent tasks with status
			orgIDs := s.agentOrgIDs(ctx, agentID)
			slog.Info("telegram bot: /tasks", "agent_id", agentID, "org_ids", orgIDs)

			var tasks []service.Task
			var tasksErr error
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("telegram bot: /tasks panic", "recover", r)
						tasksErr = fmt.Errorf("internal error: %v", r)
					}
				}()
				tasks, tasksErr = s.listBotTasks(ctx, agentID)
			}()
			if tasksErr != nil {
				slog.Error("telegram bot: list tasks failed", "agent_id", agentID, "error", tasksErr)
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("⚠️ Failed to list tasks: %v\n\nAgent: %s\nOrgs found: %d", tasksErr, agentID, len(orgIDs)))
				bot.Send(reply) //nolint:errcheck
				return
			}
			if len(tasks) == 0 {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("No tasks found.\n\nAgent: %s\nOrgs: %d\n\nCreate one with /new <topic>", agentID, len(orgIDs)))
				bot.Send(reply) //nolint:errcheck
				return
			}

			slog.Info("telegram bot: tasks found, building message", "count", len(tasks))
			var sb strings.Builder
			sb.WriteString("Recent tasks:\n\n")
			for _, t := range tasks {
				emoji := "[ ]"
				switch t.Status {
				case "done", "completed":
					emoji = "[done]"
				case "in_progress":
					emoji = "[running]"
				case "cancelled", "blocked":
					emoji = "[failed]"
				case "open", "todo":
					emoji = "[open]"
				}
				status := emoji
				title := sanitizeUTF8(t.Title)
				if len(title) > 40 {
					title = title[:40] + "..."
				}
				taskRef := sanitizeUTF8(t.Identifier)
				if taskRef == "" {
					taskRef = sanitizeUTF8(t.ID)
				}
				sb.WriteString(fmt.Sprintf("%s %s - %s\n/status %s\n\n", status, taskRef, title, taskRef))
			}
			sendTelegramText(bot, msg.Chat.ID, sb.String())
			return
		case "current":
			// /current — show the active task
			if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
				taskRef := activeID.(string)
				task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
				if task != nil {
					title := sanitizeUTF8(task.Title)
					if len(title) > 50 {
						title = title[:50] + "..."
					}
					sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Active task: %s\nStatus: %s\nTitle: %s", sanitizeUTF8(taskRef), task.Status, title))
				} else {
					sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Active task: %s (not found in DB)", sanitizeUTF8(taskRef)))
				}
			} else {
				sendTelegramText(bot, msg.Chat.ID, "No active task. Use /pick <id> to select one.")
			}
			return
		case "pick":
			// /pick <identifier> — set the active task for this chat
			target := strings.TrimSpace(msg.CommandArguments())
			if target == "" {
				// No argument — clear active task
				tgCtx.activeTask.Delete(chatIDStr)
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Active task cleared. Normal chat mode.")
				bot.Send(reply) //nolint:errcheck
				return
			}

			task, pickErr := s.findTaskByIdentifier(ctx, agentID, target)
			if pickErr != nil || task == nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Task %s not found. Use /tasks to see available tasks.", target))
				bot.Send(reply) //nolint:errcheck
				return
			}

			taskRef := task.Identifier
			if taskRef == "" {
				taskRef = task.ID[:12]
			}
			tgCtx.activeTask.Store(chatIDStr, taskRef)

			// Clear the chat session so context is fresh for this task
			if err := s.chatSessionStore.DeleteChatMessages(ctx, sessionID); err != nil {
				slog.Warn("telegram bot: pick clear session failed", "error", err)
			}

			title := task.Title
			if len(title) > 50 {
				title = title[:50] + "..."
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("📌 Active task: %s — %s\nStatus: %s\n\nYou can now chat about this task. Messages will include task context.\nUse /pick (no args) to deselect.", taskRef, title, task.Status))
			bot.Send(reply) //nolint:errcheck
			return
		case "login":
			provider := msg.CommandArguments()
			if provider == "" {
				provider = "google"
			}
			loginURL := s.buildOAuthLoginURL(ctx, provider, "telegram", userIDStr)
			if loginURL == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "OAuth login is not available. Make sure external_url is configured and the provider's client_id variable is set.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Click the link below to connect your "+provider+" account:\n"+loginURL)
			bot.Send(reply) //nolint:errcheck
			return
		case "agents":
			agents := s.listAllowedAgents(ctx, tgCtx.botID, tgCtx.allowedAgentIDs)
			if len(agents) == 0 {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Agent switching is not enabled for this bot.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			var sb strings.Builder
			sb.WriteString("Available agents:\n")
			for _, a := range agents {
				desc := a.Config.Description
				if desc != "" {
					sb.WriteString(fmt.Sprintf("• %s - %s\n", a.Name, desc))
				} else {
					sb.WriteString(fmt.Sprintf("• %s\n", a.Name))
				}
			}
			sb.WriteString("\nUsage: /switch <agent name>")
			reply := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
			bot.Send(reply) //nolint:errcheck
			return
		case "switch":
			target := strings.TrimSpace(msg.CommandArguments())
			if target == "" {
				reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /switch <agent name>\nUse /agents to see available agents.")
				bot.Send(reply) //nolint:errcheck
				return
			}
			name, switchErr := s.switchBotAgent(ctx, tgCtx.botID, sessionID, target, tgCtx.allowedAgentIDs)
			if switchErr != nil {
				reply := tgbotapi.NewMessage(msg.Chat.ID, switchErr.Error())
				bot.Send(reply) //nolint:errcheck
				return
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Switched to %s. Session cleared.", name))
			bot.Send(reply) //nolint:errcheck
			return
		case "help":
			helpText := "Available commands:\n" +
				"/new <topic> - Create a background task\n" +
				"/tasks - List recent tasks\n" +
				"/status [id] - Check task status\n" +
				"/result [id] - Get task output + video\n" +
				"/pick <id> - Select task to chat about\n" +
				"/run <instruction> - Run a background subtask on active task\n" +
				"/revise <changes> - Re-run the active (finished) task with changes applied to its original brief\n" +
				"/cancel [id] - Cancel a running task (uses active task if no id)\n" +
				"/resume [id] - Continue a task that hit the iteration limit (uses active task if no id)\n" +
				"/current - Show active task\n" +
				"/reset - Clear conversation history\n" +
				"/login - Connect your Google account (usage: /login or /login google)\n" +
				"/agents - List available agents you can switch to\n" +
				"/switch - Switch to a different agent (usage: /switch <agent name>)\n" +
				"/help - Show this help message"

			// Append any bot-specific custom commands.
			if extra := s.formatTelegramCustomCommandsHelp(ctx, tgCtx.botID); extra != "" {
				helpText += "\n\nCustom commands:\n" + extra
			}
			reply := tgbotapi.NewMessage(msg.Chat.ID, helpText)
			bot.Send(reply) //nolint:errcheck
			return
		case "run":
			// /run <instruction> — create a background subtask under the active task
			instruction := strings.TrimSpace(msg.CommandArguments())
			if instruction == "" {
				sendTelegramText(bot, msg.Chat.ID, "Usage: /run <instruction>\nExample: /run upload to youtube draft")
				return
			}

			activeID, hasActive := tgCtx.activeTask.Load(chatIDStr)
			if !hasActive {
				sendTelegramText(bot, msg.Chat.ID, "No active task. Use /pick <id> to select one first.")
				return
			}

			taskRef := activeID.(string)
			parentTask, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
			if parentTask == nil || parentTask.OrganizationID == "" {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s not found or has no organization.", sanitizeUTF8(taskRef)))
				return
			}

			typingCancel()

			subtaskTitle := instruction
			if len(subtaskTitle) > 100 {
				subtaskTitle = subtaskTitle[:100] + "..."
			}

			description := fmt.Sprintf("Follow-up on %s: %s", taskRef, sanitizeUTF8(parentTask.Title))
			// Include the parent's original brief so the agent has the
			// full requirements when running the follow-up.
			if parentTask.Description != "" {
				origPreview := parentTask.Description
				if len(origPreview) > 2000 {
					origPreview = origPreview[:2000] + "..."
				}
				description += fmt.Sprintf("\n\nOriginal brief:\n%s", sanitizeUTF8(origPreview))
			}
			if parentTask.Result != "" {
				resultPreview := parentTask.Result
				if len(resultPreview) > 1000 {
					resultPreview = resultPreview[:1000] + "..."
				}
				description += fmt.Sprintf("\n\nPrevious result:\n%s", resultPreview)
			}
			description += fmt.Sprintf("\n\nFollow-up instruction:\n%s", sanitizeUTF8(instruction))

			chatID := msg.Chat.ID
			onSubDone := func(ident, status, result string) {
				switch status {
				case "done", "completed":
					if s.taskStore != nil && result != "" {
						// Append to parent result instead of overwriting
						existingResult := ""
						if fresh, err := s.taskStore.GetTask(context.Background(), parentTask.ID); err == nil && fresh != nil {
							existingResult = fresh.Result
						}
						separator := "\n\n---\n"
						newResult := existingResult
						if newResult != "" {
							newResult += separator
						}
						newResult += fmt.Sprintf("[Subtask %s]: %s", ident, result)
						_, _ = s.taskStore.UpdateTask(context.Background(), parentTask.ID, service.Task{
							Result: newResult,
						})
					}
					// Show what was done
					summary := sanitizeUTF8(result)
					if len(summary) > 500 {
						summary = summary[:500] + "..."
					}
					sendTelegramText(bot, chatID, fmt.Sprintf("Done: %s\n\nResult:\n%s\n\n/result %s for full output", sanitizeUTF8(ident), summary, sanitizeUTF8(taskRef)))
				case "blocked":
					if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
						sendTelegramText(bot, chatID, fmt.Sprintf(
							"Subtask %s paused at the iteration limit.\n/resume %s to continue.",
							sanitizeUTF8(ident), sanitizeUTF8(ident)))
					} else {
						errMsg := sanitizeUTF8(result)
						if len(errMsg) > 300 {
							errMsg = errMsg[:300] + "..."
						}
						sendTelegramText(bot, chatID, fmt.Sprintf("Blocked: %s\n%s", sanitizeUTF8(ident), errMsg))
					}
				case "failed", "cancelled":
					errMsg := sanitizeUTF8(result)
					if len(errMsg) > 300 {
						errMsg = errMsg[:300] + "..."
					}
					sendTelegramText(bot, chatID, fmt.Sprintf("Failed: %s\n%s", sanitizeUTF8(ident), errMsg))
				}
			}

			_, subtaskIdent, err := s.createBotSubtask(ctx, parentTask, subtaskTitle, description, onSubDone)
			if err != nil {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Failed: %v", err))
				return
			}

			sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Running: %s\nSubtask: %s\n\nI'll notify you when done.", sanitizeUTF8(instruction), sanitizeUTF8(subtaskIdent)))
			return
		case "revise":
			// /revise <changes> — create a NEW top-level task that is a revision
			// of the active (finished) task. Carries forward the original brief
			// and asks the org to apply the user's changes. Unlike /run, this
			// creates a sibling task, not a subtask, so the new task gets its
			// own clean delegation chain (no shared workspace, no entanglement
			// with the original).
			changes := strings.TrimSpace(msg.CommandArguments())
			if changes == "" {
				sendTelegramText(bot, msg.Chat.ID, "Usage: /revise <changes>\nExample: /revise make it 30 minutes instead of 25, switch ambient to rain")
				return
			}

			activeID, hasActive := tgCtx.activeTask.Load(chatIDStr)
			if !hasActive {
				sendTelegramText(bot, msg.Chat.ID, "No active task. Use /pick <id> to select one first.")
				return
			}

			sourceRef := activeID.(string)
			sourceTask, _ := s.findTaskByIdentifier(ctx, agentID, sourceRef)
			if sourceTask == nil || sourceTask.OrganizationID == "" {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s not found or has no organization.", sanitizeUTF8(sourceRef)))
				return
			}

			if !isFinishedTaskStatus(sourceTask.Status) {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s is still %s. Wait for it to finish (or /cancel it first) before revising.", sanitizeUTF8(sourceRef), sanitizeUTF8(sourceTask.Status)))
				return
			}

			typingCancel()

			// Build a fresh brief that combines the original task's brief with
			// the user's requested changes. We give the head agent everything
			// it needs to produce a brand-new run that's anchored to the same
			// requirements but applies the diff.
			revTitle := fmt.Sprintf("[Revision of %s] %s", sourceRef, changes)
			if len(revTitle) > 100 {
				revTitle = revTitle[:100] + "..."
			}

			var revBriefBuilder strings.Builder
			revBriefBuilder.WriteString(fmt.Sprintf("This is a REVISION of task %s. Produce a NEW deliverable from scratch — do not edit the original task's files in place.\n\n", sanitizeUTF8(sourceRef)))
			revBriefBuilder.WriteString(fmt.Sprintf("Original task title: %s\n\n", sanitizeUTF8(sourceTask.Title)))
			if sourceTask.Description != "" {
				origBrief := sourceTask.Description
				if len(origBrief) > 4000 {
					origBrief = origBrief[:4000] + "..."
				}
				revBriefBuilder.WriteString("Original brief:\n")
				revBriefBuilder.WriteString(sanitizeUTF8(origBrief))
				revBriefBuilder.WriteString("\n\n")
			}
			if sourceTask.Result != "" {
				prevResult := sourceTask.Result
				if len(prevResult) > 2000 {
					prevResult = prevResult[:2000] + "..."
				}
				revBriefBuilder.WriteString("Previous result (for reference; do NOT mutate its files):\n")
				revBriefBuilder.WriteString(sanitizeUTF8(prevResult))
				revBriefBuilder.WriteString("\n\n")
			}
			revBriefBuilder.WriteString("Apply these changes (this is what the user actually wants different this time):\n")
			revBriefBuilder.WriteString(sanitizeUTF8(changes))
			revBriefBuilder.WriteString("\n\nKeep everything from the original brief that the user did NOT ask to change. The new task gets a fresh workspace and runs the full pipeline end-to-end.")
			revDescription := revBriefBuilder.String()

			chatID := msg.Chat.ID
			onRevDone := func(ident, status, result string) {
				switch status {
				case "done", "completed":
					sendTelegramText(bot, chatID, fmt.Sprintf("Revision %s completed.\nUse /result %s to get the output.", sanitizeUTF8(ident), sanitizeUTF8(ident)))
				case "blocked":
					if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
						sendTelegramText(bot, chatID, fmt.Sprintf(
							"Revision %s paused at the iteration limit. Partial progress saved.\n/resume %s to continue.",
							sanitizeUTF8(ident), sanitizeUTF8(ident)))
					} else {
						errMsg := sanitizeUTF8(result)
						if len(errMsg) > 500 {
							errMsg = errMsg[:500] + "..."
						}
						sendTelegramText(bot, chatID, fmt.Sprintf("Revision %s blocked.\n\n%s", sanitizeUTF8(ident), errMsg))
					}
				case "failed", "cancelled":
					errMsg := sanitizeUTF8(result)
					if len(errMsg) > 500 {
						errMsg = errMsg[:500] + "..."
					}
					sendTelegramText(bot, chatID, fmt.Sprintf("Revision %s failed.\n\n%s", sanitizeUTF8(ident), errMsg))
				}
			}

			// Submit the revision as a fresh top-level task into the same org as
			// the source task. The org's head agent picks it up and runs the
			// usual delegation pipeline.
			_, revIdent, revErr := s.createBotOrgTask(ctx, sourceTask.OrganizationID, revTitle, revDescription, 0, onRevDone)
			if revErr != nil {
				slog.Error("telegram bot: revise failed", "source_task", sourceRef, "error", revErr)
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Failed to start revision: %v", revErr))
				return
			}

			// Make the new revision the active task automatically — the user is
			// almost certainly going to /status / /result it next.
			tgCtx.activeTask.Store(chatIDStr, revIdent)

			sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf(
				"Revision %s created and running in background.\nSource: %s\nChanges: %s\n\nNew task is now active. I'll notify you when it's done.\n/status to check",
				sanitizeUTF8(revIdent),
				sanitizeUTF8(sourceRef),
				sanitizeUTF8(changes),
			))
			return
		case "cancel":
			// /cancel [id] — stop the agents running on a task and mark it cancelled.
			// Defaults to the chat's active task when no id is supplied.
			identifier := strings.TrimSpace(msg.CommandArguments())
			if identifier == "" {
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier, _ = activeID.(string)
				}
			}
			if identifier == "" {
				sendTelegramText(bot, msg.Chat.ID, "Usage: /cancel [id]\nNo active task. Use /tasks to find one.")
				return
			}

			task, err := s.findTaskByIdentifier(ctx, agentID, identifier)
			if err != nil || task == nil {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s not found.", sanitizeUTF8(identifier)))
				return
			}

			if !s.cancelDelegation(task.ID) {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s is not running (status: %s).", sanitizeUTF8(identifier), sanitizeUTF8(task.Status)))
				return
			}

			sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Cancel signal sent for %s. The task will stop at the next iteration.", sanitizeUTF8(identifier)))
			return
		case "resume":
			// /resume [id] — re-process a task that was paused at the iteration
			// limit (or any blocked / cancelled task). Conversation state saved
			// by org-delegation as a [CONVERSATION_STATE] system comment is
			// restored automatically inside runOrgDelegation, so the agent
			// picks up exactly where it left off with a fresh iteration budget.
			//
			// Defaults to the chat's active task when no id is supplied.
			identifier := strings.TrimSpace(msg.CommandArguments())
			if identifier == "" {
				if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
					identifier, _ = activeID.(string)
				}
			}
			if identifier == "" {
				sendTelegramText(bot, msg.Chat.ID, "Usage: /resume [id]\nNo active task. Use /tasks to find one, or /pick <id> first.")
				return
			}

			task, err := s.findTaskByIdentifier(ctx, agentID, identifier)
			if err != nil || task == nil {
				sendTelegramText(bot, msg.Chat.ID, fmt.Sprintf("Task %s not found.", sanitizeUTF8(identifier)))
				return
			}

			// Refuse to resume a run that's still in motion — it would race
			// the existing goroutine and corrupt the [CONVERSATION_STATE]
			// comment lifecycle (the live run consumes the comment on load).
			if s.isDelegationActive(task.ID) {
				sendTelegramText(bot, msg.Chat.ID,
					fmt.Sprintf("Task %s is already running. /status %s to check progress.",
						sanitizeUTF8(identifier), sanitizeUTF8(identifier)))
				return
			}

			// Only terminal states make sense to resume. Open / in-progress
			// tasks that aren't actively running indicate a server restart;
			// allow resume in that case too by also accepting Open.
			if !isFinishedTaskStatus(task.Status) && task.Status != service.TaskStatusOpen {
				sendTelegramText(bot, msg.Chat.ID,
					fmt.Sprintf("Task %s status is %s — nothing to resume.",
						sanitizeUTF8(identifier), sanitizeUTF8(task.Status)))
				return
			}

			typingCancel()

			// Make the resumed task active so /status / /result / /run default to it.
			tgCtx.activeTask.Store(chatIDStr, identifier)

			chatID := msg.Chat.ID
			onResumeDone := func(ident, status, result string) {
				switch status {
				case "done", "completed":
					sendTelegramText(bot, chatID,
						fmt.Sprintf("Task %s resumed and completed.\nUse /result %s to get the output.",
							sanitizeUTF8(ident), sanitizeUTF8(ident)))
				case "blocked":
					// Hit the iteration limit a second time — tell the user
					// they can call /resume again.
					if strings.HasPrefix(result, "[ITERATION_LIMIT]") {
						sendTelegramText(bot, chatID,
							fmt.Sprintf("Task %s hit the iteration limit again.\n/resume %s to continue, or /result %s to see partial output.",
								sanitizeUTF8(ident), sanitizeUTF8(ident), sanitizeUTF8(ident)))
					} else {
						errMsg := sanitizeUTF8(result)
						if len(errMsg) > 500 {
							errMsg = errMsg[:500] + "..."
						}
						sendTelegramText(bot, chatID,
							fmt.Sprintf("Task %s blocked.\n\n%s", sanitizeUTF8(ident), errMsg))
					}
				case "failed", "cancelled":
					errMsg := sanitizeUTF8(result)
					if len(errMsg) > 500 {
						errMsg = errMsg[:500] + "..."
					}
					sendTelegramText(bot, chatID,
						fmt.Sprintf("Task %s resume failed.\n\n%s", sanitizeUTF8(ident), errMsg))
				}
			}

			if err := s.resumeBotTask(ctx, task, onResumeDone); err != nil {
				sendTelegramText(bot, msg.Chat.ID,
					fmt.Sprintf("Failed to resume %s: %v", sanitizeUTF8(identifier), err))
				return
			}

			sendTelegramText(bot, msg.Chat.ID,
				fmt.Sprintf("Resuming task %s from saved state. I'll notify you when it's done.\n/status to check.",
					sanitizeUTF8(identifier)))
			return
		default:
			// Check for user-configured custom commands stored on the bot config.
			// Custom commands are dispatched as background tasks (same UX as /new),
			// optionally routed to a specific agent or organization.
			if handled := s.handleTelegramCustomCommand(ctx, bot, msg, tgCtx, chatIDStr, agentID); handled {
				return
			}
			slog.Warn("telegram bot: unknown command, passing to agent", "command", msg.Command())
		}
	}

	// If there's an active task, inject task context into the message (chat mode)
	if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok {
		taskRef := activeID.(string)
		task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
		if task != nil {
			taskContext := fmt.Sprintf("[Active task: %s | Status: %s | Title: %s]", taskRef, task.Status, task.Title)

			// Include the ORIGINAL brief (task.Description) so the agent can remix it
			// for revision requests. Without this, "change this part" loses the
			// original requirements and the agent has to guess.
			if task.Description != "" {
				descPreview := task.Description
				if len(descPreview) > 3000 {
					descPreview = descPreview[:3000] + "..."
				}
				taskContext += fmt.Sprintf("\n[Original brief: %s]", descPreview)
			}

			if task.Result != "" {
				resultPreview := task.Result
				if len(resultPreview) > 4000 {
					resultPreview = resultPreview[:4000] + "..."
				}
				taskContext += fmt.Sprintf("\n[Task result: %s]", resultPreview)

				// Always extract and list media file paths from the FULL result,
				// so the agent can reference them even if the result text was truncated.
				videos, images := extractMediaFiles(task.Result)
				if len(videos) > 0 || len(images) > 0 {
					taskContext += "\n[Available files:"
					for _, v := range videos {
						taskContext += fmt.Sprintf(" %s (video)", v)
					}
					for _, img := range images {
						taskContext += fmt.Sprintf(" %s (image)", img)
					}
					taskContext += "]"
				}
			}

			// Revision guidance — when the active task is finished and the user
			// is asking for a CHANGE (rather than just a question), the agent
			// should spawn a NEW task that combines the original brief with the
			// requested revision, NOT mutate the already-finished task in place.
			//
			// We surface this as a hint; the agent is responsible for detecting
			// revision intent ("change", "make it shorter", "different ambient",
			// "redo with X", "değiştir", "tekrar yap", etc.) and using
			// `org_task_intake` (or `task_create` + `task_process`) with a brief
			// of the form: "REVISION of <ID>. Original brief: <...>. Apply these
			// changes: <user message>. Reuse anything reusable from <ID>'s
			// components if possible." The user will be notified when the new
			// task finishes via the same /status / /result flow.
			if isFinishedTaskStatus(task.Status) {
				taskContext += "\n[Revision policy: this task is FINISHED. If the user asks for a CHANGE / fix / variation / redo, do NOT touch the finished task or its files. Create a NEW task that includes the original brief above PLUS the user's requested changes, using `org_task_intake` (or `task_create` + `task_process`). Title prefix the new task with `[Revision of " + taskRef + "]`. The user can keep chatting and the new task will run in the background.]"
			} else if isRunningTaskStatus(task.Status) {
				taskContext += "\n[This task is currently RUNNING. The user's message is likely a question or a course-correction. Answer questions directly. For course-corrections that require restart, suggest the user wait for completion or use /cancel + a new task. Do NOT spawn another delegation chain on the same task.]"
			}
			content = taskContext + "\n\n" + content

			// Set the task's workspace directory so bash tool handlers (e.g. upload_to_youtube)
			// can find files created by the task's delegation chain.
			if s.taskStore != nil {
				rootID := s.resolveRootTaskID(ctx, task)
				taskWorkDir := filepath.Join(defaultTaskWorkspaceBase, rootID)
				if _, statErr := os.Stat(taskWorkDir); statErr == nil {
					ctx = workflow.ContextWithWorkDir(ctx, taskWorkDir)
				}
			}
		}
	}

	// Prepend user context so agents know the user's identity for triggers/notifications
	userContext := fmt.Sprintf("[User context: platform=telegram, chat_id=%s, user_id=%s, bot_id=%s]\n\n",
		chatIDStr, userIDStr, tgCtx.botID)
	content = userContext + content

	// Set container scope for per-user isolation if bot has user_containers enabled
	if tgCtx.botID != "" && s.botConfigStore != nil {
		botCfg, err := s.botConfigStore.GetBotConfig(ctx, tgCtx.botID)
		if err == nil && botCfg != nil && botCfg.UserContainers {
			ctx = workflow.ContextWithContainerScope(ctx, workflow.ContainerScope{
				UserID: "tg-" + chatIDStr,
			})
		}
	}

	// No active task — normal chat flow
	response, err := s.collectAgenticResponse(ctx, sessionID, content)

	// If the error is about corrupt message history, clear and retry once.
	// RunAgenticLoop emits LLM errors as events (not Go errors), so we must
	// check both the returned error AND the response text for tool-call errors.
	isToolCallError := isToolPairingError(err) ||
		(response != "" && (strings.Contains(response, "tool call result does not follow") ||
			strings.Contains(response, "tool_use content block") ||
			(strings.Contains(response, "tool_result") && strings.Contains(response, "not follow")) ||
			(strings.Contains(response, "tool id") && strings.Contains(response, "not found")) ||
			(strings.Contains(response, "tool_call_id") && strings.Contains(response, "not found"))))
	if isToolCallError {
		slog.Warn("telegram bot: corrupt message history, clearing and retrying", "session_id", sessionID)
		if s.chatSessionStore != nil {
			_ = s.chatSessionStore.DeleteChatMessages(ctx, sessionID)
		}
		response, err = s.collectAgenticResponse(ctx, sessionID, content)
	}

	typingCancel()

	if err != nil {
		slog.Error("telegram bot: agentic loop failed", "session_id", sessionID, "error", err)
		sendTelegramText(bot, msg.Chat.ID, "Sorry, an error occurred. Session has been reset, please try again.")
		if s.chatSessionStore != nil {
			_ = s.chatSessionStore.DeleteChatMessages(ctx, sessionID)
		}
		return
	}

	if response == "" {
		response = "(no response)"
	}

	// If there's an active task and the response has file paths, append to task result
	if activeID, ok := tgCtx.activeTask.Load(chatIDStr); ok && s.taskStore != nil {
		taskRef := activeID.(string)
		task, _ := s.findTaskByIdentifier(ctx, agentID, taskRef)
		if task != nil {
			// Only update if response contains actionable content (file paths, JSON results)
			videos, images := extractMediaFiles(response)
			if len(videos) > 0 || len(images) > 0 {
				// Append — don't overwrite
				newResult := task.Result
				if newResult != "" {
					newResult += "\n\n---\n"
				}
				newResult += fmt.Sprintf("[Chat update]: %s", response)
				_, _ = s.taskStore.UpdateTask(ctx, task.ID, service.Task{
					Result: newResult,
				})
				slog.Info("telegram bot: task result appended", "task", taskRef)
			}
		}
	}

	// Send response to Telegram
	for len(response) > 0 {
		chunk := response
		if len(chunk) > 4096 {
			cutAt := 4096
			if idx := lastIndexBefore(response, '\n', 4096); idx > 0 {
				cutAt = idx + 1
			}
			chunk = response[:cutAt]
			response = response[cutAt:]
		} else {
			response = ""
		}

		reply := tgbotapi.NewMessage(msg.Chat.ID, chunk)
		if _, err := bot.Send(reply); err != nil {
			slog.Error("telegram bot: failed to send message", "error", err)
			return
		}
	}
}
