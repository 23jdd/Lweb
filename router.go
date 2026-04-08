package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Context struct {
	Writer http.ResponseWriter
	Req    *http.Request
	// 请求信息
	Path   string
	Method string
	Params map[string]string // 路由参数 /user/:id
	// 响应信息
	StatusCode int
	// 中间件相关
	index  int // 当前执行到第几个中间件
	chains []Handler
	store  map[string]any
	done   bool
}

// Query 获取 URL 查询参数 /hello?name=xxx
func (c *Context) Query(key string) string {
	return c.Req.URL.Query().Get(key)
}

// DefaultQuery 带默认值的查询参数
func (c *Context) DefaultQuery(key, defaultValue string) string {
	if value := c.Query(key); value != "" {
		return value
	}
	return defaultValue
}

// PostForm 获取表单数据
func (c *Context) PostForm(key string) string {
	return c.Req.FormValue(key)
}

// Param 获取路由参数 /user/:id → c.Param("id")
func (c *Context) Param(key string) string {
	return c.Params[key]
}

// Header 获取请求头
func (c *Context) Header(key string) string {
	return c.Req.Header.Get(key)
}

// Status 设置 HTTP 状态码
func (c *Context) Status(code int) {
	if c.done {
		return
	}
	c.StatusCode = code
	c.Writer.WriteHeader(code)
}

// SetHeader 设置响应头
func (c *Context) SetHeader(key, value string) {
	c.Writer.Header().Set(key, value)
}

func (c *Context) Reset(w http.ResponseWriter, req *http.Request, chains []Handler, params map[string]string) {
	c.Writer = &responseWriter{ResponseWriter: w}
	c.Req = req
	c.Path = req.URL.Path
	c.Method = req.Method
	c.Params = params
	c.index = -1
	c.StatusCode = 0
	c.done = false
	if c.store == nil {
		c.store = make(map[string]any, 8)
	} else {
		clear(c.store)
	}
	c.chains = chains
}

type RouteGroup struct {
	T    *Trie
	Path string
}
type Engine struct {
	*RouteGroup
	pool     sync.Pool
	noRoute  Handler
	noMethod Handler
	server   *http.Server
}
type Handler func(*Context)

var routeMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}

func Split(path string) []string {
	if path == "" {
		return nil
	}
	parts := make([]string, 0, strings.Count(path, "/")+1)
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if start < i {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}
func Transform(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}
func (r *RouteGroup) Use(handler Handler) {
	for _, method := range routeMethods {
		r.T.Insert(Split(method+r.Path), handler, true)
	}
}

// Group 创建子路由组，复用同一棵路由树
func (r *RouteGroup) Group(relativePath string) *RouteGroup {
	return &RouteGroup{
		T:    r.T,
		Path: joinPaths(r.Path, relativePath),
	}
}

func NewGroup(path string) *RouteGroup {
	path = Transform(path)
	return &RouteGroup{
		Path: path,
		T:    NewRouterTree(),
	}
}

func joinPaths(basePath, relativePath string) string {
	if relativePath == "" || relativePath == "/" {
		return Transform(basePath)
	}
	if basePath == "" || basePath == "/" {
		return Transform(relativePath)
	}
	return Transform(basePath + "/" + strings.TrimPrefix(relativePath, "/"))
}
func (r *RouteGroup) GET(uri string, handler Handler) {
	uri = Transform(uri)
	split := Split("GET" + r.Path + uri)
	r.T.Insert(split, handler, false)
}
func (r *RouteGroup) POST(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("POST"+r.Path+uri), handler, false)
}
func (r *RouteGroup) PUT(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("PUT"+r.Path+uri), handler, false)
}
func (r *RouteGroup) DELETE(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("DELETE"+r.Path+uri), handler, false)
}
func (r *RouteGroup) PATCH(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("PATCH"+r.Path+uri), handler, false)
}
func (r *RouteGroup) HEAD(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("HEAD"+r.Path+uri), handler, false)
}
func (r *RouteGroup) OPTIONS(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("OPTIONS"+r.Path+uri), handler, false)
}
func (r *RouteGroup) TRACE(uri string, handler Handler) {
	uri = Transform(uri)
	r.T.Insert(Split("TRACE"+r.Path+uri), handler, false)
}

