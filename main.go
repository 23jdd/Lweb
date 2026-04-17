package main

import "fmt"

func main() {
	engine := NewEngine()

	// 全局中间件：使用内置日志
	engine.Log()

	engine.NoRoute(func(c *Context) {
		c.Json(404, map[string]any{
			"message": "接口不存在",
			"path":    c.Path,
		})
	})

	api := engine.Group("/api")
	api.Use(func(c *Context) {
		c.Set("hello", "world")
		c.Next()

	})
	api.GET("/hello", func(c *Context) {
		name := c.DefaultQuery("name", "world")
		c.String(200, "hello "+name)
		get, ok := c.Get("hello")
		if ok {
			fmt.Println(get)
		}
	})

	api.GET("/users/{id}/{name}", func(c *Context) {
		c.Json(200, map[string]any{
			"id":   c.Param("id"),
			"name": c.Param("name"),
		})
	})

	api.GET("/static/*filepath", func(c *Context) {
		c.Json(200, map[string]any{
			"filepath": c.Param("filepath"),
		})
	})
	api.Static("/assets", "./static")

	api.POST("/echo", func(c *Context) {
		var body map[string]any
		if err := c.BindJSON(&body); err != nil {
			c.Fail(400, "JSON 解析失败")
			return
		}
		c.Json(200, map[string]any{
			"received": body,
		})
	})
	api.GET("/what", func(c *Context) {
		c.Json(200, map[string]any{})
	})

	if err := engine.Run(":8080"); err != nil {
		panic(err)
	}
}
