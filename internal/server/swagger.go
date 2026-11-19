package server

import (
	"bytes"
	_ "embed"
	"net/http"

	swaggerFiles "github.com/swaggo/files"

	"github.com/ma6254/tg_down/internal/build"
)

// openAPISpec 是维护在本仓库的 OpenAPI 3 规范（internal/server/openapi.json）。
// 新增接口时同步修改该文件，Swagger UI 与 /api/openapi.json 会自动生效。
//
//go:embed openapi.json
var openAPISpec []byte

// versionPlaceholder 在规范里占位，运行时替换成 build.Version，
// 避免 openapi.json 的版本号与二进制版本漂移。
const versionPlaceholder = "__VERSION__"

// mountSwagger 挂载 OpenAPI 规范与 Swagger UI：
//
//   - GET /api/openapi.json：机器可读规范
//   - GET /swagger/：Swagger UI，资源来自 github.com/swaggo/files（内嵌，不依赖 CDN）
//
// 两者都不要求 token：文档不含敏感数据，鉴权信息在页面里的 Authorize 中填写。
func (s *Server) mountSwagger(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/openapi.json", s.handleOpenAPISpec)

	// 覆盖内嵌的 swagger-initializer.js，让它指向本服务的规范地址。
	mux.HandleFunc("GET /swagger/swagger-initializer.js", handleSwaggerInitializer)

	mux.Handle("GET /swagger/", http.StripPrefix("/swagger", http.FileServer(swaggerFiles.HTTP)))
	mux.Handle("GET /swagger", http.RedirectHandler("/swagger/", http.StatusMovedPermanently))
}

// handleOpenAPISpec 输出运行时版本已替换的规范。
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	spec := bytes.ReplaceAll(openAPISpec, []byte(versionPlaceholder), []byte(build.Version))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(spec)
}

// handleSwaggerInitializer 让 Swagger UI 加载本服务的规范。
func handleSwaggerInitializer(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(swaggerInitializerJS))
}

// swaggerInitializerJS 是 Swagger UI 的启动配置：
//   - url 指向本服务的规范；
//   - persistAuthorization 让 Authorize 填入的 token 刷新页面后仍然有效，
//     否则每次刷新都要重新粘贴 token。
const swaggerInitializerJS = `window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "/api/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: true,
    displayRequestDuration: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout"
  });
};
`
