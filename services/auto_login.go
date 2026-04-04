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
	"context"
	"cursor2api-go/config"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
)

// AutoLoginCursor 使用无头浏览器自动登录 Cursor 并获取 Cookie
func AutoLoginCursor(ctx context.Context, cfg *config.Config) (string, error) {
	if cfg.CursorEmail == "" || cfg.CursorPassword == "" {
		return "", fmt.Errorf("CURSOR_EMAIL and CURSOR_PASSWORD must be configured for auto login")
	}

	logrus.Info("Starting headless browser to login Cursor...")

	// 创建 chromedp 选项
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-web-security", false),
		chromedp.Flag("disable-features", "VizDisplayCompositor"),
		chromedp.UserAgent(cfg.FP.UserAgent),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	// 创建浏览器上下文
	ctx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(logrus.Debugf))
	defer cancel()

	// 设置超时
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var cookies []*http.Cookie

	err := chromedp.Run(ctx,
		// 访问登录页面
		chromedp.Navigate("https://cursor.com/login"),
		chromedp.Sleep(2*time.Second),

		// 输入邮箱
		chromedp.WaitVisible(`input[type="email"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="email"]`, cfg.CursorEmail, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),

		// 点击继续
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),

		// 等待密码输入框出现
		chromedp.WaitVisible(`input[type="password"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[type="password"]`, cfg.CursorPassword, chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),

		// 点击登录
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),

		// 等待登录成功（跳转到主页）
		chromedp.WaitVisible(`div[class*="chat"]`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),

		// 获取 Cookie
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			var netCookies []*network.Cookie
			netCookies, err = network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			for _, nc := range netCookies {
				cookies = append(cookies, &http.Cookie{
					Name:   nc.Name,
					Value:  nc.Value,
					Domain: nc.Domain,
					Path:   nc.Path,
				})
			}
			return nil
		}),
	)

	if err != nil {
		return "", fmt.Errorf("auto login failed: %w", err)
	}

	// 拼接 Cookie 字符串
	var cookieParts []string
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, "_vcrcs") ||
			strings.HasPrefix(c.Name, "generaltranslation") ||
			strings.HasPrefix(c.Name, "muxData") ||
			strings.HasPrefix(c.Name, "WorkosCursorSessionToken") ||
			strings.HasPrefix(c.Name, "workos_id") ||
			strings.HasPrefix(c.Name, "cursor_") {
			cookieParts = append(cookieParts, fmt.Sprintf("%s=%s", c.Name, c.Value))
		}
	}

	if len(cookieParts) == 0 {
		return "", fmt.Errorf("no useful cookies found after login")
	}

	cookieStr := strings.Join(cookieParts, "; ")
	logrus.WithFields(logrus.Fields{
		"cookie_length": len(cookieStr),
		"cookie_count":  len(cookieParts),
	}).Info("Successfully obtained Cursor cookies via headless browser")

	return cookieStr, nil
}
