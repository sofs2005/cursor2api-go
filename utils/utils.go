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

package utils

import (
	"crypto/rand"
	"cursor2api-go/middleware"
	"cursor2api-go/models"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// jsonBufferPool reuses byte slices for JSON marshaling to reduce GC pressure.
var jsonBufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 4096)
		return &b
	},
}

func getJSONBuffer() *[]byte {
	return jsonBufferPool.Get().(*[]byte)
}

func putJSONBuffer(b *[]byte) {
	*b = (*b)[:0]
	jsonBufferPool.Put(b)
}

// GenerateRandomString 生成指定长度的随机字符串
func GenerateRandomString(length int) string {
	if length <= 0 {
		return ""
	}
	byteLen := (length + 1) / 2
	bytes := make([]byte, byteLen)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback: use timestamp-based string
		fallback := fmt.Sprintf("%d", time.Now().UnixNano())
		if len(fallback) >= length {
			return fallback[:length]
		}
		return fallback + strings.Repeat("0", length-len(fallback))
	}
	encoded := hex.EncodeToString(bytes)
	if len(encoded) >= length {
		return encoded[:length]
	}
	return encoded + GenerateRandomString(length-len(encoded))
}

// GenerateChatCompletionID 生成聊天完成ID
func GenerateChatCompletionID() string {
	return "chatcmpl-" + GenerateRandomString(29)
}

// ParseSSELine 解析SSE数据行
func ParseSSELine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "data: ") {
		return strings.TrimSpace(line[6:])
	}
	return ""
}

// WriteSSEEvent 写入SSE事件
func WriteSSEEvent(w http.ResponseWriter, event, data string) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// writeJSONEvent writes a JSON SSE event using a pooled buffer.
func writeJSONEvent(w http.ResponseWriter, v interface{}) error {
	buf := getJSONBuffer()
	defer putJSONBuffer(buf)

	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

// StreamChatCompletion 处理流式聊天完成
func StreamChatCompletion(c *gin.Context, chatGenerator <-chan interface{}, modelName string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("X-Accel-Buffering", "no") // disable nginx buffering

	responseID := GenerateChatCompletionID()
	started := false
	toolCallIndex := 0

	writeChunk := func(delta models.StreamDelta, finishReason *string) {
		streamResp := models.NewChatCompletionStreamResponse(responseID, modelName, delta, finishReason)
		if err := writeJSONEvent(c.Writer, streamResp); err != nil {
			logrus.WithError(err).Debug("Failed to write SSE event")
		}
	}

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			logrus.Debug("Client disconnected during streaming")
			return
		case data, ok := <-chatGenerator:
			if !ok {
				reason := "stop"
				if toolCallIndex > 0 {
					reason = "tool_calls"
				}
				writeChunk(models.StreamDelta{}, stringPtr(reason))
				WriteSSEEvent(c.Writer, "", "[DONE]")
				return
			}

			switch v := data.(type) {
			case models.AssistantEvent:
				if !started {
					writeChunk(models.StreamDelta{Role: "assistant"}, nil)
					started = true
				}
				switch v.Kind {
				case models.AssistantEventText:
					if v.Text != "" {
						writeChunk(models.StreamDelta{Content: v.Text}, nil)
					}
				case models.AssistantEventToolCall:
					if v.ToolCall != nil {
						writeChunk(models.StreamDelta{
							ToolCalls: []models.ToolCallDelta{{
								Index: toolCallIndex,
								ID:    v.ToolCall.ID,
								Type:  v.ToolCall.Type,
								Function: &models.FunctionCallDelta{
									Name:      v.ToolCall.Function.Name,
									Arguments: v.ToolCall.Function.Arguments,
								},
							}},
						}, nil)
						toolCallIndex++
					}
				}
			case string:
				if !started {
					writeChunk(models.StreamDelta{Role: "assistant"}, nil)
					started = true
				}
				if v != "" {
					writeChunk(models.StreamDelta{Content: v}, nil)
				}
			case error:
				logrus.WithError(v).Error("Stream generator error")
				WriteSSEEvent(c.Writer, "", "[DONE]")
				return
			}
		}
	}
}

// SafeStreamWrapper 安全流式包装器
func SafeStreamWrapper(handler func(*gin.Context, <-chan interface{}, string), c *gin.Context, chatGenerator <-chan interface{}, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			logrus.WithField("panic", r).Error("Panic in stream handler")
			if !c.Writer.Written() {
				c.JSON(http.StatusInternalServerError, models.NewErrorResponse(
					"Internal server error", "panic_error", "",
				))
			}
		}
	}()

	firstItem, ok := <-chatGenerator
	if !ok {
		middleware.HandleError(c, middleware.NewCursorWebError(http.StatusInternalServerError, "empty stream"))
		return
	}
	if err, isErr := firstItem.(error); isErr {
		middleware.HandleError(c, err)
		return
	}

	buffered := make(chan interface{}, 1)
	buffered <- firstItem
	ctx := c.Request.Context()

	go func() {
		defer close(buffered)
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-chatGenerator:
				if !ok {
					return
				}
				select {
				case buffered <- item:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	handler(c, buffered, modelName)
}

// RunJS 执行JavaScript代码并返回标准输出内容
func RunJS(jsCode string) (string, error) {
	finalJS := `const crypto = require('crypto').webcrypto;
global.crypto = crypto;
globalThis.crypto = crypto;
if (typeof window === 'undefined') { global.window = global; }
window.crypto = crypto;
this.crypto = crypto;
` + jsCode

	cmd := exec.Command("node")
	cmd.Stdin = strings.NewReader(finalJS)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("node.js execution failed (exit code: %d)\nSTDOUT:\n%s\nSTDERR:\n%s",
				exitErr.ExitCode(), string(output), string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to execute node.js: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// stringPtr 返回字符串指针
func stringPtr(s string) *string { return &s }
