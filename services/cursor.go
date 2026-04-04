// Copyright (c) 2025-2026 libaxuan
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package services

import (
	"bufio"
	"context"
	"cursor2api-go/config"
	"cursor2api-go/middleware"
	"cursor2api-go/models"
	"cursor2api-go/utils"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"
	"github.com/sirupsen/logrus"
)

const cursorAPIURL = "https://cursor.com/api/chat"

// CursorService handles interactions with Cursor API.
type CursorService struct {
	config          *config.Config
	client          *req.Client
	mainJS          string
	envJS           string
	headerGenerator *utils.HeaderGenerator
	scriptCache     string
	scriptCacheTime time.Time
	scriptMutex     sync.RWMutex
}

// NewCursorService creates a new service instance.
func NewCursorService(cfg *config.Config) *CursorService {
	mainJS, err := os.ReadFile(filepath.Join("jscode", "main.js"))
	if err != nil {
		logrus.Fatalf("failed to read jscode/main.js: %v", err)
	}

	envJS, err := os.ReadFile(filepath.Join("jscode", "env.js"))
	if err != nil {
		logrus.Fatalf("failed to read jscode/env.js: %v", err)
	}

	// Auto login if email/password configured
	if cfg.CursorEmail != "" && cfg.CursorPassword != "" {
		logrus.Info("Auto-login mode: fetching cookies via headless browser...")
		ctx := context.Background()
		cookie, err := AutoLoginCursor(ctx, cfg)
		if err != nil {
			logrus.WithError(err).Warn("Auto login failed, falling back to manual cookie or random token")
		} else {
			cfg.CursorCookie = cookie
			logrus.Info("Auto login successful, using obtained cookies")
		}
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		logrus.Warnf("failed to create cookie jar: %v", err)
	}

	client := req.C()
	client.SetTimeout(time.Duration(cfg.Timeout) * time.Second)
	client.ImpersonateChrome()
	if jar != nil {
		client.SetCookieJar(jar)
	}

	return &CursorService{
		config:          cfg,
		client:          client,
		mainJS:          string(mainJS),
		envJS:           string(envJS),
		headerGenerator: utils.NewHeaderGenerator(),
	}
}