// Static 将 URL 前缀映射到本地静态目录，例如 /assets -> ./public
func (r *RouteGroup) Static(relativePath, root string) {
	pattern := joinPaths(relativePath, "*filepath")
	prefix := joinPaths(r.Path, relativePath)
	fileServer := http.StripPrefix(prefix, http.FileServer(http.Dir(root)))
	r.GET(pattern, func(c *Context) {
		if c.done {
			return
		}
		c.SetHeader("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(c.Writer, c.Req)
		c.StatusCode = httpStatus(c.Writer, http.StatusOK)
		c.done = true
	})
}
func (c *Context) Next() {
	c.index++
	if c.index >= len(c.chains) {
		return
	}
	c.chains[c.index](c)
}
func (c *Context) Abort() {
	c.index = len(c.chains) - 1 //
}
func (c *Context) Set(key string, val any) {
	c.store[key] = val
}
func (c *Context) Get(key string) (any, bool) {
	v, ok := c.store[key]
	return v, ok
}

func (c *Context) SetInt(key string, val int) {
	c.Set(key, val)
}

func (c *Context) GetInt(key string) (int, bool) {
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	val, ok := v.(int)
	return val, ok
}

func (c *Context) GetIntDefault(key string, defaultValue int) int {
	if val, ok := c.GetInt(key); ok {
		return val
	}
	return defaultValue
}

func (c *Context) SetInt64(key string, val int64) {
	c.Set(key, val)
}

func (c *Context) GetInt64(key string) (int64, bool) {
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	val, ok := v.(int64)
	return val, ok
}

func (c *Context) SetString(key, val string) {
	c.Set(key, val)
}

func (c *Context) GetString(key string) (string, bool) {
	v, ok := c.Get(key)
	if !ok {
		return "", false
	}
	val, ok := v.(string)
	return val, ok
}

func (c *Context) GetStringDefault(key, defaultValue string) string {
	if val, ok := c.GetString(key); ok {
		return val
	}
	return defaultValue
}

func (c *Context) SetBool(key string, val bool) {
	c.Set(key, val)
}

func (c *Context) GetBool(key string) (bool, bool) {
	v, ok := c.Get(key)
	if !ok {
		return false, false
	}
	val, ok := v.(bool)
	return val, ok
}

func (c *Context) GetBoolDefault(key string, defaultValue bool) bool {
	if val, ok := c.GetBool(key); ok {
		return val
	}
	return defaultValue
}

func (c *Context) SetFloat64(key string, val float64) {
	c.Set(key, val)
}

func (c *Context) GetFloat64(key string) (float64, bool) {
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	val, ok := v.(float64)
	return val, ok
}

func (c *Context) MustGet(key string) any {
	val, ok := c.Get(key)
	if !ok {
		panic(fmt.Sprintf("context key not found: %s", key))
	}
	return val
}
func NewEngine() *Engine {
	return &Engine{
		pool: sync.Pool{New: func() interface{} {
			return &Context{}
		}},
		RouteGroup: NewGroup("/"),
		noRoute: func(c *Context) {
			c.Json(http.StatusNotFound, map[string]string{
				"message": "404 page not found",
			})
		},
		noMethod: func(c *Context) {
			c.Json(http.StatusMethodNotAllowed, map[string]string{
				"message": "405 method not allowed",
			})
		},
	}
}

// Group 创建一级路由组，语义上等价于 e.RouteGroup.Group(...)
func (e *Engine) Group(relativePath string) *RouteGroup {
	return e.RouteGroup.Group(relativePath)
}

// Log 注册框架内置请求日志中间件
func (e *Engine) Log() {
	e.Use(Log())
}

// Log 返回一个通用请求日志中间件
func Log() Handler {
	return func(c *Context) {
		start := time.Now()
		c.Next()
		status := c.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		fmt.Printf("[%s] %s %s %d %v\n", c.Method, c.Path, c.ClientIP(), status, time.Since(start))
	}
}

// NoRoute 设置未匹配路由时的兜底处理函数
func (e *Engine) NoRoute(handler Handler) {
	if handler == nil {
		return
	}
	e.noRoute = handler
}

// NoMethod 设置路径存在但方法不匹配时的兜底处理函数
func (e *Engine) NoMethod(handler Handler) {
	if handler == nil {
		return
	}
	e.noMethod = handler
}
func (c *Context) String(StatusCode int, content string) {
	if c.done {
		return
	}
	c.SetHeader("Content-Type", "text/plain; charset=utf-8")
	c.Status(StatusCode)
	_, err := c.Writer.Write([]byte(content))
	if err != nil {
		panic(err)
	}
	c.StatusCode = StatusCode
	c.done = true
}
func (c *Context) Json(StatusCode int, content interface{}) {
	if c.done {
		return
	}
	body, err := json.Marshal(content)
	if err != nil {
		http.Error(c.Writer, fmt.Sprintf(`{"error":"json encode failed: %v"}`, err), http.StatusInternalServerError)
		c.StatusCode = http.StatusInternalServerError
		c.done = true
		return
	}
	c.SetHeader("Content-Type", "application/json; charset=utf-8")
	c.Status(StatusCode)
	if _, err = c.Writer.Write(body); err != nil {
		panic(err)
	}
	c.StatusCode = StatusCode
	c.done = true
}
func (c *Context) XML(statusCode int, content interface{}) {
	if c.done {
		return
	}
	body, err := xml.Marshal(content)
	if err != nil {
		http.Error(c.Writer, fmt.Sprintf("xml encode failed: %v", err), http.StatusInternalServerError)
		c.StatusCode = http.StatusInternalServerError
		c.done = true
		return
	}
	c.SetHeader("Content-Type", "application/xml; charset=utf-8")
	c.Status(statusCode)
	if _, err = c.Writer.Write(body); err != nil {
		panic(err)
	}
	c.StatusCode = statusCode
	c.done = true
}

func (c *Context) HTML(statusCode int, htmlText string) {
	if c.done {
		return
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.Status(statusCode)
	_, err := c.Writer.Write([]byte(htmlText))
	if err != nil {
		panic(err)
	}
	c.StatusCode = statusCode
	c.done = true
}

func (c *Context) HTMLTemplate(statusCode int, tpl string, data any) {
	if c.done {
		return
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	t, err := template.New("inline").Parse(tpl)
	if err != nil {
		c.Fail(http.StatusInternalServerError, fmt.Sprintf("模板解析失败: %v", err))
		return
	}
	c.Status(statusCode)
	if err = t.Execute(c.Writer, data); err != nil {
		c.Fail(http.StatusInternalServerError, fmt.Sprintf("模板渲染失败: %v", err))
		return
	}
	c.StatusCode = statusCode
	c.done = true
}

func (c *Context) File(filePath string) {
	if c.done {
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		c.Fail(http.StatusNotFound, "文件不存在")
		return
	}
	http.ServeFile(c.Writer, c.Req, filePath)
	c.StatusCode = httpStatus(c.Writer, http.StatusOK)
	c.done = true
}

func (c *Context) Redirect(statusCode int, location string) {
	if c.done {
		return
	}
	c.StatusCode = statusCode
	http.Redirect(c.Writer, c.Req, location, statusCode)
	c.done = true
}

// FileAttachment 以附件形式返回文件，触发浏览器下载
func (c *Context) FileAttachment(filePath, filename string) {
	if c.done {
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		c.Fail(http.StatusNotFound, "文件不存在")
		return
	}
	if filename == "" {
		filename = filepath.Base(filePath)
	}
	escapedName := strings.ReplaceAll(filename, `"`, `'`)
	c.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, escapedName))
	http.ServeFile(c.Writer, c.Req, filePath)
	c.StatusCode = httpStatus(c.Writer, http.StatusOK)
	c.done = true
}

// ClientIP 获取客户端真实 IP，优先读取代理头
func (c *Context) ClientIP() string {
	// 常见反向代理头优先级：X-Forwarded-For > X-Real-IP
	if xff := c.Header("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For 可能是逗号分隔链路，首个通常是原始客户端 IP
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xrip := strings.TrimSpace(c.Header("X-Real-IP")); xrip != "" {
		return xrip
	}

	// 回退到 RemoteAddr（通常是 IP:Port）
	remoteAddr := strings.TrimSpace(c.Req.RemoteAddr)
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return remoteAddr
}

// Data 返回二进制或原始字节数据
func (c *Context) Data(statusCode int, data []byte) {
	if c.done {
		return
	}
	c.Status(statusCode)
	_, err := c.Writer.Write(data)
	if err != nil {
		panic(err)
	}
	c.StatusCode = statusCode
	c.done = true
}

// BindJSON 解析请求 JSON 到目标结构体
func (c *Context) BindJSON(obj interface{}) error {
	decoder := json.NewDecoder(c.Req.Body)
	if err := decoder.Decode(obj); err != nil {
		return err
	}
	return nil
}

// Fail 返回统一错误响应，并中断后续处理
func (c *Context) Fail(code int, msg string) {
	c.index = len(c.chains)
	c.Json(code, map[string]string{
		"message": msg,
	})
}
func (e *Engine) GetChains(path string) ([]Handler, map[string]string) {
	search, params, err := e.T.Search(Split(path))
	if err != nil {
		return nil, nil
	}
	return search, params
}
func (e *Engine) hasPath(path string) bool {
	for _, method := range routeMethods {
		search, _, err := e.T.Search(Split(method + path))
		if err == nil && len(search) > 0 {
			return true
		}
	}
	return false
}
func (e *Engine) Run(addr string) error {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		context := e.pool.Get().(*Context)
		defer e.pool.Put(context)
		url := r.Method + r.URL.Path
		chains, params := e.GetChains(url)
		if chains == nil {
			if e.hasPath(r.URL.Path) {
				context.Reset(w, r, []Handler{e.noMethod}, map[string]string{})
				context.Next()
				return
			}
			context.Reset(w, r, []Handler{e.noRoute}, map[string]string{})
			context.Next()
			return
		}
		context.Reset(w, r, chains, params)
		context.Next()
	})
	e.server = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.server.Shutdown(shutdownCtx)
	}()
	err := e.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(p []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(p)
}

func httpStatus(w http.ResponseWriter, fallback int) int {
	rw, ok := w.(*responseWriter)
	if !ok {
		return fallback
	}
	if rw.status == 0 {
		return fallback
	}
	return rw.status
}
