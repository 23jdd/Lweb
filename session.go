package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const sessionContextKey = "__session__"

// SessionOptions 定义会话与 Cookie 配置
type SessionOptions struct {
	CookieName string // cookie name
	MaxAge     time.Duration // max age
	Path       string // path
	HttpOnly   bool // http only
	Secure     bool // secure
	SameSite   http.SameSite // same site
}

// DefaultSessionOptions 返回默认会话配置
func DefaultSessionOptions() SessionOptions {
	return SessionOptions{
		CookieName: "sid",
		MaxAge:     24 * time.Hour,
		Path:       "/",
		HttpOnly:   true,
		Secure:     false,
		SameSite:   http.SameSiteLaxMode,
	}
}

type sessionRecord struct {
	values    map[string]any
	expiresAt time.Time
}

// SessionManager 负责会话创建、读取、持久化（内存版）
type SessionManager struct {
	opts       SessionOptions
	mu         sync.RWMutex
	data       map[string]*sessionRecord
	stopJanitor chan struct{}
	stopOnce   sync.Once
}

// Session 保存当前请求对应的会话信息
type Session struct {
	id       string
	values   map[string]any
	isNew    bool
	dirty    bool
	destroy  bool
	manager  *SessionManager
	expires  time.Time
}

// NewSessionManager 创建一个内存版会话管理器
func NewSessionManager(opts SessionOptions) *SessionManager {
	if opts.CookieName == "" {
		opts.CookieName = "sid"
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = 24 * time.Hour
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	m := &SessionManager{
		opts: opts,
		data: make(map[string]*sessionRecord),
		stopJanitor: make(chan struct{}),
	}
	m.startJanitor(5 * time.Minute)
	return m
}

// Session 中间件：为每个请求注入 Session，并在请求结束后保存
func (m *SessionManager) Session() Handler {
	return func(c *Context) {
		s := m.loadOrCreate(c)
		c.Set(sessionContextKey, s)

		// 在进入业务前先写 Cookie，避免业务提前写入响应头导致 Cookie 丢失。
		m.writeCookie(c, s.id, int(m.opts.MaxAge.Seconds()))
		c.Next()

		if s.destroy {
			m.delete(s.id)
			m.writeCookie(c, "", -1)
			return
		}
		if s.dirty || s.isNew {
			m.save(s)
		}
	}
}

// GetSession 从 Context 获取当前请求 Session
func GetSession(c *Context) *Session {
	v, ok := c.Get(sessionContextKey)
	if !ok {
		return nil
	}
	s, ok := v.(*Session)
	if !ok {
		return nil
	}
	return s
}

// ID 返回会话 ID
func (s *Session) ID() string {
	return s.id
}

// Get 获取会话值
func (s *Session) Get(key string) (any, bool) {
	v, ok := s.values[key]
	return v, ok
}

// Set 设置会话值
func (s *Session) Set(key string, value any) {
	s.values[key] = value
	s.dirty = true
}

// Delete 删除指定会话键
func (s *Session) Delete(key string) {
	delete(s.values, key)
	s.dirty = true
}

// Clear 清空会话数据
func (s *Session) Clear() {
	for k := range s.values {
		delete(s.values, k)
	}
	s.dirty = true
}

// Destroy 销毁会话（请求结束后会清理服务端数据与浏览器 Cookie）
func (s *Session) Destroy() {
	s.destroy = true
}

func (m *SessionManager) loadOrCreate(c *Context) *Session {
	if cookie, err := c.Req.Cookie(m.opts.CookieName); err == nil && cookie.Value != "" {
		if rec, ok := m.get(cookie.Value); ok {
			return &Session{
				id:      cookie.Value,
				values:  cloneMap(rec.values),
				isNew:   false,
				manager: m,
				expires: rec.expiresAt,
			}
		}
	}
	return m.newSession()
}

func (m *SessionManager) newSession() *Session {
	id := generateSessionID()
	return &Session{
		id:      id,
		values:  make(map[string]any),
		isNew:   true,
		dirty:   true,
		manager: m,
		expires: time.Now().Add(m.opts.MaxAge),
	}
}

func (m *SessionManager) save(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[s.id] = &sessionRecord{
		values:    cloneMap(s.values),
		expiresAt: time.Now().Add(m.opts.MaxAge),
	}
}

func (m *SessionManager) get(id string) (*sessionRecord, bool) {
	m.mu.RLock()
	rec, ok := m.data[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(rec.expiresAt) {
		m.delete(id)
		return nil, false
	}
	return rec, true
}

func (m *SessionManager) delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
}

// Close 停止后台清理协程，适合在服务退出时调用
func (m *SessionManager) Close() {
	m.stopOnce.Do(func() {
		close(m.stopJanitor)
	})
}

func (m *SessionManager) startJanitor(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.cleanupExpired()
			case <-m.stopJanitor:
				return
			}
		}
	}()
}

func (m *SessionManager) cleanupExpired() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rec := range m.data {
		if now.After(rec.expiresAt) {
			delete(m.data, id)
		}
	}
}

func (m *SessionManager) writeCookie(c *Context, value string, maxAgeSeconds int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     m.opts.CookieName,
		Value:    value,
		Path:     m.opts.Path,
		HttpOnly: m.opts.HttpOnly,
		Secure:   m.opts.Secure,
		SameSite: m.opts.SameSite,
		MaxAge:   maxAgeSeconds,
		Expires:  time.Now().Add(time.Duration(maxAgeSeconds) * time.Second),
	})
}

func cloneMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func generateSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 退化兜底：时间戳 + 固定前缀，保证函数不 panic
		return "sid_fallback_" + time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}