// ChatCompletion creates a chat completion stream for the given request.
func (s *CursorService) ChatCompletion(ctx context.Context, request *models.ChatCompletionRequest) (<-chan interface{}, error) {
	buildResult, err := s.buildCursorRequest(request)
	if err != nil {
		return nil, err
	}

	jsonPayload, err := json.Marshal(buildResult.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cursor payload: %w", err)
	}

	maxRetries := 2
	for attempt := 1; attempt <= maxRetries; attempt++ {
		xIsHuman, err := s.fetchXIsHuman(ctx)
		if err != nil {
			if attempt < maxRetries {
				logrus.WithError(err).Warnf("Failed to fetch x-is-human token (attempt %d/%d), retrying...", attempt, maxRetries)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, err
		}

		headers := s.chatHeaders(xIsHuman)

		logrus.WithFields(logrus.Fields{
			"url":            cursorAPIURL,
			"model":          request.Model,
			"payload_length": len(jsonPayload),
			"attempt":        attempt,
		}).Debug("Sending request to Cursor API")

		// Add browser cookies if configured
		var resp *req.Response
		if s.config.CursorCookie != "" {
			// Parse cookie string and set via cookie jar for proper handling
			req := s.client.R().
				SetContext(ctx).
				SetHeaders(headers).
				SetBody(jsonPayload).
				DisableAutoReadResponse()

			// Parse and add cookies from the cookie string
			for _, pair := range strings.Split(s.config.CursorCookie, ";") {
				pair = strings.TrimSpace(pair)
				if pair == "" {
					continue
				}
				parts := strings.SplitN(pair, "=", 2)
				if len(parts) == 2 {
					req.SetCookies(&http.Cookie{
						Name:   strings.TrimSpace(parts[0]),
						Value:  strings.TrimSpace(parts[1]),
						Path:   "/",
						Domain: "cursor.com",
					})
				}
			}

			resp, err = req.Post(cursorAPIURL)
		} else {
			resp, err = s.client.R().
				SetContext(ctx).
				SetHeaders(headers).
				SetBody(jsonPayload).
				DisableAutoReadResponse().
				Post(cursorAPIURL)
		}
		if err != nil {
			if attempt < maxRetries {
				logrus.WithError(err).Warnf("Cursor request failed (attempt %d/%d), retrying...", attempt, maxRetries)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, fmt.Errorf("cursor request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Response.Body)
			resp.Response.Body.Close()
			message := strings.TrimSpace(string(body))

			logrus.WithFields(logrus.Fields{
				"status_code": resp.StatusCode,
				"response":    message,
				"attempt":     attempt,
			}).Error("Cursor API returned non-OK status")

			if resp.StatusCode == http.StatusForbidden && attempt < maxRetries {
				logrus.Warn("Received 403, refreshing browser fingerprint...")
				s.headerGenerator.Refresh()
				// Clear script cache to force re-fetch
				s.scriptMutex.Lock()
				s.scriptCache = ""
				s.scriptCacheTime = time.Time{}
				s.scriptMutex.Unlock()
				continue
			}

			if strings.Contains(message, "Attention Required! | Cloudflare") {
				message = "Cloudflare 403"
			}
			return nil, middleware.NewCursorWebError(resp.StatusCode, message)
		}

		output := make(chan interface{}, 32)
		go s.consumeSSE(ctx, resp.Response, output, buildResult.ParseConfig)
		return output, nil
	}

	return nil, fmt.Errorf("failed after %d attempts", maxRetries)
}

type nonStreamCollectResult struct {
	Message      models.Message
	FinishReason string
	Usage        models.Usage
	ToolCalls    []models.ToolCall
	Text         string
}

func (s *CursorService) collectNonStream(ctx context.Context, gen <-chan interface{}, _ string) (nonStreamCollectResult, error) {
	var fullContent strings.Builder
	var usage models.Usage
	toolCalls := make([]models.ToolCall, 0, 2)
	finishReason := "stop"

	for {
		select {
		case <-ctx.Done():
			return nonStreamCollectResult{}, ctx.Err()
		case data, ok := <-gen:
			if !ok {
				msg := models.Message{Role: "assistant"}
				if fullContent.Len() > 0 || len(toolCalls) == 0 {
					msg.Content = fullContent.String()
				}
				if len(toolCalls) > 0 {
					msg.ToolCalls = toolCalls
					finishReason = "tool_calls"
				}
				return nonStreamCollectResult{
					Message:      msg,
					FinishReason: finishReason,
					Usage:        usage,
					ToolCalls:    toolCalls,
					Text:         fullContent.String(),
				}, nil
			}

			switch v := data.(type) {
			case models.AssistantEvent:
				switch v.Kind {
				case models.AssistantEventText:
					fullContent.WriteString(v.Text)
				case models.AssistantEventToolCall:
					if v.ToolCall != nil {
						toolCalls = append(toolCalls, *v.ToolCall)
					}
				}
			case string:
				fullContent.WriteString(v)
			case models.Usage:
				usage = v
			case error:
				return nonStreamCollectResult{}, v
			}
		}
	}
}

func (s *CursorService) toolCallRequiredForRequest(request *models.ChatCompletionRequest) (bool, toolChoiceSpec, error) {
	choice, err := parseToolChoice(request.ToolChoice)
	if err != nil {
		return false, toolChoiceSpec{}, err
	}
	if s.config != nil && s.config.KiloToolStrict && len(request.Tools) > 0 && choice.Mode == "auto" {
		choice.Mode = "required"
	}
	if len(request.Tools) == 0 {
		return false, choice, nil
	}
	return choice.Mode == "required" || choice.Mode == "function", choice, nil
}

func (s *CursorService) withToolRetrySystemMessage(request *models.ChatCompletionRequest, choice toolChoiceSpec) *models.ChatCompletionRequest {
	cloned := *request
	cloned.Messages = append([]models.Message(nil), request.Messages...)

	var b strings.Builder
	b.WriteString("TOOL USE REQUIRED.\n")
	b.WriteString("Your next assistant message MUST be a tool call and must contain only the tool call in the exact bridge format. Do not output any natural language.\n")
	if choice.Mode == "function" && strings.TrimSpace(choice.FunctionName) != "" {
		fmt.Fprintf(&b, "You MUST call function %q.\n", strings.TrimSpace(choice.FunctionName))
	} else {
		b.WriteString("You MUST call at least one tool.\n")
	}
	b.WriteString("After receiving the tool result, you will provide the final answer.\n")

	sys := models.Message{Role: "system", Content: b.String()}
	cloned.Messages = append([]models.Message{sys}, cloned.Messages...)
	return &cloned
}

// ChatCompletionNonStream runs a non-stream chat completion.
func (s *CursorService) ChatCompletionNonStream(ctx context.Context, request *models.ChatCompletionRequest) (*models.ChatCompletionResponse, error) {
	required, choice, err := s.toolCallRequiredForRequest(request)
	if err != nil {
		return nil, middleware.NewRequestValidationError(err.Error(), "invalid_tool_choice")
	}

	runOnce := func(req *models.ChatCompletionRequest) (nonStreamCollectResult, error) {
		gen, err := s.ChatCompletion(ctx, req)
		if err != nil {
			return nonStreamCollectResult{}, err
		}
		return s.collectNonStream(ctx, gen, req.Model)
	}

	result, err := runOnce(request)
	if err != nil {
		return nil, err
	}

	if required && len(result.ToolCalls) == 0 {
		retryReq := s.withToolRetrySystemMessage(request, choice)
		retryResult, retryErr := runOnce(retryReq)
		if retryErr == nil {
			result = retryResult
		} else {
			logrus.WithError(retryErr).Warn("tool-required retry failed; returning first attempt")
		}
	}

	respID := utils.GenerateChatCompletionID()
	return models.NewChatCompletionResponse(respID, request.Model, result.Message, result.FinishReason, result.Usage), nil
}

func (s *CursorService) consumeSSE(ctx context.Context, resp *http.Response, output chan interface{}, parseConfig models.CursorParseConfig) {
	defer close(output)
	defer resp.Body.Close()

	parser := utils.NewCursorProtocolParser(parseConfig)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	flushParser := func() {
		for _, event := range parser.Finish() {
			select {
			case output <- event:
			case <-ctx.Done():
				return
			}
		}
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data := utils.ParseSSELine(scanner.Text())
		if data == "" {
			continue
		}

		if data == "[DONE]" {
			flushParser()
			return
		}

		var eventData models.CursorEventData
		if err := json.Unmarshal([]byte(data), &eventData); err != nil {
			logrus.WithError(err).Debugf("Failed to parse SSE data: %s", data)
			continue
		}

		switch eventData.Type {
		case "error":
			if eventData.ErrorText != "" {
				errResp := middleware.NewCursorWebError(http.StatusBadGateway, "cursor API error: "+eventData.ErrorText)
				select {
				case output <- errResp:
				default:
					logrus.WithError(errResp).Warn("failed to push SSE error to channel")
				}
				return
			}
		case "finish":
			flushParser()
			if eventData.MessageMetadata != nil && eventData.MessageMetadata.Usage != nil {
				usage := models.Usage{
					PromptTokens:     eventData.MessageMetadata.Usage.InputTokens,
					CompletionTokens: eventData.MessageMetadata.Usage.OutputTokens,
					TotalTokens:      eventData.MessageMetadata.Usage.TotalTokens,
				}
				select {
				case output <- usage:
				case <-ctx.Done():
					return
				}
			}
			return
		default:
			if eventData.Delta == "" {
				continue
			}
			for _, event := range parser.Feed(eventData.Delta) {
				select {
				case output <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		errResp := middleware.NewCursorWebError(http.StatusBadGateway, err.Error())
		select {
		case output <- errResp:
		default:
			logrus.WithError(err).Warn("failed to push SSE scan error to channel")
		}
	}

	flushParser()
}

func (s *CursorService) fetchXIsHuman(ctx context.Context) (string, error) {
	s.scriptMutex.RLock()
	cached := s.scriptCache
	cachedTime := s.scriptCacheTime
	s.scriptMutex.RUnlock()

	// Cache for 5 minutes
	if cached != "" && time.Since(cachedTime) < 5*time.Minute {
		return cached, nil
	}

	s.scriptMutex.Lock()
	defer s.scriptMutex.Unlock()

	// Double-check after acquiring write lock
	if s.scriptCache != "" && time.Since(s.scriptCacheTime) < 5*time.Minute {
		return s.scriptCache, nil
	}

	cursorJS, err := s.fetchCursorScript(ctx)
	if err != nil {
		// If fetch fails, fall back to random token
		logrus.WithError(err).Warn("Failed to fetch cursor script, using random token")
		token := utils.GenerateRandomString(64)
		s.scriptCache = token
		s.scriptCacheTime = time.Now()
		return token, nil
	}

	jsCode := s.prepareJS(cursorJS)
	result, err := utils.RunJS(jsCode)
	if err != nil {
		logrus.WithError(err).Warn("Failed to execute cursor JS, using random token")
		token := utils.GenerateRandomString(64)
		s.scriptCache = token
		s.scriptCacheTime = time.Now()
		return token, nil
	}

	s.scriptCache = result
	s.scriptCacheTime = time.Now()
	return result, nil
}

func (s *CursorService) fetchCursorScript(ctx context.Context) (string, error) {
	headers := s.scriptHeaders()
	resp, err := s.client.R().
		SetContext(ctx).
		SetHeaders(headers).
		Get(s.config.ScriptURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch cursor script: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cursor script returned %d", resp.StatusCode)
	}
	return resp.String(), nil
}

func (s *CursorService) prepareJS(cursorJS string) string {
	replacer := strings.NewReplacer(
		"$$currentScriptSrc$$", s.config.ScriptURL,
		"$$UNMASKED_VENDOR_WEBGL$$", s.config.FP.UNMASKED_VENDOR_WEBGL,
		"$$UNMASKED_RENDERER_WEBGL$$", s.config.FP.UNMASKED_RENDERER_WEBGL,
		"$$userAgent$$", s.config.FP.UserAgent,
	)

	mainScript := replacer.Replace(s.mainJS)
	mainScript = strings.Replace(mainScript, "$$env_jscode$$", s.envJS, 1)
	mainScript = strings.Replace(mainScript, "$$cursor_jscode$$", cursorJS, 1)
	return mainScript
}

func (s *CursorService) truncateCursorMessages(messages []models.CursorMessage) []models.CursorMessage {
	if len(messages) == 0 || s.config.MaxInputLength <= 0 {
		return messages
	}

	maxLength := s.config.MaxInputLength
	total := 0
	for _, msg := range messages {
		total += cursorMessageTextLength(msg)
	}
	if total <= maxLength {
		return messages
	}

	var result []models.CursorMessage
	startIdx := 0

	// Keep system message
	if len(messages) > 0 && strings.EqualFold(messages[0].Role, "system") {
		result = append(result, messages[0])
		maxLength -= cursorMessageTextLength(messages[0])
		if maxLength < 0 {
			maxLength = 0
		}
		startIdx = 1
	}

	// Collect most recent messages that fit within remaining budget
	current := 0
	collected := make([]models.CursorMessage, 0, len(messages)-startIdx)
	for i := len(messages) - 1; i >= startIdx; i-- {
		msg := messages[i]
		msgLen := cursorMessageTextLength(msg)
		if msgLen == 0 {
			continue
		}
		if current+msgLen > maxLength {
			continue
		}
		collected = append(collected, msg)
		current += msgLen
	}

	// Reverse to restore chronological order
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}

	return append(result, collected...)
}

func cursorMessageTextLength(msg models.CursorMessage) int {
	total := 0
	for _, part := range msg.Parts {
		total += len(part.Text)
	}
	return total
}

func (s *CursorService) chatHeaders(xIsHuman string) map[string]string {
	return s.headerGenerator.GetChatHeaders(xIsHuman)
}

func (s *CursorService) scriptHeaders() map[string]string {
	return s.headerGenerator.GetScriptHeaders()
}
