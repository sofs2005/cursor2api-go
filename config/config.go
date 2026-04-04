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

package config

import (
	"cursor2api-go/models"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// Environment variable names
const (
	EnvPort               = "PORT"
	EnvDebug              = "DEBUG"
	EnvAPIKey             = "API_KEY"
	EnvModels             = "MODELS"
	EnvSystemPromptInject = "SYSTEM_PROMPT_INJECT"
	EnvTimeout            = "TIMEOUT"
	EnvMaxInputLength     = "MAX_INPUT_LENGTH"
	EnvKiloToolStrict     = "KILO_TOOL_STRICT"
	EnvScriptURL          = "SCRIPT_URL"
	EnvCursorCookie       = "CURSOR_COOKIE"
	EnvCursorEmail        = "CURSOR_EMAIL"
	EnvCursorPassword     = "CURSOR_PASSWORD"
	EnvUserAgent          = "USER_AGENT"
	EnvVendorWebGL        = "UNMASKED_VENDOR_WEBGL"
	EnvRendererWebGL      = "UNMASKED_RENDERER_WEBGL"
)

// Default values
const (
	DefaultPort           = 8002
	DefaultDebug          = false
	DefaultAPIKey         = "0000"
	DefaultModels         = "gemini-3-flash"
	DefaultTimeout        = 120
	DefaultMaxInputLength = 200000
	DefaultKiloToolStrict = false
	DefaultScriptURL      = "https://cursor.com/_next/static/chunks/pages/_app.js"
	DefaultCursorCookie   = ""
	DefaultCursorEmail    = ""
	DefaultCursorPassword = ""
	DefaultUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	DefaultVendorWebGL    = "Google Inc. (Intel)"
	DefaultRendererWebGL  = "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0, D3D11)"
)

// Config 应用程序配置结构
type Config struct {
	// 服务器配置
	Port  int  `json:"port"`
	Debug bool `json:"debug"`

	// API配置
	APIKey             string `json:"-"` // never serialize API key
	Models             string `json:"models"`
	SystemPromptInject string `json:"system_prompt_inject"`
	Timeout            int    `json:"timeout"`
	MaxInputLength     int    `json:"max_input_length"`

	// 兼容性配置
	// KILO_TOOL_STRICT=true: when the client provides tools, force at least one tool call
	// to adapt to orchestrators like Kilo Code that require tool usage.
	KiloToolStrict bool `json:"kilo_tool_strict"`

	// Cursor相关配置
	ScriptURL      string `json:"script_url"`
	CursorCookie   string `json:"-"` // never serialize cookie
	CursorEmail    string `json:"-"` // never serialize email
	CursorPassword string `json:"-"` // never serialize password
	FP             FP     `json:"fp"`
}

// FP 指纹配置结构
type FP struct {
	UserAgent               string `json:"userAgent"`
	UNMASKED_VENDOR_WEBGL   string `json:"unmaskedVendorWebgl"`
	UNMASKED_RENDERER_WEBGL string `json:"unmaskedRendererWebgl"`
}

// LoadConfig 加载配置
func LoadConfig() (*Config, error) {
	// 尝试加载.env文件
	_ = godotenv.Load() // ignore error — env vars are fallback

	config := &Config{
		Port:               getEnvAsInt(EnvPort, DefaultPort),
		Debug:              getEnvAsBool(EnvDebug, DefaultDebug),
		APIKey:             getEnv(EnvAPIKey, DefaultAPIKey),
		Models:             getEnv(EnvModels, DefaultModels),
		SystemPromptInject: getEnv(EnvSystemPromptInject, ""),
		Timeout:            getEnvAsInt(EnvTimeout, DefaultTimeout),
		MaxInputLength:     getEnvAsInt(EnvMaxInputLength, DefaultMaxInputLength),
		KiloToolStrict:     getEnvAsBool(EnvKiloToolStrict, DefaultKiloToolStrict),
		ScriptURL:          getEnv(EnvScriptURL, DefaultScriptURL),
		CursorCookie:       getEnv(EnvCursorCookie, DefaultCursorCookie),
		CursorEmail:        getEnv(EnvCursorEmail, DefaultCursorEmail),
		CursorPassword:     getEnv(EnvCursorPassword, DefaultCursorPassword),
		FP: FP{
			UserAgent:               getEnv(EnvUserAgent, DefaultUserAgent),
			UNMASKED_VENDOR_WEBGL:   getEnv(EnvVendorWebGL, DefaultVendorWebGL),
			UNMASKED_RENDERER_WEBGL: getEnv(EnvRendererWebGL, DefaultRendererWebGL),
		},
	}

	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// validate 验证配置
func (c *Config) validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("invalid port: %d (must be 1-65535)", c.Port)
	}
	if c.APIKey == "" {
		return fmt.Errorf("API_KEY is required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("TIMEOUT must be positive, got %d", c.Timeout)
	}
	if c.MaxInputLength <= 0 {
		return fmt.Errorf("MAX_INPUT_LENGTH must be positive, got %d", c.MaxInputLength)
	}
	return nil
}

// GetBaseModels 获取基础模型列表
func (c *Config) GetBaseModels() []string {
	modelsList := strings.Split(c.Models, ",")
	result := make([]string, 0, len(modelsList))
	for _, model := range modelsList {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// GetModels 获取模型列表（自动展开 -thinking 变体）
func (c *Config) GetModels() []string {
	return models.ExpandModelList(c.GetBaseModels())
}

// IsValidModel 检查模型是否有效
func (c *Config) IsValidModel(model string) bool {
	validModels := c.GetModels()
	for _, validModel := range validModels {
		if validModel == model {
			return true
		}
	}
	return false
}

// MaskedAPIKey 返回掩码后的 API 密钥
func (c *Config) MaskedAPIKey() string {
	if len(c.APIKey) <= 4 {
		return "****"
	}
	return c.APIKey[:4] + "****"
}

// ToJSON 将配置序列化为JSON（用于调试）
func (c *Config) ToJSON() string {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error marshaling config: %v", err)
	}
	return string(data)
}

// --- helpers ---

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		logrus.Warnf("Invalid integer value for %s: %q, using default: %d", key, valueStr, defaultValue)
		return defaultValue
	}
	return value
}

func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		logrus.Warnf("Invalid boolean value for %s: %q, using default: %t", key, valueStr, defaultValue)
		return defaultValue
	}
	return value
}
