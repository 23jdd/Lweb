# 轻量 Web 框架

这是一个用 Go 实现的轻量级 Web 框架示例，包含：

- 路由分组（`Group`）
- 中间件链（`Use` + `Next`）
- 路由参数（如 `/users/{id}`）
- 常见响应能力（`String` / `Json` / `XML` / `HTML`）



## 快速开始

在项目根目录执行：

```bash
go run .
```

启动后监听地址：`http://localhost:8080`

## main 示例（可直接运行）

当前 `main.go` 已提供完整示例，包含：

- 全局中间件：内置日志（`engine.Log()`）
- `NoRoute`：未匹配路由的统一兜底处理
- `NoMethod`：路径存在但方法不匹配时返回 405
- `GET /api/hello`：查询参数示例
- `GET /api/users/{id}`：路径参数示例
- `GET /api/static/*filepath`：通配参数示例（捕获剩余路径）
- `Static("/assets", "./static")`：静态目录映射示例
- `POST /api/echo`：JSON 绑定与回显示例

## 接口示例

### 1) 查询参数

```bash
curl "http://localhost:8080/api/hello?name=cursor"
```

返回：

```text
hello cursor
```

### 2) 路径参数

```bash
curl "http://localhost:8080/api/users/123"
```

返回：

```json
{"id":"123","name":"demo-user"}
```

### 3) JSON 请求体

```bash
curl -X POST "http://localhost:8080/api/echo" \
  -H "Content-Type: application/json" \
  -d "{\"msg\":\"你好\"}"
```

返回：

```json
{"received":{"msg":"你好"}}
```

### 4) 通配参数（`*arg`）

```bash
curl "http://localhost:8080/api/static/css/app.css"
```

返回：

```json
{"filepath":"css/app.css"}
```

### 5) 未匹配路由（NoRoute）

```bash
curl "http://localhost:8080/not-found"
```

返回：

```json
{"message":"接口不存在","path":"/not-found"}
```

### 6) 方法不匹配（NoMethod）

```bash
curl -X PUT "http://localhost:8080/api/hello"
```

返回（默认）：

```json
{"message":"405 method not allowed"}
```

### 7) 静态文件（Static）

先在项目目录创建文件 `static/hello.txt`，内容例如：

```text
hello static
```

请求：

```bash
curl "http://localhost:8080/api/assets/hello.txt"
```

返回：

```text
hello static
```

## 常用 API 说明

- `NewEngine()`：创建引擎实例。
- `Group(path)`：创建路由分组。
- `Use(handler)`：注册中间件。
- `Log()`：注册内置请求日志中间件，打印方法、路径、IP、状态码、耗时。
- `NoRoute(handler)`：设置未匹配路由时的兜底处理函数。
- `NoMethod(handler)`：设置路径存在但方法不匹配时的兜底处理函数。
- `Static(relativePath, root)`：把 URL 前缀映射到本地目录（内部使用 `*filepath` 捕获剩余路径）。
- `GET/POST/PUT/DELETE...`：注册 HTTP 路由。
- `c.Query(key)`：读取 URL 查询参数。
- `c.Param(key)`：读取路径参数（对应 `{key}`）。
- `c.BindJSON(&obj)`：把请求体 JSON 解析到结构体/Map。
- `c.String()/c.Json()/c.XML()/c.HTML()`：输出响应。
- `c.Fail(code, msg)`：返回统一错误并终止后续处理。

## 路由参数写法

本项目支持两种参数语法：

- 单段参数：`/api/users/{id}`
- 通配参数：`/api/static/*filepath`（必须放在路由最后一段）
- 错误写法：`/api/users/:id`（当前实现不支持）

路由注册会进行冲突检测，典型冲突包括：

- 同层 `{id}` 与 `{name}` 参数名冲突（语义等价，容易歧义）
- `*filepath` 与同层静态段混用冲突

## 运行时增强

- 响应写保护：避免重复 `WriteHeader` 造成状态码混乱。
- 静态文件优化：`Static` 内部改为 `http.FileServer`，默认附带 `Cache-Control`。
- 优雅关闭：`Run` 内置 `http.Server` 超时配置，并在 `Ctrl+C`/`SIGTERM` 时优雅退出。

