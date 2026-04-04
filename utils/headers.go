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
	"fmt"
	"math/rand/v2"
	"runtime"
)

// BrowserProfile 浏览器配置文件
type BrowserProfile struct {
	Platform        string
	PlatformVersion string
	Architecture    string
	Bitness         string
	ChromeVersion   int
	UserAgent       string
	Mobile          bool
}

// Chrome 版本范围 (已更新至 146)
var chromeVersions = []int{140, 141, 142, 143, 144, 145, 146}

// Windows 平台配置
var windowsProfiles = []BrowserProfile{
	{Platform: "Windows", PlatformVersion: "10.0.0", Architecture: "x86", Bitness: "64"},
	{Platform: "Windows", PlatformVersion: "11.0.0", Architecture: "x86", Bitness: "64"},
	{Platform: "Windows", PlatformVersion: "15.0.0", Architecture: "x86", Bitness: "64"},
}

// macOS 平台配置
var macosProfiles = []BrowserProfile{
	{Platform: "macOS", PlatformVersion: "13.0.0", Architecture: "arm", Bitness: "64"},
	{Platform: "macOS", PlatformVersion: "14.0.0", Architecture: "arm", Bitness: "64"},
	{Platform: "macOS", PlatformVersion: "15.0.0", Architecture: "arm", Bitness: "64"},
	{Platform: "macOS", PlatformVersion: "15.5.0", Architecture: "arm", Bitness: "64"},
	{Platform: "macOS", PlatformVersion: "13.0.0", Architecture: "x86", Bitness: "64"},
	{Platform: "macOS", PlatformVersion: "14.0.0", Architecture: "x86", Bitness: "64"},
}

// Linux 平台配置
var linuxProfiles = []BrowserProfile{
	{Platform: "Linux", PlatformVersion: "", Architecture: "x86", Bitness: "64"},
}

// HeaderGenerator 动态 header 生成器
type HeaderGenerator struct {
	profile       BrowserProfile
	chromeVersion int
}

// NewHeaderGenerator 创建新的 header 生成器
func NewHeaderGenerator() *HeaderGenerator {
	g := &HeaderGenerator{}
	g.refresh()
	return g
}

func (g *HeaderGenerator) refresh() {
	var profiles []BrowserProfile
	switch runtime.GOOS {
	case "darwin":
		profiles = macosProfiles
	case "linux":
		profiles = linuxProfiles
	default:
		profiles = windowsProfiles
	}

	profile := profiles[rand.IntN(len(profiles))]
	chromeVersion := chromeVersions[rand.IntN(len(chromeVersions))]
	profile.ChromeVersion = chromeVersion
	profile.UserAgent = generateUserAgent(profile)

	g.profile = profile
	g.chromeVersion = chromeVersion
}

// generateUserAgent 生成 User-Agent 字符串
func generateUserAgent(profile BrowserProfile) string {
	ua := "Mozilla/5.0 ("
	switch profile.Platform {
	case "Windows":
		ua += "Windows NT 10.0; Win64; x64"
	case "macOS":
		ua += "Macintosh; Intel Mac OS X 10_15_7"
	case "Linux":
		ua += "X11; Linux x86_64"
	default:
		ua += "Windows NT 10.0; Win64; x64"
	}
	return fmt.Sprintf("%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36",
		ua, profile.ChromeVersion)
}

// GetChatHeaders 获取聊天请求的 headers (匹配真实浏览器 curl)
func (g *HeaderGenerator) GetChatHeaders(xIsHuman string) map[string]string {
	langs := []string{"zh-CN,zh;q=0.9", "en-US,en;q=0.9", "en-GB,en;q=0.9"}
	refs := []string{
		"https://cursor.com/en-US/learn/how-ai-models-work",
		"https://cursor.com/",
	}

	headers := map[string]string{
		"accept":                     "*/*",
		"accept-language":            langs[rand.IntN(len(langs))],
		"content-type":               "application/json",
		"origin":                     "https://cursor.com",
		"priority":                   "u=1, i",
		"referer":                    refs[rand.IntN(len(refs))],
		"sec-ch-ua":                  fmt.Sprintf(`"Chromium";v="%d", "Not-A.Brand";v="24", "Google Chrome";v="%d"`, g.chromeVersion, g.chromeVersion),
		"sec-ch-ua-arch":             fmt.Sprintf(`"%s"`, g.profile.Architecture),
		"sec-ch-ua-bitness":          fmt.Sprintf(`"%s"`, g.profile.Bitness),
		"sec-ch-ua-mobile":           "?0",
		"sec-ch-ua-platform":         fmt.Sprintf(`"%s"`, g.profile.Platform),
		"sec-ch-ua-platform-version": fmt.Sprintf(`"%s"`, g.profile.PlatformVersion),
		"sec-fetch-dest":             "empty",
		"sec-fetch-mode":             "cors",
		"sec-fetch-site":             "same-origin",
		"user-agent":                 g.profile.UserAgent,
		"x-is-human":                 xIsHuman,
	}

	return headers
}

// GetScriptHeaders 获取脚本请求的 headers
func (g *HeaderGenerator) GetScriptHeaders() map[string]string {
	langs := []string{"zh-CN,zh;q=0.9", "en-US,en;q=0.9", "en-GB,en;q=0.9"}
	refs := []string{
		"https://cursor.com/cn/learn/how-ai-models-work",
		"https://cursor.com/en-US/learn/how-ai-models-work",
		"https://cursor.com/",
	}

	headers := map[string]string{
		"accept":                     "*/*",
		"accept-language":            langs[rand.IntN(len(langs))],
		"referer":                    refs[rand.IntN(len(refs))],
		"sec-ch-ua":                  fmt.Sprintf(`"Chromium";v="%d", "Not-A.Brand";v="24", "Google Chrome";v="%d"`, g.chromeVersion, g.chromeVersion),
		"sec-ch-ua-arch":             fmt.Sprintf(`"%s"`, g.profile.Architecture),
		"sec-ch-ua-bitness":          fmt.Sprintf(`"%s"`, g.profile.Bitness),
		"sec-ch-ua-mobile":           "?0",
		"sec-ch-ua-platform":         fmt.Sprintf(`"%s"`, g.profile.Platform),
		"sec-ch-ua-platform-version": fmt.Sprintf(`"%s"`, g.profile.PlatformVersion),
		"sec-fetch-dest":             "script",
		"sec-fetch-mode":             "no-cors",
		"sec-fetch-site":             "same-origin",
		"user-agent":                 g.profile.UserAgent,
	}

	return headers
}

// GetUserAgent 获取 User-Agent
func (g *HeaderGenerator) GetUserAgent() string {
	return g.profile.UserAgent
}

// GetProfile 获取浏览器配置文件
func (g *HeaderGenerator) GetProfile() BrowserProfile {
	return g.profile
}

// Refresh 刷新配置文件（生成新的随机配置）
func (g *HeaderGenerator) Refresh() {
	g.refresh()
}
