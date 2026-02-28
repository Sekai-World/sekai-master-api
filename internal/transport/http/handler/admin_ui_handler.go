package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
)

//go:embed admin_static/*
var adminStaticFS embed.FS

type AdminUIHandler struct {
	assets http.FileSystem
}

func NewAdminUIHandler(cfg config.Config) *AdminUIHandler {
	assetsFS, err := newAdminAssetsFS(cfg)
	if err != nil {
		panic(err)
	}

	return &AdminUIHandler{
		assets: http.FS(assetsFS),
	}
}

func newAdminAssetsFS(cfg config.Config) (fs.FS, error) {
	if cfg.IsDevelopment() {
		if _, currentFilePath, _, ok := runtime.Caller(0); ok {
			assetsDir := filepath.Join(filepath.Dir(currentFilePath), "admin_static")
			if stat, err := os.Stat(assetsDir); err == nil && stat.IsDir() {
				return os.DirFS(assetsDir), nil
			}
		}
	}

	return fs.Sub(adminStaticFS, "admin_static")
}

func (handler *AdminUIHandler) LoginPage(c *gin.Context) {
	c.FileFromFS("login.html", handler.assets)
}

func (handler *AdminUIHandler) DashboardPage(c *gin.Context) {
	c.FileFromFS("dashboard.html", handler.assets)
}

func (handler *AdminUIHandler) Asset(c *gin.Context) {
	assetPath := strings.TrimPrefix(c.Param("filepath"), "/")
	if assetPath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	c.FileFromFS(assetPath, handler.assets)
}
